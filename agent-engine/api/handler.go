package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/hycjack/agent-engine/engine"
	"github.com/hycjack/crux-ai/ai"
	core "github.com/hycjack/crux-ai/core"
)

// ─── Configuration ──────────────────────────────────────────────────────────

// HandlerConfig configures the OpenAI-compatible chat handler.
//
// There are two modes of operation:
//
//  1. Stateless mode (recommended): Set Model + SystemPrompt + Tools + SimpleStreamOptions.
//     Each request creates a fresh AgentLoop session.
//
//  2. Stateful mode: Provide an *engine.Agent that holds its own message history.
//     Each request appends to the agent's history and resumes.
type HandlerConfig struct {
	// Model is the crux-ai model to use (required in stateless mode).
	Model core.Model

	// SystemPrompt is the system prompt (optional).
	SystemPrompt string

	// Tools are the agent tools available to the model (optional).
	Tools []engine.AgentTool

	// SimpleStreamOptions carries API key, temperature, max_tokens defaults, etc.
	SimpleStreamOptions core.SimpleStreamOptions

	// Compaction configures automatic context-window compaction (optional).
	Compaction engine.CompactionConfig

	// Agent, if set, uses this stateful agent instead of creating new sessions.
	// When using an Agent, Model and Tools are typically set on the agent itself.
	Agent *engine.Agent

	// ResponseIDGenerator generates unique response IDs. If nil, a default
	// UUID-based generator is used.
	ResponseIDGenerator func() string

	// LogStreamingErrors, when true, logs SSE streaming errors to stderr.
	LogStreamingErrors bool

	// RequestTimeout is the maximum duration for a non-streaming request.
	// Default: 5 minutes. For streaming requests, this is per-chunk timeout.
	RequestTimeout time.Duration

	// EnableCORS, when true, adds CORS headers to all responses.
	EnableCORS bool
}

// NewChatHandler creates an HTTP handler for POST /v1/chat/completions.
func NewChatHandler(cfg HandlerConfig) http.Handler {
	h := &chatHandler{cfg: cfg}
	if h.cfg.ResponseIDGenerator == nil {
		h.cfg.ResponseIDGenerator = defaultResponseID
	}
	if h.cfg.RequestTimeout <= 0 {
		h.cfg.RequestTimeout = 5 * time.Minute
	}
	return h
}

// chatHandler implements the HTTP handler.
type chatHandler struct {
	cfg HandlerConfig
	mu  sync.Mutex // protects stateful agent access
}

func (h *chatHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Read body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	// Parse request
	var req ChatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return
	}

	// Validate
	if req.Model == "" && h.cfg.Model.ID == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	if len(req.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "messages is required")
		return
	}

	if req.Stream {
		h.handleStream(w, r, &req)
	} else {
		h.handleNonStream(w, r, &req)
	}
}

// ─── Non-streaming handler ─────────────────────────────────────────────────

