// Package core: typed predicates for ContentBlock and Message unions.
//
// Type assertions in Go are common but verbose when chained (`switch
// b.(type)`). These helpers extract the inner value in one call and
// return false on a mismatch, making call sites more readable.
//
// Source: pi-mono packages/ai/src/api/openai-completions.ts and similar
//         isXxxContentBlock / isXxxMessage helpers across providers.
package core

// --- ContentBlock guards ------------------------------------------------------

// IsTextContent returns the TextContent value and true if b is a TextContent;
// otherwise the zero TextContent and false.
func IsTextContent(b ContentBlock) (TextContent, bool) {
	v, ok := b.(TextContent)
	return v, ok
}

// IsThinkingContent returns the ThinkingContent value and true if b is a
// ThinkingContent.
func IsThinkingContent(b ContentBlock) (ThinkingContent, bool) {
	v, ok := b.(ThinkingContent)
	return v, ok
}

// IsImageContent returns the ImageContent value and true if b is a
// ImageContent.
func IsImageContent(b ContentBlock) (ImageContent, bool) {
	v, ok := b.(ImageContent)
	return v, ok
}

// IsToolCall returns the ToolCall value and true if b is a ToolCall.
func IsToolCall(b ContentBlock) (ToolCall, bool) {
	v, ok := b.(ToolCall)
	return v, ok
}

// --- Message guards -----------------------------------------------------------

// IsUserMessage returns the UserMessage value and true if m is a UserMessage.
func IsUserMessage(m Message) (UserMessage, bool) {
	v, ok := m.(UserMessage)
	return v, ok
}

// IsAssistantMessage returns the AssistantMessage value and true if m is an
// AssistantMessage.
func IsAssistantMessage(m Message) (AssistantMessage, bool) {
	v, ok := m.(AssistantMessage)
	return v, ok
}

// IsToolResultMessage returns the ToolResultMessage value and true if m is
// a ToolResultMessage.
func IsToolResultMessage(m Message) (ToolResultMessage, bool) {
	v, ok := m.(ToolResultMessage)
	return v, ok
}

// --- Convenience predicates ----------------------------------------------------

// HasToolCall reports whether the assistant message contains any tool calls.
func HasToolCall(msg AssistantMessage) bool {
	for _, b := range msg.Content {
		if _, ok := b.(ToolCall); ok {
			return true
		}
	}
	return false
}

// FirstText returns the concatenated text from all TextContent blocks in
// msg.Content. Useful for logging or summary display.
func FirstText(msg AssistantMessage) string {
	for _, b := range msg.Content {
		if t, ok := b.(TextContent); ok {
			return t.Text
		}
	}
	return ""
}