// Package llm provides an OpenAI-compatible chat client with JSON-mode
// structured output. Used by l1 extractor, l2 scene summarizer, and l3
// persona generator.
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

// Client wraps an OpenAI-compatible chat completions API.
type Client struct {
	c       *openai.Client
	model   string
	BaseURL string

	// MockFn, when set, intercepts every ChatJSON call. Used by tests
	// and the offline demo. The function receives the system prompt and
	// user messages, and must return a JSON byte string that ChatJSON
	// will unmarshal into `out`.
	MockFn func(ctx context.Context, system string, messages []openai.ChatCompletionMessageParamUnion) ([]byte, error)
}

// NewClient constructs a client. apiKey may be empty if the provider doesn't
// require auth (e.g. local ollama). model is the default model for all calls.
func NewClient(baseURL, apiKey, model string) *Client {
	opts := []option.RequestOption{}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}
	c := openai.NewClient(opts...)
	return &Client{c: &c, model: model, BaseURL: baseURL, MockFn: nil}
}

// SetMockFn installs a mock function that intercepts every ChatJSON call.
func (cl *Client) SetMockFn(fn func(ctx context.Context, system string, messages []openai.ChatCompletionMessageParamUnion) ([]byte, error)) {
	cl.MockFn = fn
}

// Chat sends a chat completion request. messages must alternate user/assistant
// roles; system messages are merged into the system prompt.
func (cl *Client) Chat(ctx context.Context, system string, messages []openai.ChatCompletionMessageParamUnion, opts ...ChatOpt) (*openai.ChatCompletion, error) {
	o := options{}
	for _, opt := range opts {
		opt(&o)
	}
	model := cl.model
	if o.model != "" {
		model = o.model
	}

	params := openai.ChatCompletionNewParams{
		Model:    model,
		Messages: append([]openai.ChatCompletionMessageParamUnion{openai.SystemMessage(system)}, messages...),
	}
	if o.temperature != nil {
		params.Temperature = openai.Float(*o.temperature)
	}
	if o.maxTokens > 0 {
		params.MaxCompletionTokens = openai.Int(int64(o.maxTokens))
	}
	if o.jsonMode {
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONObject: &openai.ResponseFormatJSONObjectParam{},
		}
	}

	resp, err := cl.c.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("llm: chat: %w", err)
	}
	return resp, nil
}

// ChatJSON is a convenience: send a request, expect JSON in response, unmarshal
// into out. out must be a pointer.
func (cl *Client) ChatJSON(ctx context.Context, system string, messages []openai.ChatCompletionMessageParamUnion, out interface{}, opts ...ChatOpt) error {
	if cl.MockFn != nil {
		raw, err := cl.MockFn(ctx, system, messages)
		if err != nil {
			return fmt.Errorf("llm: mock: %w", err)
		}
		return json.Unmarshal(raw, out)
	}
	resp, err := cl.Chat(ctx, system, messages, append(opts, WithJSONMode())...)
	if err != nil {
		return err
	}
	if len(resp.Choices) == 0 {
		return fmt.Errorf("llm: empty choices")
	}
	content := resp.Choices[0].Message.Content
	if content == "" {
		return fmt.Errorf("llm: empty content")
	}
	if err := json.Unmarshal([]byte(content), out); err != nil {
		return fmt.Errorf("llm: unmarshal: %w\nraw: %s", err, truncate(content, 500))
	}
	return nil
}

// ChatOpt configures a single chat call.
type ChatOpt func(*options)

type options struct {
	model       string
	temperature *float64
	maxTokens   int
	jsonMode    bool
}

// WithModel overrides the model for this call.
func WithModel(m string) ChatOpt {
	return func(o *options) { o.model = m }
}

// WithTemperature sets the sampling temperature.
func WithTemperature(t float64) ChatOpt {
	return func(o *options) { o.temperature = &t }
}

// WithMaxTokens caps the response length.
func WithMaxTokens(n int) ChatOpt {
	return func(o *options) { o.maxTokens = n }
}

// WithJSONMode requests JSON object response format.
func WithJSONMode() ChatOpt {
	return func(o *options) { o.jsonMode = true }
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}