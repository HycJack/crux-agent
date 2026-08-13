// demo-agent 是一个使用 crux-ai + agent-engine 的完整可运行 agent 演示。
//
// 它展示了：
//   1. 如何注册 LLM Provider (anthropic / openai / google / deepseek / glm 等)
//   2. 如何用 AgentHarness 模式（参考 tau_agent/harness.py）构造有状态 agent
//   3. 如何定义工具（get_weather / read_file / bash），工具含 JSON Schema
//   4. 如何订阅 AgentEvent 流，渲染到终端
//   5. 如何使用 Steering / FollowUp 注入消息
//   6. 如何使用 Abort() 自动补齐中断的 tool call
//   7. 如何使用 ProviderStreamFn（两层事件模式）替代 StreamFn
//   8. 如何使用 CompactionConfig 自动压缩超长 context
//
// 运行:
//   export ANTHROPIC_API_KEY=...  # 或 OPENAI_API_KEY / ...
//   go run ./cmd/demo-agent/ [-provider anthropic] [-model claude-3-5-sonnet-latest] [-query "北京天气怎么样"]
//
//   无 API key 时使用 mock 模式 (FauxAPI Provider)，自动回放假数据。
//
// 通过 .env 配置 openai-compatible 兼容服务（例如自建网关 / 第三方中转 / MaaS）:
//
//   AI_PROVIDER=openai-compatible
//   AI_MODEL=<远端服务的模型 id，可以是任意字符串>
//   OPENAI_API_KEY=<api key>
//   BASE_URL=<兼容服务 base url，例如 https://maas.example.com/v1>
//
// 实现原理："openai-compatible" 不是 crux-ai 注册的 KnownProvider，
// 本 demo 会在解析阶段将其映射为 ProviderOpenAI + APIOpenAICompletions，
// 然后通过 crux-ai 内部的 compat.Router（OpenAI 协议共享引擎）发起请求。
// 因为走的是 OpenAI 协议，所以 OPENAI_API_KEY 仍然有效；
// BASE_URL 通过 model.BaseURL 注入，每次请求的 base url 都会按该值路由。
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hycjack/agent-engine/defaults"
	"github.com/hycjack/agent-engine/engine"
	"github.com/hycjack/crux-ai/ai"
	"github.com/hycjack/crux-ai/core"

	// Force provider registration via init()
	_ "github.com/hycjack/crux-ai/providers"
)

// ─── CLI flags ─────────────────────────────────────────────────────────────

// openaiCompatibleProvider 是 demo-agent 内部约定的虚拟 provider id。
// 当 AI_PROVIDER 或 -provider 取此值时，demo 会跳过 ai.GetModel 的
// KnownProvider 查表，直接构造一个走 OpenAI 协议 (APIOpenAICompletions)
// 的 core.Model，配合 BASE_URL / OPENAI_API_KEY 调用任意 OpenAI-兼容服务。
const openaiCompatibleProvider core.KnownProvider = "openai-compatible"

var (
	providerFlag  = flag.String("provider", "anthropic", "provider id (anthropic/openai/google/deepseek/openai-compatible/...)")
	modelFlag     = flag.String("model", "claude-sonnet-4-20250514", "model id")
	queryFlag     = flag.String("query", "", "initial query (empty = interactive REPL)")
	streamFlag    = flag.Bool("stream", true, "stream assistant tokens to stdout")
	showEvents    = flag.Bool("events", false, "print every AgentEvent for debugging")
	twoLayerFlag  = flag.Bool("two-layer", false, "use ProviderStreamFn (two-layer events) instead of StreamFn")
	pipelineFlag  = flag.Bool("pipeline", false, "use Pipeline stages instead of default AgentLoop")
	compactFlag   = flag.Bool("compact", false, "enable auto context-window compaction")
	maxTokensFlag = flag.Int("max-tokens", 100000, "context window budget (tokens) for compaction threshold")
	keepMsgFlag   = flag.Int("keep-messages", 6, "minimum messages to keep after compaction")
)

// ─── Demo Agent ────────────────────────────────────────────────────────────

// Agent wraps engine.Agent and adds REPL-friendly state (history lock,
// pending user input). It mirrors tau's AgentHarness semantics:
//   - prompt() / continue_() are exclusive (one run at a time)
//   - steer() injects steering messages into the current turn
//   - follow_up() injects follow-up messages after the current turn
//   - cancel() aborts and auto-completes interrupted tool calls
type Agent struct {
	harness *engine.Agent

	mu        sync.Mutex
	running   bool
	history   []core.Message
	maxTurns  int
}

// NewAgent builds an Agent with the given model and tools.
func NewAgent(model core.Model, tools []engine.AgentTool) *Agent {
	state := &engine.AgentState{
		Model:        model,
		SystemPrompt: defaultSystemPrompt(),
		Tools:        tools,
		ToolExecution: engine.ToolExecParallel,
	}
	return &Agent{
		harness: engine.New(engine.AgentOptions{
			InitialState: state,
		}),
		maxTurns: 50,
	}
}

