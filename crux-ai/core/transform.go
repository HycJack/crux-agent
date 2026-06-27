package core

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"time"
)

// =============================================================================
// Message transformation for cross-provider compatibility.
//
// Reference: pi-mono packages/ai/src/api/transform-messages.ts
//
// The crux-ai Context holds a history of mixed-provider assistant messages
// (e.g. an Anthropic reply followed by an OpenAI Responses reply). When we
// dispatch the next turn to a particular provider, several fixes are required
// for that target:
//
//  1. Tool call ID normalization: OpenAI Responses API emits IDs that are
//     450+ characters long and may include characters like '|'. Anthropic
//     APIs require IDs matching ^[a-zA-Z0-9_-]+$ and at most 64 chars.
//     We hash+truncate offending IDs to a safe short form and rewrite the
//     dependent toolResult.toolCallId to the new value.
//
//  2. Thinking block handling:
//     - Redacted thinking blocks are opaque encrypted content; they are
//       only valid for the *same* model that produced them. Drop them on
//       cross-model replay to avoid API rejection.
//     - Empty/whitespace thinking blocks are dropped.
//     - For same-model replay we keep the signature (needed for replay).
//     - For cross-model we downgrade the block to a plain text block so
//       the downstream model can see the reasoning.
//
//  3. Image downgrade: when the target model.input does NOT include the
//     "image" modality, image blocks in user/tool messages are replaced
//     with a placeholder text block.
//
//  4. Aborted/errored assistant messages are skipped entirely: they are
//     incomplete turns that cannot be safely replayed.
//
//  5. Synthetic tool result injection: orphaned tool calls (no matching
//     toolResult in the next message) get a synthetic error result so the
//     provider API does not reject the request.
// =============================================================================

// NonVisionUserImagePlaceholder is the marker text inserted into user
// messages that contain an image but the target model does not support
// vision. Configurable for tests.
const NonVisionUserImagePlaceholder = "(image omitted: model does not support images)"

// NonVisionToolImagePlaceholder is the marker text inserted into tool
// results that contain an image but the target model does not support
// vision.
const NonVisionToolImagePlaceholder = "(tool image omitted: model does not support images)"

// toolCallIDNormalizer returns a transformed tool call ID. Default impl
// hashes+truncates so the result is <=64 chars and matches [a-zA-Z0-9_-]+.
// Replaceable by callers (e.g. to preserve human-readable prefixes).
type ToolCallIDNormalizer func(id string, model Model) string

// toolCallIDPattern matches IDs Anthropic accepts: alphanumerics, '-', '_'.
// Length cap 64 chars enforced separately.
var toolCallIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// DefaultToolCallIDNormalizer rewrites an ID to a 64-char-or-less
// hash-and-prefix form. Anthropic-compatible APIs (Anthropic, Bedrock)
// call this; OpenAI-style providers receive the ID as-is.
func DefaultToolCallIDNormalizer(id string, _ Model) string {
	if id == "" {
		return id
	}
	if len(id) <= 64 && toolCallIDPattern.MatchString(id) {
		return id
	}
	// SHA-256 hex is 64 chars; drop the trailing bytes when the input
	// already gives us <64 safe chars. We always include a deterministic
	// prefix so the call site can recognize hash-form IDs.
	sum := sha256.Sum256([]byte(id))
	hash := hex.EncodeToString(sum[:])[:32]
	return "tc_" + hash
}

// supportsVision reports whether the model's input modality list includes
// images.
func supportsVision(model Model) bool {
	for _, m := range model.Input {
		if m == "image" {
			return true
		}
	}
	return false
}

// replaceImagesWithPlaceholder collapses all images in a content slice to
// a single placeholder text block. Adjacent placeholders are merged to
// avoid noise.
func replaceImagesWithPlaceholder(content []ContentBlock, placeholder string) []ContentBlock {
	out := make([]ContentBlock, 0, len(content))
	prevWasPlaceholder := false
	for _, b := range content {
		if _, ok := b.(ImageContent); ok {
			if !prevWasPlaceholder {
				out = append(out, TextContent{Type: "text", Text: placeholder})
			}
			prevWasPlaceholder = true
			continue
		}
		out = append(out, b)
		if t, ok := b.(TextContent); ok {
			prevWasPlaceholder = t.Text == placeholder
		} else {
			prevWasPlaceholder = false
		}
	}
	return out
}

// downgradeUnsupportedImages returns a copy of messages with image blocks
// replaced by placeholders for models that do not support vision input.
func downgradeUnsupportedImages(messages []Message, model Model) []Message {
	if supportsVision(model) {
		return messages
	}
	out := make([]Message, len(messages))
	for i, msg := range messages {
		switch m := msg.(type) {
		case UserMessage:
			if arr, ok := m.Content.([]ContentBlock); ok {
				m.Content = replaceImagesWithPlaceholder(arr, NonVisionUserImagePlaceholder)
			}
			out[i] = m
		case ToolResultMessage:
			m.Content = replaceImagesWithPlaceholder(m.Content, NonVisionToolImagePlaceholder)
			out[i] = m
		default:
			out[i] = msg
		}
	}
	return out
}

// isSameModel returns true if an assistant message was produced by the
// same model we are about to dispatch to.
func isSameModel(am AssistantMessage, model Model) bool {
	return am.Provider == model.Provider && am.API == model.API && am.Model == model.ID
}

