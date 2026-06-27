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

	mu          sync.RWMutex
	workingDir  string
	cancelFn    context.CancelFunc
	activeAgent *agent.Agent
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
	if info, err := os.Stat(abs); err != nil {
		return fmt.Errorf("working directory is not accessible: %w", err)
	} else if !info.IsDir() {
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

// GetModels lists models for a given provider, optionally using a custom base URL + API key.
func (a *App) GetModels(params map[string]interface{}) ([]ModelInfo, error) {
	providerStr, _ := params["provider"].(string)
	baseURL, _ := params["baseUrl"].(string)
	apiKey, _ := params["apiKey"].(string)

	provider := core.KnownProvider(providerStr)
	switch provider {
	case core.ProviderAnthropic:
		return a.fetchAnthropicModels(baseURL, apiKey)
	case core.ProviderOpenAI:
		return a.fetchOpenAIModels(baseURL, apiKey)
	}
	return nil, fmt.Errorf("unsupported provider: %s", providerStr)
}

func (a *App) fetchOpenAIModels(baseURL, apiKey string) ([]ModelInfo, error) {
	url := baseURL
	if url == "" {
		url = "https://api.openai.com/v1"
	}
	url += "/models"

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return a.cachedModels(core.ProviderOpenAI), nil
	}
	a.attachAuth(req, core.ProviderOpenAI, apiKey)

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return a.cachedModels(core.ProviderOpenAI), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return a.cachedModels(core.ProviderOpenAI), nil
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return a.cachedModels(core.ProviderOpenAI), nil
	}
	out := make([]ModelInfo, 0, len(result.Data))
	for _, m := range result.Data {
		out = append(out, ModelInfo{ID: m.ID, Name: m.ID})
	}
	return out, nil
}

func (a *App) fetchAnthropicModels(baseURL, apiKey string) ([]ModelInfo, error) {
	url := baseURL
	if url == "" {
		url = "https://api.anthropic.com/v1"
	}
	url += "/models"

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return a.cachedModels(core.ProviderAnthropic), nil
	}
	a.attachAuth(req, core.ProviderAnthropic, apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return a.cachedModels(core.ProviderAnthropic), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return a.cachedModels(core.ProviderAnthropic), nil
	}

	var result struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return a.cachedModels(core.ProviderAnthropic), nil
	}
	out := make([]ModelInfo, 0, len(result.Data))
	for _, m := range result.Data {
		name := m.Name
		if name == "" {
			name = m.ID
		}
		out = append(out, ModelInfo{ID: m.ID, Name: name})
	}
	return out, nil
}

func (a *App) attachAuth(req *http.Request, provider core.KnownProvider, apiKey string) {
	key := apiKey
	if key == "" {
		key = core.ResolveAPIKey(provider, "")
	}
	if key == "" {
		return
	}
	switch provider {
	case core.ProviderAnthropic:
		req.Header.Set("x-api-key", key)
	default:
		req.Header.Set("Authorization", "Bearer "+key)
	}
	req.Header.Set("Content-Type", "application/json")
}