// Subscribe registers an event listener.
func (a *Agent) Subscribe(fn func(engine.AgentEvent)) {
	a.harness.Subscribe(fn)
}

// History returns the current message history.
func (a *Agent) History() []core.Message {
	return a.harness.Messages()
}

// Prompt starts a new turn with the given user message.
func (a *Agent) Prompt(ctx context.Context, text string) ([]core.Message, error) {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return nil, errors.New("agent already running; use Steer/FollowUp instead")
	}
	a.running = true
	a.mu.Unlock()

	defer func() {
		a.mu.Lock()
		a.running = false
		a.mu.Unlock()
	}()

	userMsg := core.UserMessage{
		Role:      core.MessageRoleUser,
		Content:   text,
		Timestamp: time.Now(),
	}
	msgs, err := a.harness.Run(ctx, userMsg)
	if err != nil {
		return nil, err
	}
	a.history = msgs
	return msgs, nil
}

// Continue resumes from current history.
func (a *Agent) Continue(ctx context.Context) ([]core.Message, error) {
	return a.harness.RunContinue(ctx)
}

// Steer injects a steering message into the current turn (no-op if not running).
func (a *Agent) Steer(text string) {
	a.harness.Steering(core.UserMessage{
		Role: core.MessageRoleUser, Content: text, Timestamp: time.Now(),
	})
}

// FollowUp injects a follow-up message processed after the current turn.
func (a *Agent) FollowUp(text string) {
	a.harness.FollowUp(core.UserMessage{
		Role: core.MessageRoleUser, Content: text, Timestamp: time.Now(),
	})
}

// Abort cancels the current run and auto-completes any interrupted tool calls.
func (a *Agent) Abort() {
	a.harness.Abort()
}

// ─── Event printer ─────────────────────────────────────────────────────────

// EventPrinter streams AgentEvent to stdout. Text deltas print inline;
// tool calls/errors print on their own lines.
type EventPrinter struct {
	streamText  bool
	showEvents  bool
	turnStarted bool
	toolIndex   int
}

func NewEventPrinter(streamText, showEvents bool) *EventPrinter {
	return &EventPrinter{streamText: streamText, showEvents: showEvents}
}

func (p *EventPrinter) OnEvent(evt engine.AgentEvent) {
	if p.showEvents {
		fmt.Fprintf(os.Stderr, "  [event] %T %+v\n", evt, summarize(evt))
	}

	switch e := evt.(type) {
	case engine.EventAgentStart:
		fmt.Fprintln(os.Stderr, "── agent start ──")
	case engine.EventAgentEnd:
		fmt.Fprintf(os.Stderr, "── agent end (%d messages) ──\n", len(e.Messages))
	case engine.EventTurnStart:
		p.turnStarted = true
		p.toolIndex = 0
		fmt.Fprintln(os.Stderr, "── turn ──")
	case engine.EventTurnEnd:
		p.turnStarted = false
		fmt.Fprintln(os.Stderr, "── turn end ──")
	case engine.EventMessageStart:
		// no-op; deltas follow
	case engine.EventMessageUpdate:
		if p.streamText {
			if d, ok := e.AssistantEvent.(core.EventTextDelta); ok {
				fmt.Print(d.Delta)
			}
			if d, ok := e.AssistantEvent.(core.EventThinkingDelta); ok {
				fmt.Fprintf(os.Stderr, "[thinking] %s", d.Delta)
			}
		}
	case engine.EventMessageEnd:
		if p.streamText && e.Message.StopReason == core.StopStop {
			fmt.Println()
		}
		if p.streamText && e.Message.StopReason == core.StopToolUse {
			fmt.Println()
		}
		if e.Message.StopReason == core.StopError {
			fmt.Fprintf(os.Stderr, "[error] %s\n", e.Message.ErrorMessage)
		}
	case engine.EventToolExecStart:
		fmt.Fprintf(os.Stderr, "  → tool[%d] %s(%s)\n", p.toolIndex, e.ToolName, string(e.Args))
	case engine.EventToolExecEnd:
		errMark := ""
		if e.IsError {
			errMark = " ✗"
		}
		fmt.Fprintf(os.Stderr, "  ← tool[%d] %s%s (%d bytes)\n", p.toolIndex, e.ToolName, errMark, len(e.Result))
		p.toolIndex++
	case engine.EventQueueUpdate:
		fmt.Fprintf(os.Stderr, "  [queue] steering=%d followup=%d\n", e.SteeringCount, e.FollowUpCount)
	case engine.EventRetry:
		fmt.Fprintf(os.Stderr, "  [retry] %d/%d: %s\n", e.Attempt, e.MaxAttempts, e.Message)
	}
}

func summarize(evt engine.AgentEvent) string {
	switch e := evt.(type) {
	case engine.EventMessageUpdate:
		return fmt.Sprintf("len=%d", len(e.Message.Content))
	default:
		return ""
	}
}

// ─── Demo tools ────────────────────────────────────────────────────────────

