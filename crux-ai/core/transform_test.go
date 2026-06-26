package core

import (
	"strings"
	"testing"
	"time"
)

func TestDefaultToolCallIDNormalizer(t *testing.T) {
	// Safe ID is returned unchanged.
	if got := DefaultToolCallIDNormalizer("safe_id-123", Model{}); got != "safe_id-123" {
		t.Errorf("safe id: got %q", got)
	}
	// Long ID with pipe is rewritten to hash form <= 64 chars.
	long := "fc_abc|def|with|pipe|" + strings.Repeat("x", 200)
	got := DefaultToolCallIDNormalizer(long, Model{})
	if len(got) > 64 {
		t.Errorf("length: got %d", len(got))
	}
	if !toolCallIDPattern.MatchString(got) {
		t.Errorf("must match safe pattern, got %q", got)
	}
	if !strings.HasPrefix(got, "tc_") {
		t.Errorf("hash form should be prefixed, got %q", got)
	}
	// Same input -> same hash.
	if got2 := DefaultToolCallIDNormalizer(long, Model{}); got2 != got {
		t.Errorf("determinism: got %q then %q", got, got2)
	}
}

func TestTransformMessages_SkipsErroredAssistant(t *testing.T) {
	model := Model{ID: "claude-3-5-sonnet", Provider: ProviderAnthropic, API: APIAnthropicMessages, Input: []Modality{"text"}}
	msgs := []Message{
		UserMessage{Content: "hi"},
		AssistantMessage{Provider: "openai", API: "openai-responses", Model: "x", StopReason: StopError, Content: []ContentBlock{TextContent{Type: "text", Text: "partial"}}},
		UserMessage{Content: "again"},
	}
	out := TransformMessages(msgs, model, nil)
	if len(out) != 2 {
		t.Fatalf("errored message should be dropped, got %d", len(out))
	}
}

func TestTransformMessages_DowngradesImagesForNonVisionModel(t *testing.T) {
	model := Model{ID: "llama-3", Provider: ProviderOpenRouter, API: APIOpenAICompletions, Input: []Modality{"text"}}
	msgs := []Message{
		UserMessage{Content: []ContentBlock{
			TextContent{Type: "text", Text: "what is this?"},
			ImageContent{Type: "image", MimeType: "image/png", Data: "BASE64"},
		}},
	}
	out := TransformMessages(msgs, model, nil)
	um, ok := out[0].(UserMessage)
	if !ok {
		t.Fatalf("expected UserMessage, got %T", out[0])
	}
	blocks, ok := um.Content.([]ContentBlock)
	if !ok {
		t.Fatalf("expected blocks, got %T", um.Content)
	}
	// Expect 2 blocks: original text + placeholder for the image.
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks (text + placeholder), got %d", len(blocks))
	}
	if _, ok := blocks[0].(TextContent); !ok || blocks[0].(TextContent).Text != "what is this?" {
		t.Errorf("block 0: got %+v", blocks[0])
	}
	tc, ok := blocks[1].(TextContent)
	if !ok {
		t.Fatalf("expected placeholder TextContent, got %T", blocks[1])
	}
	if !strings.Contains(tc.Text, "image omitted") {
		t.Errorf("placeholder text: got %q", tc.Text)
	}

	// Adjacent images should produce a single placeholder (not N placeholders).
	msgs2 := []Message{
		UserMessage{Content: []ContentBlock{
			ImageContent{Type: "image", MimeType: "image/png", Data: "A"},
			ImageContent{Type: "image", MimeType: "image/png", Data: "B"},
		}},
	}
	out2 := TransformMessages(msgs2, model, nil)
	blocks2 := out2[0].(UserMessage).Content.([]ContentBlock)
	if len(blocks2) != 1 {
		t.Errorf("adjacent images should collapse to 1 placeholder, got %d", len(blocks2))
	}
}

