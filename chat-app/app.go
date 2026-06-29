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
	"crux-agent-runtime/autolearn"
	ctxpkg "crux-agent-runtime/context"
	"crux-agent-runtime/memory"
	"crux-agent-runtime/tools"

	"github.com/hycjack/crux-ai/ai"
	core "github.com/hycjack/crux-ai/core"

	// Register built-in LLM providers (OpenAI, Anthropic, Google, etc.).
	_ "github.com/hycjack/crux-ai/providers"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"chat-app/logutil"
	"chat-app/skillutil"
)

// App is the Wails application struct bound to the frontend.
type App struct {
	ctx context.Context

	mu         sync.RWMutex
	workingDir string
	cancelFn   context.CancelFunc
	agt        *agent.Agent

	// Cross-session long-term memory
	mem    *memory.Memory
	memDir string

	// Skill loader
	skillLoader *skillutil.Loader

	// Auto-learn
	learner          *autolearn.AutoLearner
	wfDir            string
	wfExtractor      *autolearn.WorkflowExtractor
	autoLearnEnabled bool
}

// NewApp creates a new App instance.
func NewApp() *App {
	// Initialize skill loader
	sl := skillutil.NewLoader()

	return &App{
		skillLoader: sl,
	}
}

// startup is called by Wails when the app is ready.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// Restore working directory from persisted settings FIRST so that
	// all subsequent init (skills, memory, autolearn) uses the correct
	// working directory from the start.
	var s PersistedSettings
	if err := loadJSON("settings.json", &s); err == nil && s.WorkingDir != "" {
		_ = a.SetWorkingDir(s.WorkingDir)
	}
	// If no persisted working dir, default to the executable's current dir.
	if a.GetWorkingDir() == "" {
		cwd, _ := os.Getwd()
		if cwd != "" {
			a.mu.Lock()
			a.workingDir = cwd
			a.mu.Unlock()
		}
	}
	// Load skills using the resolved working directory.
	_ = a.skillLoader.LoadAll(a.GetWorkingDir())

	// Initialize logging to <appDataDir>/logs/<YYYY-MM-DD>.log
	if appDir, err := appDataDir(); err == nil {
		if err := logutil.Init(appDir); err != nil {
			fmt.Fprintf(os.Stderr, "[logutil] init failed: %v\n", err)
		} else {
			logutil.Infof("Crux Agent Chat started")
		}
	}

	// Initialize long-term memory
	if appDir, err := appDataDir(); err == nil {
		a.memDir = appDir
		memPath := filepath.Join(appDir, "memory.json")
		mem, err := memory.New(memPath)
		if err != nil {
			logutil.Warnf("Failed to init memory: %v", err)
		} else {
			a.mem = mem
			logutil.Infof("Memory loaded (%d entries)", mem.Size())
		}
	}

	logutil.Infof("App startup complete")
}

// AppDataDir returns the OS-conventional directory for app data.
// Re-exposed as a Wails-bound method so the frontend can check it.
func (a *App) AppDataDir() (string, error) {
	return appDataDir()
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

	// Reload skills when working directory changes
	a.mu.RLock()
	sl := a.skillLoader
	a.mu.RUnlock()
	if sl != nil {
		_ = sl.Reload(abs)
		count := sl.Count()
		if count > 0 {
			logutil.Infof("Reloaded %d skills from %s", count, abs)
		}
	}

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

// -------------------- Skill management --------------------

// GetSkills returns the list of loaded skill names.
func (a *App) GetSkills() []string {
	a.mu.RLock()
	sl := a.skillLoader
	a.mu.RUnlock()
	if sl == nil {
		return nil
	}
	return sl.List()
}

// SkillInfo is the rich per-skill metadata returned to the frontend so
// the Settings panel can badge bundled vs user-authored entries.
type SkillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"` // "user" or "bundled"
}

// ListSkills returns the full skill catalog. Frontends that only need
// flat names should keep using GetSkills.
func (a *App) ListSkills() []SkillInfo {
	a.mu.RLock()
	sl := a.skillLoader
	a.mu.RUnlock()
	if sl == nil {
		return nil
	}
	all := sl.All()
	out := make([]SkillInfo, 0, len(all))
	for _, s := range all {
		out = append(out, SkillInfo{
			Name:        s.Name,
			Description: s.Description,
			Source:      s.Source,
		})
	}
	return out
}