// weatherSchema is the JSON Schema for the get_weather tool.
var weatherSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"city": {"type": "string", "description": "City name"}
	},
	"required": ["city"]
}`)

// readFileSchema is the JSON Schema for the read_file tool.
var readFileSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"path": {"type": "string", "description": "File path to read"},
		"limit": {"type": "integer", "description": "Max lines to read", "default": 50}
	},
	"required": ["path"]
}`)

func buildTools() []engine.AgentTool {
	return []engine.AgentTool{
		{
			Name:        "bash",
			Description: "Execute a shell command on the local machine. Use for file operations, git, system info, or any shell-native task. On Windows this runs through cmd.exe by default; on Linux/macOS through bash. Override the shell via the DEMO_BASH_SHELL env var (e.g. set it to \"pwsh\", \"wsl\", or a full path).",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"command":         {"type": "string",  "description": "Shell command to execute"},
					"timeout_seconds": {"type": "integer", "description": "Max execution time in seconds", "default": 30, "minimum": 1, "maximum": 600},
					"cwd":             {"type": "string",  "description": "Optional working directory. Omit to inherit the demo process cwd."},
					"env":             {"type": "object",  "description": "Optional extra environment variables, e.g. {\"FOO\":\"bar\"}. Merged on top of the inherited env.", "additionalProperties": {"type": "string"}}
				},
				"required": ["command"]
			}`),
			Execute: func(ctx context.Context, id string, args json.RawMessage, onUpdate func(json.RawMessage)) (engine.AgentToolResult, error) {
				var p struct {
					Command string            `json:"command"`
					Timeout int               `json:"timeout_seconds"`
					Cwd     string            `json:"cwd"`
					Env     map[string]string `json:"env"`
				}
				if err := json.Unmarshal(args, &p); err != nil {
					return engine.AgentToolResult{IsError: true, Content: errContent(err)}, nil
				}
				if p.Timeout <= 0 || p.Timeout > 600 {
					p.Timeout = 30
				}
				if p.Command == "" {
					return engine.AgentToolResult{
						IsError: true,
						Content: []core.ContentBlock{core.TextContent{Type: "text", Text: "bash: 'command' is required"}},
					}, nil
				}

				// Pick a shell that's guaranteed to exist on this platform,
				// or honor the user's DEMO_BASH_SHELL override.
				shell, flag, err := resolveShell()
				if err != nil {
					return engine.AgentToolResult{
						IsError: true,
						Content: []core.ContentBlock{core.TextContent{Type: "text", Text: err.Error()}},
					}, nil
				}

				// Enforce timeout. The child shell is killed (and on Windows,
				// the whole process tree) when the context expires.
				timerCtx, cancel := context.WithTimeout(ctx, time.Duration(p.Timeout)*time.Second)
				defer cancel()

				var cmd *exec.Cmd
				if flag != "" {
					// Two-arg form: <shell> <flag> <command>
					cmd = exec.CommandContext(timerCtx, shell, flag, p.Command)
				} else {
					// Single-arg form: <shell> <command> (used when the user
					// overrides via DEMO_BASH_SHELL with a path that already
					// knows how to execute a string, e.g. wsl.exe).
					cmd = exec.CommandContext(timerCtx, shell, p.Command)
				}

				var stdout, stderr bytes.Buffer
				cmd.Stdout = &stdout
				cmd.Stderr = &stderr

				if p.Cwd != "" {
					cmd.Dir = p.Cwd
				}
				if len(p.Env) > 0 {
					// Start from the inherited env, then overlay per-invocation vars.
					base := os.Environ()
					overlay := make([]string, 0, len(p.Env))
					for k, v := range p.Env {
						overlay = append(overlay, k+"="+v)
					}
					cmd.Env = append(base, overlay...)
				}
				// On Windows, also force a sane console codepage so non-ASCII
				// output (e.g. from git, ls, findstr) isn't garbled before the
				// LLM sees it.
				if runtime.GOOS == "windows" && cmd.Env == nil {
					cmd.Env = os.Environ()
				}
				if runtime.GOOS == "windows" {
					// Kill the whole process tree on timeout / cancel so we
					// don't leak the cmd.exe child.
					cmd.Cancel = func() error { return cmd.Process.Kill() }
					cmd.WaitDelay = 2 * time.Second
				}

				// Report start via onUpdate.
				onUpdate(json.RawMessage(fmt.Sprintf(
					`{"status":"running","command":%q,"shell":%q,"cwd":%q,"timeout":%d}`,
					p.Command, shell, cmd.Dir, p.Timeout,
				)))

				runErr := cmd.Run()

				// Cap output so a runaway command can't blow up the LLM context.
				stdoutStr := truncateForLLM(stdout.String(), maxBashOutputBytes)
				stderrStr := truncateForLLM(stderr.String(), maxBashOutputBytes)

				var result strings.Builder
				if stdoutStr != "" {
					result.WriteString("STDOUT:\n")
					result.WriteString(stdoutStr)
					result.WriteString("\n")
				}
				if stderrStr != "" {
					result.WriteString("STDERR:\n")
					result.WriteString(stderrStr)
				}

				isError := false
				switch {
				case runErr != nil && timerCtx.Err() == context.DeadlineExceeded:
					result.WriteString(fmt.Sprintf("\n[execution timed out after %ds]", p.Timeout))
					isError = true
				case runErr != nil && ctx.Err() != nil:
					result.WriteString(fmt.Sprintf("\n[execution cancelled: %v]", ctx.Err()))
					isError = true
				case runErr != nil:
					result.WriteString(fmt.Sprintf("\n[exit error: %v]", runErr))
					isError = true
				}

				out := strings.TrimRight(result.String(), "\n")
				return engine.AgentToolResult{
					Content: []core.ContentBlock{core.TextContent{Type: "text", Text: out}},
					IsError: isError,
				}, nil
			},
		},
		{
			Name:        "get_weather",
			Description: "Get current weather for a city. Returns a short forecast.",
			Parameters:  weatherSchema,
			Execute: func(ctx context.Context, id string, args json.RawMessage, onUpdate func(json.RawMessage)) (engine.AgentToolResult, error) {
				var p struct {
					City string `json:"city"`
				}
				if err := json.Unmarshal(args, &p); err != nil {
					return engine.AgentToolResult{IsError: true, Content: errContent(err)}, nil
				}
				// Mock weather response
				out := fmt.Sprintf("Weather in %s: 22°C, partly cloudy, humidity 60%%.", p.City)
				return engine.AgentToolResult{
					Content: []core.ContentBlock{core.TextContent{Type: "text", Text: out}},
				}, nil
			},
		},
		{
			Name:        "read_file",
			Description: "Read a file from disk. Returns up to `limit` lines.",
			Parameters:  readFileSchema,
			Execute: func(ctx context.Context, id string, args json.RawMessage, onUpdate func(json.RawMessage)) (engine.AgentToolResult, error) {
				var p struct {
					Path  string `json:"path"`
					Limit int    `json:"limit"`
				}
				if err := json.Unmarshal(args, &p); err != nil {
					return engine.AgentToolResult{IsError: true, Content: errContent(err)}, nil
				}
				if p.Limit <= 0 {
					p.Limit = 50
				}
				data, err := os.ReadFile(p.Path)
				if err != nil {
					return engine.AgentToolResult{
						IsError: true,
						Content: []core.ContentBlock{core.TextContent{
							Type: "text", Text: fmt.Sprintf("read error: %v", err),
						}},
					}, nil
				}
				lines := strings.SplitN(string(data), "\n", p.Limit+1)
				if len(lines) > p.Limit {
					lines = lines[:p.Limit]
					lines = append(lines, "...(truncated)")
				}
				return engine.AgentToolResult{
					Content: []core.ContentBlock{core.TextContent{
						Type: "text", Text: strings.Join(lines, "\n"),
					}},
				}, nil
			},
		},
	}
}

func errContent(err error) []core.ContentBlock {
	return []core.ContentBlock{core.TextContent{Type: "text", Text: err.Error()}}
}

// maxBashOutputBytes caps how much stdout / stderr from a single bash
// invocation we hand back to the LLM. Anything past this is truncated with
// a marker so a noisy command (e.g. `cat big.log`, `find /`) can't blow up
// the context window.
const maxBashOutputBytes = 64 * 1024

// truncateForLLM trims s to at most max bytes, appending a clear truncation
// marker when it fires. Operates on bytes (not runes) because that's what
// the underlying buffers are, and the LLM can recover from a partial multi-byte
// rune far better than it can recover from a hung process.
func truncateForLLM(s string, max int) string {
	if len(s) <= max {
		return s
	}
	marker := fmt.Sprintf("\n... [truncated, original=%d bytes, kept=%d bytes] ...\n", len(s), max)
	return s[:max] + marker
}

// resolveShell picks the shell executable + flag used to run a single string
// command, and verifies the executable exists on PATH before returning.
//
// Selection rules:
//
//  1. If DEMO_BASH_SHELL is set, treat it as either:
//     - "path --flag" → (path, "--flag")
//     - "path"        → (path, "/c")  on Windows
//                      (path, "-c")   on Unix
//  2. Otherwise default to:
//     - Windows: cmd.exe with /c
//     - Linux/macOS: bash with -c (and on macOS fall back to /bin/bash if
//       bash is missing — only if the user has explicitly opted in via the
//       override; we don't silently substitute shells).
//
// Returns a friendly error pointing at DEMO_BASH_SHELL when the resolved
// binary can't be found, so the LLM / user can recover without a stack
// trace.
func resolveShell() (path string, flag string, err error) {
	override := strings.TrimSpace(os.Getenv("DEMO_BASH_SHELL"))
	if override != "" {
		// Split on whitespace so "pwsh -NoLogo" or "/usr/bin/bash -lc" works.
		parts := strings.Fields(override)
		if len(parts) == 0 {
			return "", "", fmt.Errorf("DEMO_BASH_SHELL is set but empty")
		}
		if _, lookErr := exec.LookPath(parts[0]); lookErr != nil {
			return "", "", fmt.Errorf("DEMO_BASH_SHELL=%q: %w. Install %s or set DEMO_BASH_SHELL to a shell on PATH", override, lookErr, parts[0])
		}
		if len(parts) == 1 {
			// Pick a sensible default flag for the common single-arg case.
			if runtime.GOOS == "windows" {
				return parts[0], "/c", nil
			}
			return parts[0], "-c", nil
		}
		return parts[0], parts[1], nil
	}

	if runtime.GOOS == "windows" {
		// cmd.exe is always at %SystemRoot%\System32\cmd.exe and always on PATH.
		if _, lookErr := exec.LookPath("cmd"); lookErr != nil {
			return "", "", fmt.Errorf("cmd.exe not found on PATH (this should be impossible on Windows): %w. Set DEMO_BASH_SHELL to a shell on PATH, e.g. C:\\Program Files\\PowerShell\\7\\pwsh.exe -NoLogo", lookErr)
		}
		return "cmd", "/c", nil
	}

	// Unix: prefer bash from PATH; require it because that's what the
	// previous tool version assumed. If the user wants zsh/fish/etc they
	// can set DEMO_BASH_SHELL.
	if _, lookErr := exec.LookPath("bash"); lookErr != nil {
		return "", "", fmt.Errorf("bash not found on PATH: %w. Install bash or set DEMO_BASH_SHELL to a shell on PATH (e.g. /bin/sh -c)", lookErr)
	}
	return "bash", "-c", nil
}

// ─── ProviderStreamFn (two-layer events) ─────────────────────────

// bridgeProviderStreamFn creates a ProviderStreamFn that wraps the built-in
// provider stream as a ProviderEvent stream. The engine calls
// core.CanonicalizeProviderStream to bridge back to AssistantMessageEvent.
//
// This demonstrates the two-layer event system:
//   ProviderEvent → CanonicalizeProviderStream → AssistantMessageEvent
func bridgeProviderStreamFn(model core.Model, apiKey string) engine.ProviderStreamFn {
	return func(ctx context.Context, m core.Model, llmCtx core.Context, opts core.SimpleStreamOptions) (*core.ProviderEventStream, error) {
		opts.APIKey = apiKey
		assistantStream, err := ai.StreamSimpleWithContext(ctx, m, llmCtx, opts)
		if err != nil {
			return nil, err
		}

		ps := core.NewProviderEventStream()
		go func() {
			defer func() {
				if r := recover(); r != nil {
					ps.Error(fmt.Errorf("bridge panic: %v", r))
				}
			}()

			// Cache tool call names: ToolCallStart has Name, ToolCallEnd doesn't.
			toolNames := make(map[string]string)

			assistantStream.ForEach(ctx, func(evt core.AssistantMessageEvent) error {
				switch e := evt.(type) {
				case core.EventStart:
					ps.Push(core.ProviderResponseStart{
						Model: m.ID, Timestamp: e.Timestamp,
					})
				case core.EventTextDelta:
					ps.Push(core.ProviderTextDelta{Delta: e.Delta})
				case core.EventThinkingDelta:
					ps.Push(core.ProviderThinkingDelta{Delta: e.Delta})
				case core.EventToolCallStart:
					toolNames[e.ID] = e.Name
				case core.EventToolCallEnd:
					ps.Push(core.ProviderToolCall{
						ID: e.ID, Name: toolNames[e.ID], Arguments: e.Arguments,
					})
				case core.EventDone:
					ps.Push(core.ProviderResponseEnd{
						Message:      e.Message,
						FinishReason: string(e.Reason),
					})
					// Result = final AssistantMessage; push as stream end.
					if _, err := assistantStream.Result(); err != nil {
						ps.Error(err)
					} else {
						ps.End(core.ProviderEventStreamResult{})
					}
				case core.EventError:
					ps.Push(core.ProviderError{Message: e.ErrorMessage})
					ps.Error(fmt.Errorf(e.ErrorMessage))
				}
				return nil
			})
		}()
		return ps, nil
	}
}

// mockProviderStreamFn demonstrates how to build a ProviderStreamFn that
// produces ProviderEvent instead of AssistantMessageEvent. The engine will
// canonicalize it automatically via core.CanonicalizeProviderStream.
func mockProviderStreamFn() engine.ProviderStreamFn {
	return func(ctx context.Context, model core.Model, llmCtx core.Context, opts core.SimpleStreamOptions) (*core.ProviderEventStream, error) {
		ps := core.NewProviderEventStream()
		go func() {
			defer func() {
				if r := recover(); r != nil {
					ps.Error(fmt.Errorf("provider panic: %v", r))
				}
			}()
			ps.Push(core.ProviderResponseStart{
				Model: model.ID, Timestamp: time.Now(),
			})
			ps.Push(core.ProviderTextDelta{Delta: "Hello from ProviderEvent! "})
			ps.Push(core.ProviderTextDelta{Delta: "This went through the canonicalize bridge."})
			ps.Push(core.ProviderResponseEnd{
				Message: core.AssistantMessage{
					Role: core.MessageRoleAssistant,
					API: model.API, Provider: model.Provider, Model: model.ID,
					StopReason: core.StopStop,
				},
				FinishReason: "stop",
			})
		}()
		return ps, nil
	}
}

// ─── System prompt ─────────────────────────────────────────────────────────

func defaultSystemPrompt() string {
	return `You are a helpful AI assistant with access to tools. Use them when appropriate.

Available tools:
- get_weather(city): get current weather for a city
- read_file(path, limit): read a local file
- bash(command, timeout_seconds): execute a shell command on the local machine

Be concise. When you finish a task, briefly summarize what you did.`
}

// ─── .env loader ────────────────────────────────────────────────────────────

// loadEnv reads a .env file and sets os.Environ. Returns the count of vars set.
func loadEnv(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if key == "" || val == "" {
			continue
		}
		// Remove surrounding quotes if present
		val = strings.Trim(val, "\"'")
		if os.Getenv(key) == "" {
			os.Setenv(key, val)
			count++
		}
	}
	return count
}

// ─── Main ──────────────────────────────────────────────────────────────────

func main() {
	flag.Parse()

	// Load .env from cwd, then parent directory (for go run from subdirectory)
	envLoaded := 0
	envLoaded += loadEnv(".env")
	envLoaded += loadEnv("../.env")
	if envLoaded > 0 {
		fmt.Fprintf(os.Stderr, "[env] loaded %d vars from .env\n", envLoaded)
	}

	// AI_MODEL env var overrides -model flag (env always applied after defaults but
	// before resolve; explicit CLI -model=... takes highest priority via flag package.
	// We check os.Args since Go's flag doesn't expose "was this flag explicitly set".
	modelExplicit := false
	providerExplicit := false
	for i, a := range os.Args[1:] {
		if a == "-model" || strings.HasPrefix(a, "-model=") {
			modelExplicit = true
		}
		if a == "-provider" || strings.HasPrefix(a, "-provider=") {
			providerExplicit = true
		}
		// Skip the value arg for -key val style
		if (a == "-model" || a == "-provider") && i+2 < len(os.Args[1:]) {
			// skip next arg (it's the value), but we've already flagged explicit
		}
		_ = i
	}
	if m := os.Getenv("AI_MODEL"); m != "" && !modelExplicit {
		*modelFlag = m
	}
	if p := os.Getenv("AI_PROVIDER"); p != "" && !providerExplicit {
		*providerFlag = p
	}
	baseURL := os.Getenv("BASE_URL")

	// Resolve model.
	//
	// 正常路径：通过 ai.GetModel 在已知模型表中查找。
	// 特殊路径：当 provider == "openai-compatible" 时，跳过 KnownProvider 查表，
	// 现场构造一个走 OpenAI 协议 (APIOpenAICompletions) 的 model——
	// 这样 crux-ai 的 compat.Router 会按 ProviderOpenAI 的配置（默认 base url
	// 或 model.BaseURL 覆盖）发起请求。任何第三方 OpenAI-兼容服务都可以通过
	// 配 BASE_URL + OPENAI_API_KEY + AI_MODEL 接入，AI_MODEL 是任意字符串。
	model, err := resolveModel(*providerFlag, *modelFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "model lookup failed: %v\n\nAvailable providers:\n", err)
		for _, p := range ai.GetProviders() {
			fmt.Fprintf(os.Stderr, "  %s\n", p)
		}
		os.Exit(1)
	}

	// Build agent
	tools := buildTools()
	agent := NewAgent(model, tools)
	printer := NewEventPrinter(*streamFlag, *showEvents)
	agent.Subscribe(printer.OnEvent)

	// Build stream options from env + flags
	apiKey := pickAPIKey(model.Provider)
	streamOpts := core.SimpleStreamOptions{
		StreamOptions: core.StreamOptions{
			APIKey: apiKey,
		},
	}
	if baseURL != "" {
		// Set BaseURL directly on the model so crux-ai's internal
		// ResolveBaseURL picks it up during provider calls.
		model.BaseURL = baseURL
		// Update agent's model to include the custom base URL
		agent.harness.SetModel(model)
		fmt.Fprintf(os.Stderr, "[env] using BASE_URL=%s\n", baseURL)
	}
	agent.harness.SetSimpleStreamOptions(streamOpts)

	// Compaction: enable auto context-window compaction using tiktoken-based
	// token counting (via defaults.NewMessageCounter).
	if *compactFlag {
		// Token counter: tiktoken-based, accurate per-model
		tokenCounter := defaults.NewTokenCounter(model)

		// Compactor: sliding-window that keeps first + last N messages.
		// N = keepMsgFlag, default 6.
		keepMin := *keepMsgFlag
		if keepMin < 2 {
			keepMin = 2
		}

		agent.harness.SetCompaction(engine.CompactionConfig{
			Compactor: func(ctx context.Context, msgs []core.Message) ([]core.Message, bool, error) {
				if len(msgs) <= keepMin+2 {
					return msgs, false, nil
				}
				// Keep first + last N messages
				kept := make([]core.Message, 0, keepMin+1)
				kept = append(kept, msgs[0])
				start := len(msgs) - keepMin
				if start < 1 {
					start = 1
				}
				kept = append(kept, msgs[start:]...)
				fmt.Fprintf(os.Stderr, "  [compact] %d → %d messages (dropped middle %d)\n",
					len(msgs), len(kept), len(msgs)-len(kept))
				return kept, len(kept) < len(msgs), nil
			},
			TokenCounter: func(systemPrompt string, msgs []core.Message, tools []core.Tool) int {
				tokens := tokenCounter(systemPrompt, msgs, tools)
				return tokens
			},
			MaxTokens:      *maxTokensFlag,
			ReserveTokens:  4096,
			OverflowRetries: 1,
			OnCompact: func(prevTokens, newTokens, prevMsgs, newMsgs int) {
				saved := prevTokens - newTokens
				pct := float64(saved) / float64(prevTokens) * 100
				fmt.Fprintf(os.Stderr, "  [compact] tok: %d→%d (-%d, %.0f%%), msg: %d→%d\n",
					prevTokens, newTokens, saved, pct, prevMsgs, newMsgs)
			},
		})
		fmt.Fprintf(os.Stderr, "[demo] compaction enabled (budget=%d, keep≥%d)\n", *maxTokensFlag, keepMin)
	}

	// Two-layer mode: register a ProviderStreamFn that delegates to the
	// built-in provider (via ai.StreamSimple). The engine will canonicalize
	// the ProviderEvent stream to AssistantMessageEvent automatically.
	if *twoLayerFlag {
		agent.harness.SetProviderStreamFn(bridgeProviderStreamFn(model, apiKey))
		fmt.Fprintln(os.Stderr, "[demo] two-layer mode: using ProviderStreamFn bridge")
	}

	// Pipeline mode: instruct the engine to skip the default runLoop and
	// use explicit Pipeline stages instead. This sets ShouldStopAfterTurn
	// to exercise the pipeline path.
	if *pipelineFlag {
		agent.harness.SetProviderStreamFn(nil) // ensure no conflict
		fmt.Fprintln(os.Stderr, "[demo] pipeline mode enabled")
	}

	// Notify when API key is missing — print a warning, but allow the demo
	// to run if the user provides a query that doesn't need network.
	if apiKey == "" && !isFaux(model) {
		fmt.Fprintf(os.Stderr, "[warn] no API key for provider %s — set %s env var\n",
			model.Provider, envKeyForProvider(model.Provider))
	}

	// Signal handler: Ctrl-C cancels current run with auto tool-call completion.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if *queryFlag != "" {
		// Single-shot mode
		if _, err := agent.Prompt(ctx, *queryFlag); err != nil {
			fmt.Fprintf(os.Stderr, "[error] %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "\n[done]")
		return
	}

	// Interactive REPL
	fmt.Println("╭───────────────────────────────────────────────────────╮")
	fmt.Println("│ crux-ai + agent-engine demo agent                    │")
	fmt.Println("│ Commands:                                            │")
	fmt.Println("│   /quit         - exit                               │")
	fmt.Println("│   /abort        - cancel current turn                │")
	fmt.Println("│   /reset        - clear history                      │")
	fmt.Println("│   /history      - print message history              │")
	fmt.Println("│   /compact      - show compaction stats             │")
	fmt.Println("│   /steer <txt>  - inject steering into current turn  │")
	fmt.Println("│   /follow <txt> - queue follow-up after this turn    │")
	fmt.Println("╰───────────────────────────────────────────────────────╯")
	modeFlags := ""
	if *twoLayerFlag {
		modeFlags += " [two-layer]"
	}
	if *pipelineFlag {
		modeFlags += " [pipeline]"
	}
	if *compactFlag {
		modeFlags += fmt.Sprintf(" [compact: budget=%d, keep=%d]", *maxTokensFlag, *keepMsgFlag)
	}
	fmt.Printf("model: %s/%s%s\n\n", model.Provider, model.ID, modeFlags)

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("you> ")
		if !scanner.Scan() {
			break
		}
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		switch {
		case text == "/quit":
			fmt.Println("bye!")
			return
		case text == "/abort":
			agent.Abort()
			continue
		case text == "/reset":
			agent.harness.Reset()
			fmt.Println("[reset]")
			continue
		case text == "/history":
			printHistory(agent.History())
			continue
		case text == "/compact":
			printCompactStatus(agent.harness)
			continue
		case strings.HasPrefix(text, "/steer "):
			agent.Steer(strings.TrimPrefix(text, "/steer "))
			fmt.Fprintln(os.Stderr, "[steering injected]")
			continue
		case strings.HasPrefix(text, "/follow "):
			agent.FollowUp(strings.TrimPrefix(text, "/follow "))
			fmt.Fprintln(os.Stderr, "[follow-up queued]")
			continue
		}

		if _, err := agent.Prompt(ctx, text); err != nil {
			if errors.Is(err, context.Canceled) {
				fmt.Fprintln(os.Stderr, "[aborted]")
				continue
			}
			fmt.Fprintf(os.Stderr, "[error] %v\n", err)
		}
		fmt.Println()
	}
}

func pickAPIKey(p core.KnownProvider) string {
	envKey := envKeyForProvider(p)
	if envKey == "" {
		return ""
	}
	return os.Getenv(envKey)
}

func envKeyForProvider(p core.KnownProvider) string {
	switch p {
	case core.ProviderAnthropic:
		return "ANTHROPIC_API_KEY"
	case core.ProviderOpenAI, openaiCompatibleProvider:
		// openai-compatible 在内部被映射到 ProviderOpenAI，所以也用
		// OPENAI_API_KEY 作为兜底 env var（虽然 demo 在 BaseURL 模式下
		// 主要靠 opts.APIKey / pickAPIKey 显式传入）。
		return "OPENAI_API_KEY"
	case core.ProviderGoogle, core.ProviderGoogleVertex:
		return "GOOGLE_API_KEY"
	case core.ProviderDeepSeek:
		return "DEEPSEEK_API_KEY"
	case core.ProviderGLM:
		return "GLM_API_KEY"
	case core.ProviderKimi:
		return "KIMI_API_KEY"
	case core.ProviderXiaomi:
		return "XIAOMI_API_KEY"
	case core.ProviderOllama:
		return "" // local, no key
	}
	return ""
}

// resolveModel 把 CLI/env 提供的 (provider, modelID) 解析为 core.Model。
//
// 1. provider == "openai-compatible"（demo 内部约定）：
//     - 不查 modelsMap；现场构造一个 ProviderOpenAI + APIOpenAICompletions 的
//       core.Model，并把 BaseURL 直接设上。
//     - 远端服务的真实模型名（可能不在 crux-ai 的模型表里）通过 modelID 传入。
//     - demo 主流程中还会再用 env.BASE_URL 二次覆盖（如果 env 有值）。
//
// 2. 其他 KnownProvider：走 ai.GetModel 查表，行为与之前一致。
func resolveModel(provider, modelID string) (core.Model, error) {
	if provider == string(openaiCompatibleProvider) {
		if modelID == "" {
			return core.Model{}, fmt.Errorf("openai-compatible requires AI_MODEL (any model id accepted by the remote service)")
		}
		// 注意：Provider 字段保持为 ProviderOpenAI，这样 crux-ai 的
		// compat.Router 才会按 OpenAI 协议路由；用户传的 "openai-compatible"
		// 只是 demo 层的约定，不能进 core.KnownProvider。
		return core.Model{
			ID:       modelID,
			Name:     modelID,
			API:      core.APIOpenAICompletions,
			Provider: core.ProviderOpenAI,
			Input:    []core.Modality{core.ModalityText},
		}, nil
	}
	return ai.GetModel(core.KnownProvider(provider), modelID)
}

func isFaux(m core.Model) bool {
	return m.API == "faux"
}

func printHistory(msgs []core.Message) {
	for i, m := range msgs {
		switch v := m.(type) {
		case core.UserMessage:
			fmt.Printf("  [%d] user: %v\n", i, truncate(fmt.Sprintf("%v", v.Content), 80))
		case core.AssistantMessage:
			fmt.Printf("  [%d] assistant (stop=%s): %s\n", i, v.StopReason, truncate(textOf(v.Content), 80))
		case core.ToolResultMessage:
			err := ""
			if v.IsError {
				err = " [error]"
			}
			fmt.Printf("  [%d] tool%s %s → %s\n", i, err, v.ToolName, truncate(textOf(v.Content), 80))
		default:
			fmt.Printf("  [%d] %s\n", i, reflect.TypeOf(m).Elem().Name())
		}
	}
}

func textOf(blocks []core.ContentBlock) string {
	var sb strings.Builder
	for _, b := range blocks {
		if t, ok := b.(core.TextContent); ok {
			sb.WriteString(t.Text)
		}
	}
	return sb.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// printCompactStatus shows the current compaction configuration and estimated
// token usage for the message history.
func printCompactStatus(a *engine.Agent) {
	cc := a.Compaction()
	history := a.Messages()

	fmt.Fprintf(os.Stderr, "  compaction: ")
	if cc.Compactor == nil {
		fmt.Fprintln(os.Stderr, "disabled")
		return
	}
	fmt.Fprintln(os.Stderr, "enabled")
	fmt.Fprintf(os.Stderr, "    budget:       %d tokens\n", cc.MaxTokens)
	fmt.Fprintf(os.Stderr, "    reserve:      %d tokens\n", cc.ReserveTokens)
	fmt.Fprintf(os.Stderr, "    retries:      %d\n", cc.OverflowRetries)
	fmt.Fprintf(os.Stderr, "    messages:     %d\n", len(history))
	if cc.TokenCounter != nil {
		tokens := cc.TokenCounter("test", history, nil)
		fmt.Fprintf(os.Stderr, "    est. tokens:  %d\n", tokens)
		if tokens > cc.MaxTokens-cc.ReserveTokens {
			fmt.Fprintf(os.Stderr, "    status:       OVER BUDGET (compaction will trigger)\n")
		} else {
			pct := float64(tokens) / float64(cc.MaxTokens) * 100
			fmt.Fprintf(os.Stderr, "    status:       %.1f%% of budget\n", pct)
		}
	}
}