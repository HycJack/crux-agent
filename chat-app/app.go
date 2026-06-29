package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	stdruntime "runtime"
	"sync"
	"time"

	"crux-agent-runtime/agent"
	"crux-agent-runtime/tools"

	"github.com/hycjack/crux-ai/ai"
	core "github.com/hycjack/crux-ai/core"

	// Register built-in LLM providers (OpenAI, Anthropic, Google, etc.).
	_ "github.com/hycjack/crux-ai/providers"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App is the Wails application struct bound to the frontend.
type App struct {
	ctx context.Context

	mu         sync.RWMutex
	workingDir string
	cancelFn   context.CancelFunc
}

// NewApp creates a new App instance.
func NewApp() *App {
	cwd, _ := os.Getwd()
	return &App{
		workingDir: cwd,
	}
}

// startup is called by Wails when the app is ready.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// CancelStream cancels the current streaming request.
func (a *App) CancelStream() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancelFn != nil {
		a.cancelFn()
		a.cancelFn = nil
	}
}

// -------------------- Working directory --------------------

// SetWorkingDir updates the working directory used for file/shell tools.
func (a *App) SetWorkingDir(dir string) error {
	if dir == "" {
		return nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("working directory is not accessible: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("working directory is not a directory: %s", abs)
	}
	a.mu.Lock()
	a.workingDir = abs
	a.mu.Unlock()
	return nil
}

// GetWorkingDir returns the current working directory.
func (a *App) GetWorkingDir() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.workingDir
}

// PickWorkingDir opens a native directory picker and returns the selected path.
// Returns empty string if the user cancels.
func (a *App) PickWorkingDir() (string, error) {
	dir, err := wruntime.OpenDirectoryDialog(a.ctx, wruntime.OpenDialogOptions{
		Title:                "Choose working directory",
		CanCreateDirectories: true,
	})
	if err != nil {
		return "", err
	}
	if dir == "" {
		return "", nil
	}
	if err := a.SetWorkingDir(dir); err != nil {
		return "", err
	}
	return a.GetWorkingDir(), nil
}

// -------------------- Model discovery --------------------

// ModelInfo is a minimal model descriptor for the frontend.
type ModelInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// modelListing is the response shape shared by /v1/models endpoints.
// Both OpenAI and Anthropic use the same `{data: [{id, ...}]}` envelope.
type modelListing struct {
	Data []struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
	} `json:"data"`
}

// GetModels lists models for a given provider, optionally using a custom base URL + API key.
func (a *App) GetModels(params map[string]interface{}) ([]ModelInfo, error) {
	provider := core.KnownProvider(stringParam(params, "provider"))
	switch provider {
	case core.ProviderAnthropic:
		return a.fetchProviderModels(provider, stringParam(params, "baseUrl"), stringParam(params, "apiKey"))
	case core.ProviderOpenAI:
		return a.fetchProviderModels(provider, stringParam(params, "baseUrl"), stringParam(params, "apiKey"))
	}
	return nil, fmt.Errorf("unsupported provider: %s", provider)
}

// fetchProviderModels hits the provider's /models endpoint. The two
// providers share the same JSON shape, so they share this implementation
// — the only real differences are the default base URL and the auth
// header (handled by attachAuth).
func (a *App) fetchProviderModels(provider core.KnownProvider, baseURL, apiKey string) ([]ModelInfo, error) {
	url := baseURL
	if url == "" {
		url = defaultBaseURL(provider)
	}
	url += "/models"

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return a.cachedModels(provider), nil
	}
	a.attachAuth(req, provider, apiKey)
	if provider == core.ProviderAnthropic {
		req.Header.Set("anthropic-version", "2023-06-01")
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			_ = resp.Body.Close()
		}
		return a.cachedModels(provider), nil
	}
	defer resp.Body.Close()

	var listing modelListing
	if err := json.NewDecoder(resp.Body).Decode(&listing); err != nil {
		return a.cachedModels(provider), nil
	}

	out := make([]ModelInfo, 0, len(listing.Data))
	for _, m := range listing.Data {
		name := m.DisplayName
		if name == "" {
			name = m.ID
		}
		out = append(out, ModelInfo{ID: m.ID, Name: name})
	}
	return out, nil
}

func defaultBaseURL(provider core.KnownProvider) string {
	switch provider {
	case core.ProviderAnthropic:
		return "https://api.anthropic.com/v1"
	default:
		return "https://api.openai.com/v1"
	}
}