func (a *App) cachedModels(provider core.KnownProvider) []ModelInfo {
	models := ai.GetModels(provider)
	out := make([]ModelInfo, 0, len(models))
	for _, m := range models {
		out = append(out, ModelInfo{ID: m.ID, Name: m.ID})
	}
	return out
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

// SendMessage runs a single (non-streaming) agent turn and returns the final assistant text.
func (a *App) SendMessage(params map[string]interface{}) (string, error) {
	p, err := parseChatParams(params)
	if err != nil {
		return "", err
	}

	a.mu.RLock()
	cwd := a.workingDir
	a.mu.RUnlock()

	model, err := a.resolveModel(p)
	if err != nil {
		return "", err
	}

	agt := a.newAgent(model, cwd, p.APIKey)
	wruntime.EventsEmit(a.ctx, "stream-text-start", "")

	go func() {
		_, _ = agt.Run(a.ctx, core.UserMessage{
			Role:      core.MessageRoleUser,
			Content:   p.Message,
			Timestamp: time.Now(),
		})
	}()

	return "", nil
}

// StreamMessage runs an agent turn and streams events to the frontend.
func (a *App) StreamMessage(params map[string]interface{}) error {
	p, err := parseChatParams(params)
	if err != nil {
		wruntime.EventsEmit(a.ctx, "stream-error", err.Error())
		return err
	}

	a.mu.RLock()
	cwd := a.workingDir
	a.mu.RUnlock()

	model, err := a.resolveModel(p)
	if err != nil {
		wruntime.EventsEmit(a.ctx, "stream-error", err.Error())
		return err
	}

	agt := a.newAgent(model, cwd, p.APIKey)
	a.mu.Lock()
	a.activeAgent = agt
	a.mu.Unlock()

	runCtx, cancel := context.WithCancel(a.ctx)
	a.mu.Lock()
	a.cancelFn = cancel
	a.mu.Unlock()

	go func() {
		defer func() {
			a.mu.Lock()
			a.cancelFn = nil
			a.activeAgent = nil
			a.mu.Unlock()
		}()
		_, _ = agt.Run(runCtx, core.UserMessage{
			Role:      core.MessageRoleUser,
			Content:   p.Message,
			Timestamp: time.Now(),
		})
	}()
	return nil
}

func parseChatParams(params map[string]interface{}) (ChatParams, error) {
	get := func(k string) string {
		if v, ok := params[k].(string); ok {
			return v
		}
		return ""
	}
	p := ChatParams{
		Message:  get("message"),
		Provider: get("provider"),
		APIKey:   get("apiKey"),
		BaseURL:  get("baseUrl"),
		Model:    get("model"),
	}
	if p.Message == "" {
		return p, fmt.Errorf("message is required")
	}
	return p, nil
}

func (a *App) resolveModel(p ChatParams) (core.Model, error) {
	provider := core.KnownProvider(p.Provider)
	switch provider {
	case core.ProviderAnthropic:
		// ok
	case core.ProviderOpenAI:
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
	return core.Model{}, fmt.Errorf("no model available for provider %s", provider)
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

// wrapWithWorkingDir rewrites file/shell tools so relative paths and shell commands run inside cwd.
func wrapWithWorkingDir(t agent.AgentTool, cwd string) agent.AgentTool {
	inner := t.Execute
	t.Execute = func(ctx context.Context, toolCallID string, params json.RawMessage, onUpdate func(json.RawMessage)) (agent.AgentToolResult, error) {
		if cwd == "" {
			return inner(ctx, toolCallID, params, onUpdate)
		}
		switch t.Name {
		case "bash":
			var args struct {
				Command string `json:"command"`
			}
			if err := json.Unmarshal(params, &args); err == nil && args.Command != "" {
				args.Command = injectCwd(args.Command, cwd)
				if newParams, err := json.Marshal(args); err == nil {
					params = newParams
				}
			}
		case "read_file", "write_file":
			var args struct {
				FilePath string `json:"filePath"`
			}
			if err := json.Unmarshal(params, &args); err == nil && args.FilePath != "" && !filepath.IsAbs(args.FilePath) {
				args.FilePath = filepath.Join(cwd, args.FilePath)
				if newParams, err := json.Marshal(args); err == nil {
					params = newParams
				}
			}
		case "glob", "grep":
			var args struct {
				Path string `json:"path"`
			}
			_ = json.Unmarshal(params, &args)
			if args.Path == "" {
				args.Path = cwd
				if newParams, err := json.Marshal(args); err == nil {
					params = newParams
				}
			} else if !filepath.IsAbs(args.Path) {
				args.Path = filepath.Join(cwd, args.Path)
				if newParams, err := json.Marshal(args); err == nil {
					params = newParams
				}
			}
		}
		return inner(ctx, toolCallID, params, onUpdate)
	}
	return t
}

// injectCwd prefixes the shell command with a "cd into cwd" step so relative paths resolve there.
// Uses the platform-native shell that crux-agent-runtime's bash tool will pick.
func injectCwd(cmd, cwd string) string {
	if stdruntime.GOOS == "windows" {
		// bash tool on Windows uses cmd.exe by default; safe across shells.
		return fmt.Sprintf("cd /d \"%s\" && %s", cwd, cmd)
	}
	return fmt.Sprintf("cd \"%s\" && %s", cwd, cmd)
}

// forwardAgentEvent converts agent events into Wails runtime events.
func (a *App) forwardAgentEvent(evt agent.AgentEvent) {
	switch e := evt.(type) {
	case agent.EventAgentStart:
		wruntime.EventsEmit(a.ctx, "stream-agent-start", "")
	case agent.EventAgentEnd:
		wruntime.EventsEmit(a.ctx, "stream-done", "")
	case agent.EventMessageUpdate:
		if ae, ok := e.AssistantEvent.(core.EventTextDelta); ok {
			wruntime.EventsEmit(a.ctx, "stream-text-delta", ae.Delta)
		}
		if ae, ok := e.AssistantEvent.(core.EventThinkingDelta); ok {
			wruntime.EventsEmit(a.ctx, "stream-thinking-delta", ae.Delta)
		}
		if ae, ok := e.AssistantEvent.(core.EventToolCallStart); ok {
			data, _ := json.Marshal(map[string]string{"id": ae.ID, "name": ae.Name})
			wruntime.EventsEmit(a.ctx, "stream-tool-call-start", string(data))
		}
		if ae, ok := e.AssistantEvent.(core.EventToolCallDelta); ok {
			wruntime.EventsEmit(a.ctx, "stream-tool-call-delta", ae.ArgumentsDelta)
		}
		if ae, ok := e.AssistantEvent.(core.EventToolCallEnd); ok {
			wruntime.EventsEmit(a.ctx, "stream-tool-call-end", string(ae.Arguments))
		}
	case agent.EventToolExecStart:
		data, _ := json.Marshal(map[string]string{"id": e.ToolCallID, "name": e.ToolName})
		wruntime.EventsEmit(a.ctx, "stream-tool-exec-start", string(data))
	case agent.EventToolExecEnd:
		text := string(e.Result)
		if e.IsError {
			wruntime.EventsEmit(a.ctx, "stream-tool-exec-error", text)
		} else {
			wruntime.EventsEmit(a.ctx, "stream-tool-exec-end", text)
		}
	}
}
