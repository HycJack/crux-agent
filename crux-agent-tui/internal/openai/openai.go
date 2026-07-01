// Package openai implements an OpenAI-compatible chat completions provider.
// It speaks the standard OpenAI /chat/completions HTTP protocol and works
// with any OpenAI-compatible API (OpenAI, DeepSeek, Groq, XAI, Ollama, etc.).
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"crux-agent-tui/internal/provider"
)

type Provider struct {
	httpClient *http.Client
}

func New() *Provider {
	return &Provider{
		httpClient: &http.Client{
			Timeout: 10 * time.Minute,
		},
	}
}

func (p *Provider) Stream(ctx context.Context, llmCtx provider.LLMContext, opts provider.StreamOptions) (*provider.EventStream, error) {
	// Build the request body
	reqBody := map[string]any{
		"model":      opts.Model,
		"stream":     true,
		"max_tokens": opts.MaxTokens,
	}

	// Build messages
	var msgs []map[string]any
	systemMsgs := []string{}

	if llmCtx.SystemPrompt != "" && !opts.SkipSystem {
		systemMsgs = append(systemMsgs, llmCtx.SystemPrompt)
	}

	for _, msg := range llmCtx.Messages {
		m := map[string]any{
			"role": string(msg.Role),
		}

		switch msg.Role {
		case provider.RoleAssistant:
			// Assistant messages may have tool calls
			if len(msg.ToolCalls) > 0 {
				var content any = msg.Content
				if content == "" {
					content = nil
				}
				m["content"] = content

				tcs := make([]map[string]any, len(msg.ToolCalls))
				for i, tc := range msg.ToolCalls {
					tcs[i] = map[string]any{
						"id":   tc.ID,
						"type": "function",
						"function": map[string]any{
							"name":      tc.Name,
							"arguments": string(tc.Arguments),
						},
					}
				}
				m["tool_calls"] = tcs
			} else {
				m["content"] = msg.Content
			}

		case provider.RoleTool:
			m["tool_call_id"] = msg.ToolCallID
			m["content"] = msg.Content

		case provider.RoleUser:
			m["content"] = msg.Content

		default:
			m["content"] = msg.Content
		}

		msgs = append(msgs, m)
	}

	// If we have system messages, prepend them
	for _, sm := range systemMsgs {
		msgs = append([]map[string]any{{"role": "system", "content": sm}}, msgs...)
	}

	reqBody["messages"] = msgs

	// Add tools
	if len(llmCtx.Tools) > 0 {
		tools := make([]map[string]any, len(llmCtx.Tools))
		for i, t := range llmCtx.Tools {
			tools[i] = map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  t.Parameters,
				},
			}
		}
		reqBody["tools"] = tools
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	baseURL = strings.TrimRight(baseURL, "/")
	url := baseURL + "/chat/completions"

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if opts.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+opts.APIKey)
	}
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	stream := provider.NewEventStream()

	go p.readSSE(ctx, resp.Body, stream)

	return stream, nil
}

// openaiStreamChunk represents one SSE data line from OpenAI /chat/completions
type openaiStreamChunk struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []choice `json:"choices"`
	Usage   *usage   `json:"usage,omitempty"`
}

type choice struct {
	Index        int     `json:"index"`
	Delta        delta   `json:"delta"`
	FinishReason *string `json:"finish_reason,omitempty"`
	LogProbs     any     `json:"logprobs,omitempty"`
}

type delta struct {
	Role      string          `json:"role,omitempty"`
	Content   string          `json:"content,omitempty"`
	ToolCalls []deltaToolCall `json:"tool_calls,omitempty"`
}

type deltaToolCall struct {
	Index    int           `json:"index"`
	ID       string        `json:"id,omitempty"`
	Type     string        `json:"type,omitempty"`
	Function deltaFunction `json:"function,omitempty"`
}

type deltaFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func (p *Provider) readSSE(ctx context.Context, body io.ReadCloser, stream *provider.EventStream) {
	defer body.Close()
	defer stream.End("")

	scanner := bufio.NewScanner(body)
	// Increase buffer for large chunks
	scanner.Buffer(make([]byte, 0, 65536), 1024*1024)

	var contentBuilder strings.Builder

	for scanner.Scan() {
		line := scanner.Text()

		// Skip empty lines
		if line == "" {
			continue
		}

		// Check for "data: " prefix
		if !strings.HasPrefix(line, "data: ") {
			// Could be "event: ping" or similar, skip
			continue
		}

		data := strings.TrimPrefix(line, "data: ")

		// Check for the "data: [DONE]" signal
		if data == "[DONE]" {
			return
		}

		var chunk openaiStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			// Skip malformed chunks
			continue
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		choice0 := chunk.Choices[0]

		// Text delta
		if choice0.Delta.Content != "" {
			contentBuilder.WriteString(choice0.Delta.Content)
			stream.Push(provider.EventTextDelta{Delta: choice0.Delta.Content})
		}

		// Tool call start/delta
		for _, tc := range choice0.Delta.ToolCalls {
			if tc.ID != "" && tc.Function.Name != "" {
				// Tool call start
				stream.Push(provider.EventToolCallStart{
					ID:   tc.ID,
					Name: tc.Function.Name,
				})
			}
			if tc.Function.Arguments != "" {
				stream.Push(provider.EventToolCallDelta{
					ID:    tc.ID,
					Delta: tc.Function.Arguments,
				})
			}
		}

		// Finish reason
		if choice0.FinishReason != nil {
			stopReason := provider.StopStop
			switch *choice0.FinishReason {
			case "tool_calls":
				stopReason = provider.StopToolUse
			case "length":
				stopReason = provider.StopLength
			case "stop":
				stopReason = provider.StopStop
			}

			stream.Push(provider.EventDone{
				StopReason: stopReason,
				Content:    contentBuilder.String(),
			})
		}
	}

	if err := scanner.Err(); err != nil {
		if !isContextDone(ctx) {
			stream.Error(fmt.Errorf("sse read error: %w", err))
		}
	}
}

func isContextDone(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}