// attachAuth sets Authorization / x-api-key from the user-supplied key.
// It does NOT fall back to env vars — model listing should reflect
// exactly what the user typed. Chat sends its own key separately.
func (a *App) attachAuth(req *http.Request, provider core.KnownProvider, apiKey string) {
	if apiKey == "" {
		return
	}
	if provider == core.ProviderAnthropic {
		req.Header.Set("x-api-key", apiKey)
	} else {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("Content-Type", "application/json")
}

// cachedModels returns the statically-known model list for a provider.
// Used as the offline fallback when /v1/models fails.
func (a *App) cachedModels(provider core.KnownProvider) []ModelInfo {
	models := ai.GetModels(provider)
	out := make([]ModelInfo, 0, len(models))
	for _, m := range models {
		out = append(out, ModelInfo{ID: m.ID, Name: m.ID})
	}
	return out
}

// stringParam is a small helper for the untyped params map that Wails
// passes from the frontend. Returns "" for missing or wrong-typed keys.
func stringParam(params map[string]interface{}, key string) string {
	if v, ok := params[key].(string); ok {
		return v
	}
	return ""
}

// -------------------- Chat --------------------

// ChatParams carries per-request configuration from the frontend.
type ChatParams struct {
	Message  string
	Provider string
	APIKey   string
	BaseURL  string
	Model    string
}

func parseChatParams(params map[string]interface{}) (ChatParams, error) {
	p := ChatParams{
		Message:  stringParam(params, "message"),
		Provider: stringParam(params, "provider"),
		APIKey:   stringParam(params, "apiKey"),
		BaseURL:  stringParam(params, "baseUrl"),
		Model:    stringParam(params, "model"),
	}
	if p.Message == "" {
		return p, fmt.Errorf("message is required")
	}
	return p, nil
}

// StreamMessage runs an agent turn and streams events to the frontend.
func (a *App) StreamMessage(params map[string]interface{}) error {
	p, err := parseChatParams(params)
	if err != nil {
		wruntime.EventsEmit(a.ctx, "stream-error", err.Error())
		return err
	}

	cwd := a.GetWorkingDir()

	model, err := a.resolveModel(p)
	if err != nil {
		wruntime.EventsEmit(a.ctx, "stream-error", err.Error())
		return err
	}

	agt := a.newAgent(model, cwd, p.APIKey)
	runCtx, cancel := context.WithCancel(a.ctx)
	a.mu.Lock()
	a.cancelFn = cancel
	a.mu.Unlock()

	go func() {
		defer func() {
			a.mu.Lock()
			a.cancelFn = nil
			a.mu.Unlock()
			// Always emit stream-done when the agent run finishes, regardless
			// of how it ended (context cancellation via CancelStream, error,
			// or natural completion). Without this, the frontend isLoading
			// flag never gets reset after CancelStream because the agent
			// loop exits via stream.End() without pushing EventAgentEnd.
			wruntime.EventsEmit(a.ctx, "stream-done", "")
		}()
		_, _ = agt.Run(runCtx, core.UserMessage{
			Role:      core.MessageRoleUser,
			Content:   p.Message,
			Timestamp: time.Now(),
		})
	}()
	return nil
}

// resolveModel picks the model from the user-supplied list, or falls back
// to the first known model for the provider. The custom-model path lets
// users type any ID even if it isn't in the cached list.
func (a *App) resolveModel(p ChatParams) (core.Model, error) {
	provider := core.KnownProvider(p.Provider)
	switch provider {
	case core.ProviderAnthropic, core.ProviderOpenAI:
		// ok
	default:
		return core.Model{}, fmt.Errorf("unsupported provider: %s", p.Provider)
	}

	api := core.APIOpenAICompletions
	if provider == core.ProviderAnthropic {
		api = core.APIAnthropicMessages
	}

	if p.Model != "" {
		if m, err := ai.GetModel(provider, p.Model); err == nil {
			if p.BaseURL != "" {
				m.BaseURL = p.BaseURL
			}
			return m, nil
		}
		// Unknown ID: still allow it, with a conservative default window.
		return core.Model{
			ID:            p.Model,
			Provider:      provider,
			API:           api,
			BaseURL:       p.BaseURL,
			ContextWindow: 8192,
		}, nil
	}

	models := ai.GetModels(provider)
	if len(models) > 0 {
		m := models[0]
		if p.BaseURL != "" {
			m.BaseURL = p.BaseURL
		}
		return m, nil
	}
	return core.Model{}, fmt.Errorf("no model available for provider %s", p.Provider)
}

// -------------------- Agent wiring --------------------

const systemPromptTemplate = `You are Crux Agent, an AI coding assistant running inside the user's local workspace.

Working directory: %s

You have access to the following tools to inspect and modify files inside the working directory:
- read_file: read the contents of a file
- write_file: create or overwrite a file
- bash: run a shell command in the working directory (Windows: cmd.exe, Unix: sh)
- glob: list files matching a glob pattern
- grep: search for a regex across files

When the user asks you to do something with files or code, prefer using the tools rather than guessing. After making changes, briefly summarize what you did.`

func (a *App) newAgent(model core.Model, cwd, apiKey string) *agent.Agent {
	toolsAll := tools.All()
	wrapped := make([]agent.AgentTool, len(toolsAll))
	for i, t := range toolsAll {
		wrapped[i] = wrapWithWorkingDir(t, cwd)
	}

	agt := agent.New(agent.AgentOptions{
		InitialState: &agent.AgentState{
			Model:         model,
			SystemPrompt:  fmt.Sprintf(systemPromptTemplate, cwd),
			Tools:         wrapped,
			ToolExecution: agent.ToolExecSequential,
			SimpleStreamOptions: core.SimpleStreamOptions{
				StreamOptions: core.StreamOptions{
					APIKey: apiKey,
				},
			},
			GetApiKey: func() string { return apiKey },
		},
	})

	agt.Subscribe(func(evt agent.AgentEvent) {
		a.forwardAgentEvent(evt)
	})
	return agt
}

// wrapWithWorkingDir rewrites file/shell tools so relative paths and shell
// commands resolve inside cwd instead of the process's working dir.
func wrapWithWorkingDir(t agent.AgentTool, cwd string) agent.AgentTool {
	if cwd == "" {
		return t
	}
	inner := t.Execute
	t.Execute = func(ctx context.Context, toolCallID string, params json.RawMessage, onUpdate func(json.RawMessage)) (agent.AgentToolResult, error) {
		if rewritten, ok := rewriteToolParams(t.Name, params, cwd); ok {
			params = rewritten
		}
		return inner(ctx, toolCallID, params, onUpdate)
	}
	return t
}

// rewriteToolParams rewrites a tool's parameters so they target cwd.
// Returns (rewritten, true) on success, (nil, false) if the tool's args
// aren't recognized or no rewrite is needed.
func rewriteToolParams(name string, params json.RawMessage, cwd string) (json.RawMessage, bool) {
	switch name {
	case "bash":
		var args struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(params, &args); err != nil || args.Command == "" {
			return nil, false
		}
		args.Command = injectCwd(args.Command, cwd)
		return jsonMarshal(args)
	case "read_file", "write_file":
		var args struct {
			FilePath string `json:"filePath"`
		}
		if err := json.Unmarshal(params, &args); err != nil || args.FilePath == "" || filepath.IsAbs(args.FilePath) {
			return nil, false
		}
		args.FilePath = filepath.Join(cwd, args.FilePath)
		return jsonMarshal(args)
	case "glob", "grep":
		var args struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(params, &args); err != nil {
			return nil, false
		}
		if args.Path == "" {
			args.Path = cwd
		} else if !filepath.IsAbs(args.Path) {
			args.Path = filepath.Join(cwd, args.Path)
		} else {
			return nil, false
		}
		return jsonMarshal(args)
	}
	return nil, false
}

func jsonMarshal(v interface{}) (json.RawMessage, bool) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	return b, true
}