func (h *chatHandler) handleNonStream(w http.ResponseWriter, r *http.Request, req *ChatRequest) {
	ctx, cancel := context.WithTimeout(r.Context(), h.cfg.RequestTimeout)
	defer cancel()

	resp, err := h.executeChat(ctx, req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if h.cfg.EnableCORS {
		setCORSHeaders(w)
	}
	json.NewEncoder(w).Encode(resp)
}

// ─── Streaming handler ─────────────────────────────────────────────────────

func (h *chatHandler) handleStream(w http.ResponseWriter, r *http.Request, req *ChatRequest) {
	ctx, cancel := context.WithTimeout(r.Context(), h.cfg.RequestTimeout)
	defer cancel()

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	if h.cfg.EnableCORS {
		setCORSHeaders(w)
	}

	// Determine model name for chunks
	modelName := req.Model
	if modelName == "" {
		modelName = h.cfg.Model.ID
	}
	responseID := h.cfg.ResponseIDGenerator()

	// Build event stream
	eventStream, err := h.createEventStream(ctx, req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	builder := NewStreamDeltaBuilder(modelName, responseID)

	// Process events
	_, err = eventStream.ForEach(ctx, func(evt engine.AgentEvent) error {
		// We only care about MessageUpdate events for SSE
		switch e := evt.(type) {
		case engine.EventMessageUpdate:
			chunk := builder.OnEvent(e.AssistantEvent)
			if chunk != nil {
				if err := writeSSEChunk(w, flusher, chunk); err != nil {
					return err
				}
			}
		case engine.EventMessageEnd:
			// Write final chunk with finish_reason
			chunk := builder.OnEvent(core.EventDone{
				Type:    "done",
				Reason:  e.Message.StopReason,
				Message: e.Message,
			})
			if chunk != nil {
				if err := writeSSEChunk(w, flusher, chunk); err != nil {
					return err
				}
			}
			// Write usage if available
			if e.Message.Usage.TotalTokens > 0 {
				usageChunk := &SSEChatChunk{
					ID:      responseID,
					Object:  "chat.completion.chunk",
					Created: builder.CreatedAt,
					Model:   modelName,
					Choices: []SSEChoice{},
					Usage: &UsageInfo{
						PromptTokens:     e.Message.Usage.Input,
						CompletionTokens: e.Message.Usage.Output,
						TotalTokens:      e.Message.Usage.TotalTokens,
					},
				}
				if err := writeSSEChunk(w, flusher, usageChunk); err != nil {
					return err
				}
			}
		}
		return nil
	})

	if err != nil && h.cfg.LogStreamingErrors {
		log.Printf("api: SSE streaming error: %v", err)
	}

	// Signal end of stream
	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// writeSSEChunk writes a single SSE data frame.
func writeSSEChunk(w io.Writer, flusher http.Flusher, chunk *SSEChatChunk) error {
	data, err := json.Marshal(chunk)
	if err != nil {
		return fmt.Errorf("marshal SSE chunk: %w", err)
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return fmt.Errorf("write SSE: %w", err)
	}
	flusher.Flush()
	return nil
}

// ─── Core execution ─────────────────────────────────────────────────────────

// executeChat runs the agent and returns an OpenAI-compatible response.
func (h *chatHandler) executeChat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	modelName := req.Model
	effModel := h.cfg.Model
	if modelName != "" && modelName != effModel.ID {
		// Try to resolve the model if different from default
		resolved, err := ai.GetModel(h.cfg.Model.Provider, modelName)
		if err == nil {
			effModel = resolved
		}
	}

	// Parse request
	llmCtx, opts, err := ParseChatRequest(req, effModel)
	if err != nil {
		return nil, fmt.Errorf("parse request: %w", err)
	}

	// Merge handler-level stream options
	if h.cfg.SimpleStreamOptions.APIKey != "" && opts.APIKey == "" {
		opts.APIKey = h.cfg.SimpleStreamOptions.APIKey
	}
	if opts.MaxTokens == nil && h.cfg.SimpleStreamOptions.MaxTokens != nil {
		opts.MaxTokens = h.cfg.SimpleStreamOptions.MaxTokens
	}
	if opts.Temperature == nil && h.cfg.SimpleStreamOptions.Temperature != nil {
		opts.Temperature = h.cfg.SimpleStreamOptions.Temperature
	}

	// Merge system prompt
	if h.cfg.SystemPrompt != "" {
		if llmCtx.SystemPrompt != "" {
			llmCtx.SystemPrompt = h.cfg.SystemPrompt + "\n\n" + llmCtx.SystemPrompt
		} else {
			llmCtx.SystemPrompt = h.cfg.SystemPrompt
		}
	}

	// Resolve tools
	tools := h.cfg.Tools

	if h.cfg.Agent != nil {
		return h.executeWithAgent(ctx, req, llmCtx, tools)
	}

	return h.executeStateless(ctx, effModel, llmCtx, opts, tools)
}

func (h *chatHandler) executeStateless(
	ctx context.Context,
	model core.Model,
	llmCtx core.Context,
	opts core.SimpleStreamOptions,
	tools []engine.AgentTool,
) (*ChatResponse, error) {
	// Convert engine tools to core tools
	coreTools := make([]core.Tool, len(tools))
	for i, t := range tools {
		coreTools[i] = core.Tool{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Parameters,
		}
	}
	llmCtx.Tools = coreTools

	// Build AgentLoopConfig for a single-turn agent
	config := engine.AgentLoopConfig{
		SimpleStreamOptions: opts,
		Model:               model,
		SystemPrompt:        llmCtx.SystemPrompt,
		Tools:               tools,
		Compaction:          h.cfg.Compaction,
	}

	// Run the agent loop
	stream := engine.AgentLoop(ctx, llmCtx.Messages, config)

	// Collect the final messages
	messages, err := stream.Result()
	if err != nil {
		return nil, fmt.Errorf("agent loop: %w", err)
	}

	// Extract the last assistant message
	lastAssistant := findLastAssistantMessage(messages)
	if lastAssistant == nil {
		return nil, fmt.Errorf("no assistant response")
	}

	resp := ToChatResponse(*lastAssistant, model.ID, h.cfg.ResponseIDGenerator())
	return &resp, nil
}

func (h *chatHandler) executeWithAgent(
	ctx context.Context,
	req *ChatRequest,
	llmCtx core.Context,
	tools []engine.AgentTool,
) (*ChatResponse, error) {
	h.mu.Lock()
	agent := h.cfg.Agent
	h.mu.Unlock()

	if agent == nil {
		return nil, fmt.Errorf("no agent configured")
	}

	// Run the agent with parsed messages
	messages, err := agent.Run(ctx, llmCtx.Messages...)
	if err != nil {
		return nil, fmt.Errorf("agent run: %w", err)
	}

	lastAssistant := findLastAssistantMessage(messages)
	if lastAssistant == nil {
		return nil, fmt.Errorf("no assistant response")
	}

	modelName := agent.State().Model.ID
	resp := ToChatResponse(*lastAssistant, modelName, h.cfg.ResponseIDGenerator())
	return &resp, nil
}

// createEventStream creates an AgentEventStream for SSE streaming.
func (h *chatHandler) createEventStream(ctx context.Context, req *ChatRequest) (*engine.AgentEventStream, error) {
	modelName := req.Model
	effModel := h.cfg.Model
	if modelName != "" && modelName != effModel.ID {
		resolved, err := ai.GetModel(h.cfg.Model.Provider, modelName)
		if err == nil {
			effModel = resolved
		}
	}

	llmCtx, opts, err := ParseChatRequest(req, effModel)
	if err != nil {
		return nil, fmt.Errorf("parse request: %w", err)
	}

	// Merge handler-level options
	if h.cfg.SimpleStreamOptions.APIKey != "" && opts.APIKey == "" {
		opts.APIKey = h.cfg.SimpleStreamOptions.APIKey
	}

	// Merge system prompt
	if h.cfg.SystemPrompt != "" {
		if llmCtx.SystemPrompt != "" {
			llmCtx.SystemPrompt = h.cfg.SystemPrompt + "\n\n" + llmCtx.SystemPrompt
		} else {
			llmCtx.SystemPrompt = h.cfg.SystemPrompt
		}
	}

	config := engine.AgentLoopConfig{
		SimpleStreamOptions: opts,
		Model:               effModel,
		SystemPrompt:        llmCtx.SystemPrompt,
		Tools:               h.cfg.Tools,
		Compaction:          h.cfg.Compaction,
	}

	return engine.AgentLoop(ctx, llmCtx.Messages, config), nil
}

// ─── Helpers ────────────────────────────────────────────────────────────────

func findLastAssistantMessage(msgs []core.Message) *core.AssistantMessage {
	for i := len(msgs) - 1; i >= 0; i-- {
		if am, ok := msgs[i].(core.AssistantMessage); ok {
			return &am
		}
	}
	return nil
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	if status == http.StatusMethodNotAllowed || status >= 500 {
		// Allow CORS for error responses too if needed
	}
	body, _ := json.Marshal(map[string]any{
		"error": map[string]any{
			"message": msg,
			"type":    "api_error",
		},
	})
	w.WriteHeader(status)
	w.Write(body)
}

func setCORSHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
}

var (
	muID        sync.Mutex
	idCounter   int64
)

// defaultResponseID generates a unique response ID.
func defaultResponseID() string {
	muID.Lock()
	idCounter++
	c := idCounter
	muID.Unlock()
	return fmt.Sprintf("chatcmpl-%d-%d", time.Now().UnixNano(), c)
}