// TransformMessages prepares a message history for dispatch to a target
// model. See file-level comments for the full set of rules.
//
// normalizeID is optional; when nil DefaultToolCallIDNormalizer is used
// (a no-op for already-conformant IDs).
func TransformMessages(messages []Message, model Model, normalizeID ToolCallIDNormalizer) []Message {
	if normalizeID == nil {
		normalizeID = DefaultToolCallIDNormalizer
	}

	imageAware := downgradeUnsupportedImages(messages, model)

	// Pass 1: build the complete idMap from ALL AssistantMessages before any
	// ToolResultMessage is rewritten. This makes the rewrite correct even when
	// a tool result appears before the assistant turn that created it.
	idMap := make(map[string]string)
	for _, msg := range imageAware {
		am, ok := msg.(AssistantMessage)
		if !ok || am.StopReason == "error" || am.StopReason == "aborted" {
			continue
		}
		same := isSameModel(am, model)
		for _, b := range am.Content {
			if tc, ok := b.(ToolCall); ok && !same {
				normalized := normalizeID(tc.ID, model)
				if normalized != tc.ID {
					idMap[tc.ID] = normalized
				}
			}
		}
	}

	// Pass 2: apply all transformations using the complete idMap.
	transformed := make([]Message, 0, len(imageAware))
	var pendingTCs []ToolCall
	existingResults := make(map[string]bool)
	flushPending := func() {
		for _, tc := range pendingTCs {
			if existingResults[tc.ID] {
				continue
			}
			transformed = append(transformed, ToolResultMessage{
				Role:       "toolResult",
				ToolCallID: tc.ID,
				ToolName:   tc.Name,
				Content:    []ContentBlock{TextContent{Type: "text", Text: "No result provided"}},
				IsError:    true,
				Timestamp:  time.Now(),
			})
		}
		pendingTCs = nil
		existingResults = make(map[string]bool)
	}

	for _, msg := range imageAware {
		if _, skip := msg.(nilMessage); skip {
			continue
		}
		if am, ok := msg.(AssistantMessage); ok {
			if am.StopReason == "error" || am.StopReason == "aborted" {
				continue
			}
			flushPending()
			same := isSameModel(am, model)
			newContent := make([]ContentBlock, 0, len(am.Content))
			for _, b := range am.Content {
				switch bb := b.(type) {
				case ThinkingContent:
					if bb.Redacted {
						if same {
							newContent = append(newContent, bb)
						}
						continue
					}
					if same && bb.ThinkingSignature != "" {
						newContent = append(newContent, bb)
						continue
					}
					if strings.TrimSpace(bb.Thinking) == "" {
						continue
					}
					if same {
						newContent = append(newContent, bb)
						continue
					}
					newContent = append(newContent, TextContent{Type: "text", Text: bb.Thinking})
				case TextContent:
					if same {
						newContent = append(newContent, bb)
					} else {
						newContent = append(newContent, TextContent{Type: "text", Text: bb.Text})
					}
				case ToolCall:
					tc := bb
					if !same && tc.ThoughtSignature != "" {
						tc.ThoughtSignature = ""
					}
					if !same {
						if mapped, ok := idMap[tc.ID]; ok {
							tc.ID = mapped
						}
					}
					newContent = append(newContent, tc)
				default:
					newContent = append(newContent, b)
				}
			}
			am.Content = newContent
			tcs := make([]ToolCall, 0)
			for _, b := range am.Content {
				if tc, ok := b.(ToolCall); ok {
					tcs = append(tcs, tc)
				}
			}
			if len(tcs) > 0 {
				pendingTCs = tcs
			}
			transformed = append(transformed, am)
			continue
		}
		if tr, ok := msg.(ToolResultMessage); ok {
			if mapped, ok := idMap[tr.ToolCallID]; ok {
				tr.ToolCallID = mapped
			}
			existingResults[tr.ToolCallID] = true
			transformed = append(transformed, tr)
			continue
		}
		transformed = append(transformed, msg)
	}
	flushPending()

	return transformed
}

// nilMessage is an internal marker that the second pass strips out.
type nilMessage struct{}

func (nilMessage) messageTag()             {}
func (nilMessage) GetTimestamp() time.Time { return time.Time{} }

// PruneThinking truncates ThinkingContent blocks in msg to at most maxChars
// runes, preserving the signature (so the model can still replay its own
// reasoning). Returns msg unchanged if maxChars <= 0 or no ThinkingContent
// is present.
//
// Source: pi-mono packages/ai/src/api/transform-messages.ts (thinking
// truncation pattern).
// || 把 ThinkingContent 截断到 maxChars 个 rune（保留签名）。
func PruneThinking(msg AssistantMessage, maxChars int) AssistantMessage {
	if maxChars <= 0 {
		return msg
	}
	out := msg
	out.Content = make([]ContentBlock, 0, len(msg.Content))
	for _, b := range msg.Content {
		t, ok := b.(ThinkingContent)
		if !ok {
			out.Content = append(out.Content, b)
			continue
		}
		if utf8RuneCount(t.Thinking) <= maxChars {
			out.Content = append(out.Content, t)
			continue
		}
		runes := []rune(t.Thinking)
		t.Thinking = string(runes[:maxChars]) + "…"
		out.Content = append(out.Content, t)
	}
	return out
}

// utf8RuneCount returns the rune count of s without allocating a full
// []rune. Cheap and adequate for size comparisons.
func utf8RuneCount(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}
