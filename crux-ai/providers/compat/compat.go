// Package compat provides a shared OpenAI-compatible chat completions
// provider used by the third-party providers (Xiaomi, GLM, DeepSeek, Kimi,
// Moonshot, etc.). It follows the pattern from oh-my-pi's
// `openai-completions-compat.ts`: a single streaming/SSE engine that all
// OpenAI-protocol providers reuse, with per-provider configuration.
//
// A Router holds a set of per-provider configs and is registered against a
// single KnownAPI (e.g. APIOpenAICompletions). At request time the router
// looks up the model.Provider to pick the right config.
package compat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	core "github.com/hycjack/crux-ai/core"
	"github.com/hycjack/crux-ai/internal/sse"
	"github.com/hycjack/crux-ai/providers/openai/convert"
)

// Config describes a single OpenAI-compatible provider.
type Config struct {
	// Provider identifier (e.g. core.ProviderDeepSeek).
	Provider core.KnownProvider
	// DefaultBaseURL is used when the model does not specify its own BaseURL.
	DefaultBaseURL string
	// Path appended to BaseURL, defaults to "/chat/completions".
	Path string
	// ExtraHeaders are added to every request.
	ExtraHeaders map[string]string
	// BuildBody, if set, customizes the JSON body. Use this for quirks like
	// DeepSeek's tool_choice suppression on reasoning models.
	BuildBody func(model core.Model, c core.Context, opts core.StreamOptions, body map[string]any) error
	// FinalizeResponse, if set, post-processes the assistant message before
	// the EventDone is emitted.
	FinalizeResponse func(model core.Model, body map[string]any, msg *core.AssistantMessage)
	// RequireAPIKey, when false, allows requests without an API key.
	// Local servers (Ollama, LM Studio) typically run unauthenticated; set
	// this to false for those. Defaults to true.
	RequireAPIKey *bool
}

// Router dispatches OpenAI-compatible requests to per-provider configs.
type Router struct {
	mu      sync.RWMutex
	configs map[core.KnownProvider]Config
}

// NewRouter creates an empty router. Use Register to add provider configs.
func NewRouter() *Router {
	return &Router{configs: make(map[core.KnownProvider]Config)}
}

// Register adds a per-provider config. If cfg.Path is empty it defaults to
// "/chat/completions". If cfg.RequireAPIKey is nil it defaults to true.
func (r *Router) Register(cfg Config) {
	if cfg.Path == "" {
		cfg.Path = "/chat/completions"
	}
	if cfg.RequireAPIKey == nil {
		required := true
		cfg.RequireAPIKey = &required
	}
	r.mu.Lock()
	r.configs[cfg.Provider] = cfg
	r.mu.Unlock()
}

// WithConfig is a chainable variant of Register.
func (r *Router) WithConfig(cfg Config) *Router {
	r.Register(cfg)
	return r
}

// Lookup returns the config for a given provider, and whether it was found.
func (r *Router) Lookup(p core.KnownProvider) (Config, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cfg, ok := r.configs[p]
	return cfg, ok
}

// Stream implements core.APIProvider.
func (r *Router) Stream(ctx context.Context, model core.Model, c core.Context, opts core.StreamOptions) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error) {
	cfg, ok := r.Lookup(model.Provider)
	if !ok {
		return nil, fmt.Errorf("compat: no config registered for provider %q", model.Provider)
	}
	return runStream(ctx, cfg, model, c, opts, buildBody(cfg, model, c, opts, ""))
}

// StreamSimple implements core.APIProvider.
func (r *Router) StreamSimple(ctx context.Context, model core.Model, c core.Context, opts core.SimpleStreamOptions) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error) {
	cfg, ok := r.Lookup(model.Provider)
	if !ok {
		return nil, fmt.Errorf("compat: no config registered for provider %q", model.Provider)
	}
	effort := string(opts.Reasoning)
	if opts.Reasoning == core.ThinkingXHigh {
		effort = string(core.ThinkingHigh)
	}
	return runStream(ctx, cfg, model, c, opts.StreamOptions, buildBody(cfg, model, c, opts.StreamOptions, effort))
}

func buildBody(_ Config, model core.Model, c core.Context, opts core.StreamOptions, reasoningEffort string) map[string]any {
	body := map[string]any{
		"model":  model.ID,
		"stream": true,
	}
	if opts.MaxTokens != nil && *opts.MaxTokens > 0 {
		body["max_tokens"] = *opts.MaxTokens
	} else if model.MaxTokens > 0 {
		body["max_tokens"] = model.MaxTokens
	}
	if opts.Temperature != nil {
		body["temperature"] = *opts.Temperature
	}
	if reasoningEffort != "" {
		body["reasoning_effort"] = reasoningEffort
	}

	messages := []map[string]any{}
	if c.SystemPrompt != "" {
		messages = append(messages, map[string]any{"role": "system", "content": c.SystemPrompt})
	}
	if converted, err := convert.Messages(c.Messages, model); err == nil {
		messages = append(messages, converted...)
	}
	body["messages"] = messages

	if len(c.Tools) > 0 {
		body["tools"] = convert.Tools(c.Tools)
	}
	if opts.PromptCacheKey != nil {
		body["prompt_cache_key"] = core.ClampOpenAIPromptCacheKey(opts.PromptCacheKey)
	}
	return body
}