// injectCwd prefixes the shell command with a "cd into cwd" step so
// relative paths resolve there. Uses platform-native shell semantics.
func injectCwd(cmd, cwd string) string {
	if stdruntime.GOOS == "windows" {
		return fmt.Sprintf("cd /d \"%s\" & %s", cwd, cmd)
	}
	return fmt.Sprintf("cd \"%s\" && %s", cwd, cmd)
}

// forwardAgentEvent converts agent events into Wails runtime events so
// the React frontend can render streaming output.
func (a *App) forwardAgentEvent(evt agent.AgentEvent) {
	switch e := evt.(type) {
	case agent.EventAgentStart:
		wruntime.EventsEmit(a.ctx, "stream-agent-start", "")
	case agent.EventAgentEnd:
		wruntime.EventsEmit(a.ctx, "stream-done", "")
	case agent.EventMessageUpdate:
		a.forwardAssistantEvent(e.AssistantEvent)
	case agent.EventToolExecStart:
		data, _ := json.Marshal(map[string]string{"id": e.ToolCallID, "name": e.ToolName})
		wruntime.EventsEmit(a.ctx, "stream-tool-exec-start", string(data))
	case agent.EventToolExecEnd:
		text := string(e.Result)
		event := "stream-tool-exec-end"
		if e.IsError {
			event = "stream-tool-exec-error"
		}
		wruntime.EventsEmit(a.ctx, event, text)
	}
}

// forwardAssistantEvent emits the right Wails event for each assistant
// event variant (text / thinking delta, tool call lifecycle).
func (a *App) forwardAssistantEvent(evt core.AssistantMessageEvent) {
	switch ae := evt.(type) {
	case core.EventTextDelta:
		wruntime.EventsEmit(a.ctx, "stream-text-delta", ae.Delta)
	case core.EventThinkingDelta:
		wruntime.EventsEmit(a.ctx, "stream-thinking-delta", ae.Delta)
	case core.EventToolCallStart:
		data, _ := json.Marshal(map[string]string{"id": ae.ID, "name": ae.Name})
		wruntime.EventsEmit(a.ctx, "stream-tool-call-start", string(data))
	case core.EventToolCallDelta:
		wruntime.EventsEmit(a.ctx, "stream-tool-call-delta", ae.ArgumentsDelta)
	case core.EventToolCallEnd:
		wruntime.EventsEmit(a.ctx, "stream-tool-call-end", string(ae.Arguments))
	}
}