// GetSkillContent returns the full content of a skill by name.
func (a *App) GetSkillContent(name string) string {
	a.mu.RLock()
	sl := a.skillLoader
	a.mu.RUnlock()
	if sl == nil {
		return ""
	}
	skill, ok := sl.Get(name)
	if !ok {
		return ""
	}
	return skill.Content
}

// ReloadSkills rescans the skills directory.
func (a *App) ReloadSkills(dir string) error {
	a.mu.RLock()
	sl := a.skillLoader
	a.mu.RUnlock()
	if sl == nil {
		return nil
	}
	return sl.Reload(dir)
}

// -------------------- Memory management --------------------

// GetMemories returns all long-term memory entries as key=value pairs.
func (a *App) GetMemories() map[string]string {
	a.mu.RLock()
	mem := a.mem
	a.mu.RUnlock()
	if mem == nil {
		return nil
	}
	out := make(map[string]string)
	for _, k := range mem.Keys() {
		if v, ok := mem.Get(k); ok {
			out[k] = v
		}
	}
	return out
}

// SetMemory stores a specific memory key-value pair.
func (a *App) SetMemory(key, value string) {
	a.mu.RLock()
	mem := a.mem
	a.mu.RUnlock()
	if mem != nil {
		mem.Set(key, value)
		_ = mem.Save()
		logutil.Infof("[memory] set %s=%s", key, value)
	}
}

// DeleteMemory deletes a specific memory key.
func (a *App) DeleteMemory(key string) {
	a.mu.RLock()
	mem := a.mem
	a.mu.RUnlock()
	if mem != nil {
		mem.Delete(key)
		_ = mem.Save()
		logutil.Infof("Memory deleted: %s", key)
	}
}

// ClearMemory clears all long-term memory.
func (a *App) ClearMemory() {
	a.mu.RLock()
	mem := a.mem
	a.mu.RUnlock()
	if mem != nil {
		for _, k := range mem.Keys() {
			mem.Delete(k)
		}
		_ = mem.Save()
		logutil.Infof("All memory cleared")
	}
}

// GetToolList returns the list of currently available agent tool names.
func (a *App) GetToolList() []string {
	a.mu.RLock()
	agt := a.agt
	a.mu.RUnlock()
	if agt == nil {
		return nil
	}
	tools := agt.State().Tools
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name
	}
	return names
}

// GetCompactionStatus returns a summary of the compaction config.
func (a *App) GetCompactionStatus() string {
	a.mu.RLock()
	agt := a.agt
	a.mu.RUnlock()
	if agt == nil {
		return "No agent (send a message first)"
	}
	c := agt.Compaction()
	return fmt.Sprintf("MaxTokens: %d, ReserveTokens: %d, OverflowRetries: %d, Compactor: %T",
		c.MaxTokens, c.ReserveTokens, c.OverflowRetries, c.Compactor)
}

// -------------------- Auto-learn management --------------------

// SetAutoLearnEnabled enables or disables LLM-based auto-learning.
// When disabled, only explicit markers ([remember:key=value]) are processed.
// When enabled, the agent will also attempt LLM extraction from natural
// language patterns.
func (a *App) SetAutoLearnEnabled(enabled bool) {
	a.mu.Lock()
	a.autoLearnEnabled = enabled
	a.mu.Unlock()
	logutil.Infof("[autolearn] auto-learn %v", map[bool]string{true: "enabled", false: "disabled"}[enabled])
}

// GetAutoLearnEnabled returns whether auto-learning is currently enabled.
func (a *App) GetAutoLearnEnabled() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.autoLearnEnabled
}

// -------------------- Model discovery --------------------

// ModelInfo is a minimal model descriptor for the frontend.
type ModelInfo struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Reasoning        bool              `json:"reasoning"`
	ThinkingLevelMap map[string]string `json:"thinkingLevelMap,omitempty"`
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
	case core.ProviderOllama:
		// Local Ollama: skip /models fetch (the server advertises tags
		// via /api/tags, not /v1/models) and let the caller fall through
		// to the static cached list. This keeps things working when the
		// daemon is offline.
		return a.cachedModels(provider), nil
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
		info := ModelInfo{ID: m.ID, Name: name}
		// Enrich with reasoning / thinking level map from static model data
		if cm, err := ai.GetModel(provider, m.ID); err == nil {
			info.Reasoning = cm.Reasoning
			if cm.ThinkingLevelMap != nil {
				info.ThinkingLevelMap = cm.ThinkingLevelMap
			}
		}
		out = append(out, info)
	}
	return out, nil
}