func runStream(ctx context.Context, cfg Config, model core.Model, c core.Context, opts core.StreamOptions, body map[string]any) (*core.EventStream[core.AssistantMessageEvent, core.AssistantMessage], error) {
	apiKey := core.ResolveAPIKey(cfg.Provider, opts.APIKey)
	requireKey := cfg.RequireAPIKey == nil || *cfg.RequireAPIKey
	if apiKey == "" && requireKey {
		return nil, fmt.Errorf("%s: no API key provided", cfg.Provider)
	}
	if cfg.BuildBody != nil {
		if err := cfg.BuildBody(model, c, opts, body); err != nil {
			return nil, fmt.Errorf("%s: failed to build request: %w", cfg.Provider, err)
		}
	}
	if opts.OnPayload != nil {
		opts.OnPayload(body)
	}

	// Merge opts.Signal into the request context so cancellation propagates
	// to the HTTP round-trip. Without this the signal is silently ignored.
	// Source: pi-mono packages/ai/src/utils/abort-signals.ts pattern.
	merged := core.CombineAbortSignals(ctx, core.SignalToContext(opts.Signal))

	stream := core.NewEventStream[core.AssistantMessageEvent, core.AssistantMessage]()
	go func() {
		defer func() {
			if r := recover(); r != nil {
				stream.Error(fmt.Errorf("%s: panic: %v", cfg.Provider, r))
			}
		}()
		msg, err := doRequest(merged.Ctx, cfg, model, body, apiKey, stream, opts)
		if err != nil {
			stream.Error(err)
			return
		}
		stream.End(msg)
	}()
	return stream, nil
}

func doRequest(
	ctx context.Context,
	cfg Config,
	model core.Model,
	body map[string]any,
	apiKey string,
	stream *core.EventStream[core.AssistantMessageEvent, core.AssistantMessage],
	opts core.StreamOptions,
) (core.AssistantMessage, error) {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return core.AssistantMessage{}, err
	}
	baseURL := core.ResolveBaseURL(model, cfg.DefaultBaseURL)
	url := baseURL + cfg.Path

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return core.AssistantMessage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	for k, v := range cfg.ExtraHeaders {
		req.Header.Set(k, v)
	}
	for k, v := range core.ProviderHeadersToRecord(core.MergeProviderHeaders(model.Headers, opts.Headers)) {
		req.Header.Set(k, v)
	}

	resp, err := core.SSEClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			d := time.Duration(opts.TimeoutMs) * time.Millisecond
			if d <= 0 {
				d = 5 * time.Minute // matches SSEClient default
			}
			return core.AssistantMessage{}, core.WrapHTTPTimeout(model.Provider, d, err)
		}
		return core.AssistantMessage{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		if classified := core.ClassifyHTTPError(cfg.Provider, resp.StatusCode, string(errBody)); classified != nil {
			return core.AssistantMessage{}, classified
		}
		return core.AssistantMessage{}, fmt.Errorf("%s: API error %d: %s", cfg.Provider, resp.StatusCode, string(errBody))
	}
	return processSSE(ctx, cfg, resp.Body, stream, model, opts)
}