func TestTransformMessages_KeepsImagesForVisionModel(t *testing.T) {
	model := Model{ID: "gpt-4o", Provider: ProviderOpenAI, API: APIOpenAIResponses, Input: []Modality{"text", "image"}}
	msgs := []Message{
		UserMessage{Content: []ContentBlock{
			TextContent{Type: "text", Text: "describe"},
			ImageContent{Type: "image", MimeType: "image/png", Data: "B64"},
		}},
	}
	out := TransformMessages(msgs, model, nil)
	um := out[0].(UserMessage)
	blocks := um.Content.([]ContentBlock)
	if len(blocks) != 2 {
		t.Errorf("vision model should keep both blocks, got %d", len(blocks))
	}
}

func TestTransformMessages_NormalizesToolCallIDForCrossModel(t *testing.T) {
	target := Model{ID: "claude-3-5-sonnet", Provider: ProviderAnthropic, API: APIAnthropicMessages, Input: []Modality{"text"}}
	longID := "fc_" + strings.Repeat("a", 200) + "|x"
	msgs := []Message{
		AssistantMessage{Provider: "openai", API: "openai-responses", Model: "gpt-4o", Content: []ContentBlock{
			ToolCall{ID: longID, Name: "search"},
		}},
		ToolResultMessage{ToolCallID: longID, ToolName: "search", Content: []ContentBlock{TextContent{Type: "text", Text: "ok"}}},
	}
	out := TransformMessages(msgs, target, nil)

	// Both messages should now reference the normalized id.
	am := out[0].(AssistantMessage)
	tc := am.Content[0].(ToolCall)
	if tc.ID == longID {
		t.Errorf("id was not normalized: %q", tc.ID)
	}
	if len(tc.ID) > 64 {
		t.Errorf("normalized id too long: %d", len(tc.ID))
	}
	tr := out[1].(ToolResultMessage)
	if tr.ToolCallID != tc.ID {
		t.Errorf("toolResult id not remapped: got %q, want %q", tr.ToolCallID, tc.ID)
	}
}

func TestTransformMessages_SameModelPreservesSignature(t *testing.T) {
	target := Model{ID: "claude-3-5-sonnet", Provider: ProviderAnthropic, API: APIAnthropicMessages, Input: []Modality{"text"}}
	msgs := []Message{
		AssistantMessage{Provider: ProviderAnthropic, API: APIAnthropicMessages, Model: "claude-3-5-sonnet", Content: []ContentBlock{
			ThinkingContent{Type: "thinking", Thinking: "thought", ThinkingSignature: "sig123"},
		}},
	}
	out := TransformMessages(msgs, target, nil)
	am := out[0].(AssistantMessage)
	th, ok := am.Content[0].(ThinkingContent)
	if !ok {
		t.Fatalf("expected ThinkingContent, got %T", am.Content[0])
	}
	if th.ThinkingSignature != "sig123" {
		t.Errorf("same-model signature dropped: %q", th.ThinkingSignature)
	}
}

func TestTransformMessages_CrossModelDowngradesThinking(t *testing.T) {
	target := Model{ID: "gpt-4o", Provider: ProviderOpenAI, API: APIOpenAIResponses, Input: []Modality{"text"}}
	msgs := []Message{
		AssistantMessage{Provider: ProviderAnthropic, API: APIAnthropicMessages, Model: "claude-3-5-sonnet", Content: []ContentBlock{
			ThinkingContent{Type: "thinking", Thinking: "private reasoning", ThinkingSignature: "sig"},
		}},
	}
	out := TransformMessages(msgs, target, nil)
	am := out[0].(AssistantMessage)
	tc, ok := am.Content[0].(TextContent)
	if !ok {
		t.Fatalf("expected cross-model downgrade to text, got %T", am.Content[0])
	}
	if tc.Text != "private reasoning" {
		t.Errorf("text: got %q", tc.Text)
	}
}