func defaultBaseURL(provider core.KnownProvider) string {
	switch provider {
	case core.ProviderAnthropic:
		return "https://api.anthropic.com/v1"
	case core.ProviderOllama:
		// Ollama exposes its OpenAI-compat surface at /v1 on port 11434.
		// Users running Ollama on a remote host or behind a proxy should
		// override this via the OLLAMA_BASE_URL env var or the UI's
		// Base URL field.
		return "http://localhost:11434/v1"
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
		info := ModelInfo{ID: m.ID, Name: m.ID}
		info.Reasoning = m.Reasoning
		if m.ThinkingLevelMap != nil {
			info.ThinkingLevelMap = m.ThinkingLevelMap
		}
		out = append(out, info)
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

// parseConversationHistory converts a JSON array of {role, content}
// objects (sent from the frontend) into core.Message instances that
// the agent can process. Assistant messages with empty content are
// skipped (they represent in-progress streaming placeholders).
func parseConversationHistory(jsonStr string) []core.Message {
	type toolCallData struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	}
	type histMsg struct {
		Role       string         `json:"role"`
		Content    string         `json:"content"`
		ToolCalls  []toolCallData `json:"toolCalls,omitempty"`
		ToolCallID string         `json:"toolCallId,omitempty"`
		ToolName   string         `json:"toolName,omitempty"`
		IsError    bool           `json:"isError,omitempty"`
	}
	var history []histMsg
	if err := json.Unmarshal([]byte(jsonStr), &history); err != nil {
		return nil
	}
	msgs := make([]core.Message, 0, len(history))
	for _, h := range history {
		switch h.Role {
		case "user":
			if h.Content == "" {
				continue
			}
			msgs = append(msgs, core.UserMessage{
				Role:      core.MessageRoleUser,
				Content:   h.Content,
				Timestamp: time.Now(),
			})
		case "assistant":
			var content []core.ContentBlock
			if h.Content != "" {
				content = append(content, core.TextContent{Type: "text", Text: h.Content})
			}
			for _, tc := range h.ToolCalls {
				content = append(content, core.ToolCall{
					Type:      "tool_use",
					ID:        tc.ID,
					Name:      tc.Name,
					Arguments: json.RawMessage(tc.Arguments),
				})
			}
			if len(content) == 0 {
				continue // skip empty placeholder
			}
			msgs = append(msgs, core.AssistantMessage{
				Role:    core.MessageRoleAssistant,
				Content: content,
			})
		case "tool":
			if h.Content == "" {
				continue
			}
			msgs = append(msgs, core.ToolResultMessage{
				Role:       core.MessageRoleTool,
				ToolCallID: h.ToolCallID,
				ToolName:   h.ToolName,
				Content:    []core.ContentBlock{core.TextContent{Type: "text", Text: h.Content}},
				IsError:    h.IsError,
			})
		}
	}
	return msgs
}

// -------------------- Chat --------------------

// ChatParams carries per-request configuration from the frontend.
type ChatParams struct {
	Message       string
	Provider      string
	APIKey        string
	BaseURL       string
	Model         string
	ThinkingLevel string
	Messages      string // JSON-encoded conversation history, optional
}

func parseChatParams(params map[string]interface{}) (ChatParams, error) {
	p := ChatParams{
		Message:       stringParam(params, "message"),
		Provider:      stringParam(params, "provider"),
		APIKey:        stringParam(params, "apiKey"),
		BaseURL:       stringParam(params, "baseUrl"),
		Model:         stringParam(params, "model"),
		ThinkingLevel: stringParam(params, "thinkingLevel"),
		Messages:      stringParam(params, "messages"),
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

	agt := a.getOrCreateAgent(model, cwd, p.APIKey, p.ThinkingLevel)

	// If conversation history is provided, reset and restore the full
	// conversation context so the LLM sees the complete message thread.
	if p.Messages != "" {
		agt.Reset()
		msgs := parseConversationHistory(p.Messages)
		if len(msgs) > 0 {
			agt.SetMessages(msgs)
		}
	}

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
			// or natural completion).
			wruntime.EventsEmit(a.ctx, "stream-done", "")
		}()

		// If we have valid memory and autolearn enabled, attempt to extract facts
		// from the user input before the agent processes it.
		a.mu.RLock()
		learner := a.learner
		autoLearn := a.autoLearnEnabled
		a.mu.RUnlock()
		if learner != nil && autoLearn {
			if n := learner.ProcessUserInput(p.Message); n > 0 {
				logutil.Infof("[autolearn] extracted %d memories from user input", n)
			}
		}

		_, _ = agt.Run(runCtx, core.UserMessage{
			Role:      core.MessageRoleUser,
			Content:   p.Message,
			Timestamp: time.Now(),
		})

		// Persist memory after each turn
		a.mu.RLock()
		mem := a.mem
		a.mu.RUnlock()
		if mem != nil {
			_ = mem.Save()
		}
	}()
	return nil
}