func processSSE(
	ctx context.Context,
	cfg Config,
	body io.Reader,
	stream *core.EventStream[core.AssistantMessageEvent, core.AssistantMessage],
	model core.Model,
	opts core.StreamOptions,
) (core.AssistantMessage, error) {
	var (
		msg         core.AssistantMessage
		textBuf     strings.Builder
		thinkBuf    strings.Builder
		toolCalls   = make(map[int]*core.ToolCall)
		toolIndices []int
	)
	msg.API = model.API
	msg.Provider = model.Provider
	msg.Model = model.ID
	msg.Role = "assistant"
	msg.Timestamp = time.Now()

	stream.Push(core.EventStart{
		Type:      "start",
		API:       model.API,
		Provider:  model.Provider,
		Model:     model.ID,
		Timestamp: time.Now(),
	})

	scanErr := sse.Scan(ctx, body, sse.ScanConfig{}, func(data string) error {
		if opts.OnResponse != nil {
			opts.OnResponse(data)
		}

		var chunk map[string]any
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return nil // skip malformed events
		}
		if id, ok := chunk["id"].(string); ok && id != "" {
			msg.ResponseID = id
		}

		// Parse usage from any chunk that carries it (top-level "usage").
		// Some providers (e.g. Xiaomi MiMo) send usage in a separate final
		// chunk with empty "choices", after the finish_reason chunk.
		if usage, ok := chunk["usage"].(map[string]any); ok && usage != nil {
			mergeUsage(&msg.Usage, usage)
		}

		choices, ok := chunk["choices"].([]any)
		if !ok || len(choices) == 0 {
			return nil
		}
		choice, ok := choices[0].(map[string]any)
		if !ok {
			return nil
		}
		if fr, ok := choice["finish_reason"].(string); ok && fr != "" {
			msg.StopReason = convert.StopReason(fr)
		}
		// Some providers (e.g. Kimi) put usage inside choices[0].usage.
		if choiceUsage, ok := choice["usage"].(map[string]any); ok && choiceUsage != nil {
			mergeUsage(&msg.Usage, choiceUsage)
		}
		delta, ok := choice["delta"].(map[string]any)
		if !ok {
			return nil
		}
		if content, ok := delta["content"].(string); ok && content != "" {
			textBuf.WriteString(content)
			stream.Push(core.EventTextDelta{Type: "text_delta", Delta: content})
		}
		if reasoning, ok := delta["reasoning_content"].(string); ok && reasoning != "" {
			thinkBuf.WriteString(reasoning)
			stream.Push(core.EventThinkingDelta{Type: "thinking_delta", Delta: reasoning})
		}
		if calls, ok := delta["tool_calls"].([]any); ok {
			for _, call := range calls {
				c, ok := call.(map[string]any)
				if !ok {
					continue
				}
				index := int(getFloat(c, "index"))
				function, _ := c["function"].(map[string]any)
				id, _ := c["id"].(string)
				name, _ := function["name"].(string)
				args, _ := function["arguments"].(string)

				if id != "" {
					toolCalls[index] = &core.ToolCall{Type: "toolCall", ID: id, Name: name}
					toolIndices = append(toolIndices, index)
					stream.Push(core.EventToolCallStart{Type: "toolcall_start", ID: id, Name: name})
				}
				if tc, ok := toolCalls[index]; ok && args != "" {
					tc.Arguments = append(tc.Arguments, []byte(args)...)
					stream.Push(core.EventToolCallDelta{Type: "toolcall_delta", ID: tc.ID, ArgumentsDelta: args})
				}
			}
		}
		return nil
	})
	if scanErr != nil {
		return core.AssistantMessage{}, scanErr
	}

	// Finalization — runs after scan completes or connection drops
	if thinkBuf.Len() > 0 {
		msg.Content = append(msg.Content, core.ThinkingContent{Type: "thinking", Thinking: thinkBuf.String()})
		stream.Push(core.EventThinkingEnd{Type: "thinking_end"})
	}
	if textBuf.Len() > 0 {
		msg.Content = append(msg.Content, core.TextContent{Type: "text", Text: textBuf.String()})
		stream.Push(core.EventTextEnd{Type: "text_end"})
	}
	// Deduplicate indices: some providers re-emit the same tool_call delta,
	// which would otherwise produce duplicate toolcall_end events.
	seen := make(map[int]struct{}, len(toolIndices))
	for _, index := range toolIndices {
		if _, ok := seen[index]; ok {
			continue
		}
		seen[index] = struct{}{}
		if tc, ok := toolCalls[index]; ok {
			stream.Push(core.EventToolCallEnd{Type: "toolcall_end", ID: tc.ID, Arguments: tc.Arguments})
			msg.Content = append(msg.Content, *tc)
		}
	}
	if msg.Usage.TotalTokens == 0 {
		msg.Usage.TotalTokens = msg.Usage.Input + msg.Usage.Output
	}
	msg.Usage.Cost = core.CalculateCost(model, msg.Usage)

	if cfg.FinalizeResponse != nil {
		cfg.FinalizeResponse(model, nil, &msg)
	}
	stream.Push(core.EventDone{Type: "done", Message: msg})
	return msg, nil
}

// mergeUsage merges token usage from a provider's JSON map into the
// core.Usage struct. It only overwrites fields that are currently zero,
// so the first non-empty usage source wins.
func mergeUsage(dst *core.Usage, src map[string]any) {
	if dst.Input == 0 {
		if v := getFloat(src, "prompt_tokens"); v > 0 {
			dst.Input = int(v)
		}
	}
	if dst.Output == 0 {
		if v := getFloat(src, "completion_tokens"); v > 0 {
			dst.Output = int(v)
		}
	}
	if dst.CacheRead == 0 {
		if v := getFloat(src, "prompt_tokens_details.cached_tokens"); v > 0 {
			dst.CacheRead = int(v)
		} else if v := getFloat(src, "cached_tokens"); v > 0 {
			dst.CacheRead = int(v)
		}
	}
	if dst.TotalTokens == 0 {
		if v := getFloat(src, "total_tokens"); v > 0 {
			dst.TotalTokens = int(v)
		}
	}
}

func getFloat(m map[string]any, key string) float64 {
	keys := strings.Split(key, ".")
	current := m
	for i, k := range keys {
		if i == len(keys)-1 {
			if v, ok := current[k]; ok {
				if f, ok := v.(float64); ok {
					return f
				}
			}
			return 0
		}
		next, ok := current[k].(map[string]any)
		if !ok {
			return 0
		}
		current = next
	}
	return 0
}