func TestTransformMessages_DropsEmptyThinking(t *testing.T) {
	target := Model{ID: "gpt-4o", Provider: ProviderOpenAI, API: APIOpenAIResponses, Input: []Modality{"text"}}
	msgs := []Message{
		AssistantMessage{Provider: ProviderAnthropic, API: APIAnthropicMessages, Model: "claude-3-5-sonnet", Content: []ContentBlock{
			ThinkingContent{Type: "thinking", Thinking: "   "},
		}},
	}
	out := TransformMessages(msgs, target, nil)
	am := out[0].(AssistantMessage)
	if len(am.Content) != 0 {
		t.Errorf("empty thinking should be dropped, got %d blocks", len(am.Content))
	}
}

func TestTransformMessages_RedactedOnlyForSameModel(t *testing.T) {
	same := Model{ID: "claude-3-5-sonnet", Provider: ProviderAnthropic, API: APIAnthropicMessages, Input: []Modality{"text"}}
	other := Model{ID: "gpt-4o", Provider: ProviderOpenAI, API: APIOpenAIResponses, Input: []Modality{"text"}}

	redacted := ThinkingContent{Type: "thinking", Thinking: "", Redacted: true, ThinkingSignature: "encrypted"}
	msgs := []Message{
		AssistantMessage{Provider: ProviderAnthropic, API: APIAnthropicMessages, Model: "claude-3-5-sonnet", Content: []ContentBlock{redacted}},
	}

	// Same model: kept.
	out := TransformMessages(msgs, same, nil)
	if _, ok := out[0].(AssistantMessage).Content[0].(ThinkingContent); !ok {
		t.Errorf("same-model should keep redacted thinking")
	}
	// Cross-model: dropped.
	out = TransformMessages(msgs, other, nil)
	if len(out[0].(AssistantMessage).Content) != 0 {
		t.Errorf("cross-model should drop redacted thinking")
	}
}

func TestTransformMessages_InsertsSyntheticResults(t *testing.T) {
	target := Model{ID: "gpt-4o", Provider: ProviderOpenAI, API: APIOpenAIResponses, Input: []Modality{"text"}}
	msgs := []Message{
		AssistantMessage{Provider: ProviderOpenAI, API: APIOpenAIResponses, Model: "gpt-4o", Content: []ContentBlock{
			ToolCall{ID: "orphan", Name: "search"},
		}},
		// No toolResult for "orphan".
	}
	out := TransformMessages(msgs, target, nil)
	if len(out) != 2 {
		t.Fatalf("expected 2 messages (assistant + synthetic toolResult), got %d", len(out))
	}
	tr, ok := out[1].(ToolResultMessage)
	if !ok {
		t.Fatalf("expected ToolResultMessage, got %T", out[1])
	}
	if tr.ToolCallID != "orphan" || !tr.IsError {
		t.Errorf("synthetic result: got %+v", tr)
	}
}

func TestTransformMessages_DropsThoughtSignatureCrossModel(t *testing.T) {
	target := Model{ID: "gpt-4o", Provider: ProviderOpenAI, API: APIOpenAIResponses, Input: []Modality{"text"}}
	msgs := []Message{
		AssistantMessage{Provider: ProviderAnthropic, API: APIAnthropicMessages, Model: "claude-3-5-sonnet", Content: []ContentBlock{
			ToolCall{ID: "ok_id", Name: "search", ThoughtSignature: "encrypted-thought"},
		}},
	}
	out := TransformMessages(msgs, target, nil)
	am := out[0].(AssistantMessage)
	tc := am.Content[0].(ToolCall)
	if tc.ThoughtSignature != "" {
		t.Errorf("cross-model should drop thoughtSignature, got %q", tc.ThoughtSignature)
	}
}

// Make sure nilMessage GetTimestamp satisfies the Message interface.
func TestNilMessage_SatisfiesMessage(t *testing.T) {
	var _ Message = nilMessage{}
	var _ = nilMessage{}.GetTimestamp().IsZero()
	var _ = time.Time{}
}