// ResetAgent clears the agent's message history. Call this when
// switching to a different conversation so old context does not
// leak into the new one.
func (a *App) ResetAgent() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.agt != nil {
		a.agt.Reset()
	}
}

// CancelStream cancels an in-progress StreamMessage call.
func (a *App) CancelStream() {
	a.mu.Lock()
	if a.cancelFn != nil {
		a.cancelFn()
	}
	a.mu.Unlock()
}

// resolveModel picks the model from the user-supplied list, or falls back
// to the first known model for the provider. The custom-model path lets
// users type any ID even if it isn't in the cached list.
func (a *App) resolveModel(p ChatParams) (core.Model, error) {
	provider := core.KnownProvider(p.Provider)
	switch provider {
	case core.ProviderAnthropic, core.ProviderOpenAI, core.ProviderOllama:
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

// buildSystemPromptWithMemory renders the system prompt including memory and skills.
func (a *App) buildSystemPrompt(cwd string) string {
	prompt := fmt.Sprintf(`You are Crux Agent, an AI coding assistant running inside the user's local workspace.

Working directory: %s

You have access to the following tools to inspect and modify files inside the working directory:
- read_file: read the contents of a file
- write_file: create or overwrite a file
- bash: run a shell command in the working directory (Windows: cmd.exe, Unix: sh)
- glob: list files matching a glob pattern
- grep: search for a regex across files`, cwd)

	// Append long-term memory
	a.mu.RLock()
	mem := a.mem
	a.mu.RUnlock()
	if mem != nil {
		if memContent := mem.FormatForPrompt(); memContent != "" {
			prompt += "\n\n" + memContent
		}
	}

	// Append available skills
	a.mu.RLock()
	sl := a.skillLoader
	a.mu.RUnlock()
	if sl != nil && sl.Count() > 0 {
		prompt += "\n\nAvailable skills (use skill_<name> tool to get instructions):\n"
		for _, name := range sl.List() {
			prompt += "- " + name + "\n"
		}
	}

	prompt += "\n\nWhen the user asks you to do something with files or code, prefer using the tools rather than guessing. After making changes, briefly summarize what you did."

	return prompt
}

// -------------------- Agent wiring --------------------

func (a *App) getOrCreateAgent(model core.Model, cwd, apiKey string, thinkingLevel ...string) *agent.Agent {
	if a.agt != nil {
		// Agent already exists — update its config for this turn.
		a.agt.SetModel(model)
		a.agt.SetTools(a.buildAllTools(cwd))
		a.agt.SetSystemPrompt(a.buildSystemPrompt(cwd))
		s := a.agt.State()
		s.SimpleStreamOptions.APIKey = apiKey
		if len(thinkingLevel) > 0 && thinkingLevel[0] != "" {
			s.SimpleStreamOptions.Reasoning = core.ThinkingLevel(thinkingLevel[0])
		}
		a.agt.SetSimpleStreamOptions(s.SimpleStreamOptions)
		return a.agt
	}

	toolsAll := a.buildAllTools(cwd)

	compaction := buildCompactionConfig(model, apiKey)

	agt := agent.New(agent.AgentOptions{
		InitialState: &agent.AgentState{
			Model:         model,
			SystemPrompt:  a.buildSystemPrompt(cwd),
			Tools:         toolsAll,
			ToolExecution: agent.ToolExecSequential,
			SimpleStreamOptions: core.SimpleStreamOptions{
				StreamOptions: core.StreamOptions{
					APIKey: apiKey,
				},
			},
			GetApiKey: func() string { return apiKey },
		},
		Compaction: compaction,
	})

	agt.Subscribe(func(evt agent.AgentEvent) {
		a.forwardAgentEvent(evt)
	})

	a.agt = agt

	// Initialize autolearn after agent is created
	a.initAutolearn(model, apiKey)

	return agt
}

// initAutolearn initializes the auto-learning system if memory is available.
func (a *App) initAutolearn(model core.Model, apiKey string) {
	a.mu.RLock()
	if a.learner != nil {
		a.mu.RUnlock()
		return // Already initialized
	}
	mem := a.mem
	a.mu.RUnlock()

	if mem == nil || apiKey == "" {
		return
	}

	// Create synchronous summarizer for autolearn
	signalSummarize := newSyncSummarizer(model, apiKey, 20*time.Second, "")
	wfSummarize := newSyncSummarizer(model, apiKey, 60*time.Second, "")

	wfDir := filepath.Join(a.workingDir, "skills", "auto-extracted")
	_ = os.MkdirAll(wfDir, 0755)

	learner := autolearn.New(mem, autolearn.DefaultSettings())
	learner.SetSignalExtractor(&autolearn.LLMSignalExtractor{SummarizeFunc: signalSummarize})
	learner.SetWorkflowDir(wfDir)

	wfExtractor := &autolearn.WorkflowExtractor{SummarizeFunc: wfSummarize}

	a.mu.Lock()
	a.learner = learner
	a.wfDir = wfDir
	a.wfExtractor = wfExtractor
	a.mu.Unlock()

	logutil.Infof("[autolearn] initialized with model %s", model.ID)
}

// buildAllTools builds the full tool set including built-in tools,
// skill tools, and memory tools.
func (a *App) buildAllTools(cwd string) []agent.AgentTool {
	var allTools []agent.AgentTool

	// Built-in tools (wrapped with working dir)
	builtins := tools.All()
	for _, t := range builtins {
		allTools = append(allTools, wrapWithWorkingDir(t, cwd))
	}

	// Skill tools
	a.mu.RLock()
	sl := a.skillLoader
	a.mu.RUnlock()
	if sl != nil && sl.Count() > 0 {
		skillTools := sl.AsAgentTools()
		allTools = append(allTools, skillTools...)
	}

	// Memory tool (if memory is available)
	a.mu.RLock()
	mem := a.mem
	a.mu.RUnlock()
	if mem != nil {
		allTools = append(allTools, a.rememberTool(mem))
		allTools = append(allTools, a.recallTool(mem))
	}

	return allTools
}

// rememberTool returns a tool that stores a key=value pair into long-term memory.
func (a *App) rememberTool(mem *memory.Memory) agent.AgentTool {
	return agent.AgentTool{
		Name:        "remember",
		Description: "Store a key-value pair into long-term memory. Use this when the user asks you to remember something or when you learn a fact about the user (their name, preferences, project details) that will be useful in future conversations.",
		Parameters:  mustRawMessage(`{"type":"object","properties":{"key":{"type":"string","description":"Memory key, e.g. user.name or project.tech_stack"},"value":{"type":"string","description":"Value to store"}},"required":["key","value"]}`),
		Execute: func(_ context.Context, _ string, params json.RawMessage, _ func(json.RawMessage)) (agent.AgentToolResult, error) {
			var args struct {
				Key   string `json:"key"`
				Value string `json:"value"`
			}
			if err := json.Unmarshal(params, &args); err != nil {
				return agent.AgentToolResult{
					Content: []core.ContentBlock{core.TextContent{Type: "text", Text: "Error: invalid arguments - " + err.Error()}},
					IsError: true,
				}, nil
			}
			if args.Key == "" {
				return agent.AgentToolResult{
					Content: []core.ContentBlock{core.TextContent{Type: "text", Text: "Error: key is required"}},
					IsError: true,
				}, nil
			}
			mem.Set(args.Key, args.Value)
			_ = mem.Save()
			logutil.Infof("[memory] set %s=%s", args.Key, args.Value)
			return agent.AgentToolResult{
				Content: []core.ContentBlock{core.TextContent{Type: "text", Text: fmt.Sprintf("Remembered: %s = %s", args.Key, args.Value)}},
			}, nil
		},
	}
}

// recallTool returns a tool that retrieves a value from long-term memory.
func (a *App) recallTool(mem *memory.Memory) agent.AgentTool {
	return agent.AgentTool{
		Name:        "recall",
		Description: "Retrieve a value from long-term memory by key. Use this when you need to recall information about the user or project that was previously stored.",
		Parameters:  mustRawMessage(`{"type":"object","properties":{"key":{"type":"string","description":"Memory key to look up, e.g. user.name or project.tech_stack"}},"required":["key"]}`),
		Execute: func(_ context.Context, _ string, params json.RawMessage, _ func(json.RawMessage)) (agent.AgentToolResult, error) {
			var args struct {
				Key string `json:"key"`
			}
			if err := json.Unmarshal(params, &args); err != nil {
				return agent.AgentToolResult{
					Content: []core.ContentBlock{core.TextContent{Type: "text", Text: "Error: invalid arguments - " + err.Error()}},
					IsError: true,
				}, nil
			}
			if args.Key == "" {
				return agent.AgentToolResult{
					Content: []core.ContentBlock{core.TextContent{Type: "text", Text: "Error: key is required"}},
					IsError: true,
				}, nil
			}
			if value, ok := mem.Get(args.Key); ok {
				return agent.AgentToolResult{
					Content: []core.ContentBlock{core.TextContent{Type: "text", Text: fmt.Sprintf("Memory: %s = %s", args.Key, value)}},
				}, nil
			}
			return agent.AgentToolResult{
				Content: []core.ContentBlock{core.TextContent{Type: "text", Text: fmt.Sprintf("No memory found for key: %s", args.Key)}},
			}, nil
		},
	}
}

// mustRawMessage returns a json.RawMessage for the given literal JSON.
func mustRawMessage(s string) json.RawMessage {
	if !json.Valid([]byte(s)) {
		panic(fmt.Sprintf("app: invalid JSON: %s", s))
	}
	return json.RawMessage(s)
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

// buildCompactionConfig wires up automatic context-window compaction.
// Strategy:
//  1. Pre-call: when estimated tokens exceed MaxTokens (60k), run chained
//     compactor: LLM summarize (lossy) → slide window (cheap).
//  2. Overflow retry: after a context-overflow error, force-compact and retry
//     up to OverflowRetries times.
func buildCompactionConfig(model core.Model, apiKey string) agent.CompactionConfig {
	summarizer := &ctxpkg.LLMSummarize{
		KeepLast:   8,
		MinTrigger: 20,
		Summarize:  buildSummarizeFunc(model, apiKey),
	}

	chained := &ctxpkg.ChainedCompactor{
		Compactors: []ctxpkg.Compactor{
			summarizer,
			ctxpkg.NewSlideWindow(40),
		},
	}

	return agent.CompactionConfig{
		Compactor:       chained,
		MaxTokens:       60000,
		ReserveTokens:   4096,
		OverflowRetries: 2,
		OnCompact: func(prevTokens, newTokens, prevMsgs, newMsgs int) {
			logutil.Debugf("[compaction] %d tokens, %d msgs → %d tokens, %d msgs",
				prevTokens, prevMsgs, newTokens, newMsgs)
		},
	}
}

// forwardAgentEvent converts agent events into Wails runtime events so
// the React frontend can render streaming output.
func (a *App) forwardAgentEvent(evt agent.AgentEvent) {
	switch e := evt.(type) {
	case agent.EventAgentStart:
		wruntime.EventsEmit(a.ctx, "stream-agent-start", "")
		logutil.Debugf("[agent] started")
	case agent.EventAgentEnd:
		wruntime.EventsEmit(a.ctx, "stream-done", "")
		logutil.Debugf("[agent] ended")
	case agent.EventTurnStart:
		logutil.Debugf("[agent] turn start")
	case agent.EventTurnEnd:
		logutil.Debugf("[agent] turn end")
	case agent.EventMessageUpdate:
		a.forwardAssistantEvent(e.AssistantEvent)
	case agent.EventToolExecStart:
		data, _ := json.Marshal(map[string]string{"id": e.ToolCallID, "name": e.ToolName})
		wruntime.EventsEmit(a.ctx, "stream-tool-exec-start", string(data))
		logutil.Debugf("[tool] executing %s (%s)", e.ToolName, string(e.ToolCallID))
	case agent.EventToolExecEnd:
		text := string(e.Result)
		event := "stream-tool-exec-end"
		if e.IsError {
			event = "stream-tool-exec-error"
			logutil.Warnf("[tool] %s error: %s", e.ToolName, text[:min(len(text), 500)])
		} else {
			logutil.Debugf("[tool] %s completed (%d bytes)", e.ToolName, len(text))
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
		logutil.Debugf("[toolcall] started %s (%s)", ae.Name, ae.ID)
	case core.EventToolCallDelta:
		wruntime.EventsEmit(a.ctx, "stream-tool-call-delta", ae.ArgumentsDelta)
	case core.EventToolCallEnd:
		wruntime.EventsEmit(a.ctx, "stream-tool-call-end", string(ae.Arguments))
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
