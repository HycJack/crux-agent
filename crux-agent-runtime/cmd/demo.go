// Demo: shows how to use crux-agent-runtime with crux-ai providers.
//
// This file owns program lifecycle (signal handling, env loading, model
// selection) and wires every package together. Everything else lives in
// sibling files in this directory:
//
//	llm.go        — synchronous LLM helpers (newSyncSummarizer, buildSummarizeFunc)
//	tools.go      — demo-specific tools (echo, calculator, get_time, remember)
//	compaction.go — context-window compaction config
//	session.go    — session lifecycle (newSession, messageToEntry)
//	setup.go      — env loader, model selector, system-prompt builder
//	repl.go       — interactive prompt loop and event printer
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/hycjack/crux-ai/core"

	// Register all built-in providers
	_ "github.com/hycjack/crux-ai/providers"

	"crux-agent-runtime/agent"
	"crux-agent-runtime/autolearn"
	"crux-agent-runtime/memory"
	"crux-agent-runtime/session"
	"crux-agent-runtime/tools"
)

// demoState groups the long-lived dependencies the REPL needs to mutate
// (session path, current session, agent). Keeping them in a struct lets
// REPL commands operate on a single value instead of long argument lists.
type demoState struct {
	model        core.Model
	mem          *memory.Memory
	memPath      string
	a            *agent.Agent
	sess         *session.Session
	sessPath     string
	wfExtractor  *autolearn.WorkflowExtractor
	wfDir        string
	systemPrompt string
	learner      *autolearn.AutoLearner
}

func main() {
	// Root context is plain; signal handling for Ctrl+C happens inside the
	// REPL via the raw-mode terminal reader (see repl.go). This lets us
	// distinguish "cancel current turn" (Ctrl+C while agent is running)
	// from "exit program" (Ctrl+C at the idle prompt).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	loadEnv(".env")

	model, err := getTestModel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "No API key found: %v\n", err)
		fmt.Fprintf(os.Stderr, "Set one of: ANTHROPIC_API_KEY, OPENAI_API_KEY, GOOGLE_API_KEY\n")
		os.Exit(1)
	}

	memPath := envOr("MEMORY_PATH", "./agent-demo-memory.json")
	mem, err := memory.New(memPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to init memory: %v\n", err)
		os.Exit(1)
	}
	defer mem.Save()

	systemPrompt := buildSystemPrompt(mem)

	// Auto-learn: signal extractor + workflow extractor share the same
	// synchronous LLM helper but with different prompts / timeouts.
	extAPIKey := core.GetEnvAPIKey(model.Provider)
	signalSummarize := newSyncSummarizer(model, extAPIKey, 20*time.Second, "" /* inline prompt */)
	wfSummarize := newSyncSummarizer(model, extAPIKey, 60*time.Second, "")

	wfDir := envOr("WORKFLOW_DIR", "./skills/auto-extracted")
	if err := os.MkdirAll(wfDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "[workflow] failed to create dir %s: %v\n", wfDir, err)
	}

	learner := autolearn.New(mem, autolearn.DefaultSettings())
	learner.SetSignalExtractor(&autolearn.LLMSignalExtractor{SummarizeFunc: signalSummarize})

	wfExtractor := &autolearn.WorkflowExtractor{SummarizeFunc: wfSummarize}
	learner.SetWorkflowDir(wfDir)

	// Demo-specific tools layered on top of the built-in tools package.
	allTools := append(tools.All(),
		echoTool(),
		calculatorTool(),
		getTimeTool(),
		rememberTool(mem),
	)

	// Session storage (JSONL).
	sessPath := "./sessions/demo.jsonl"
	if err := os.MkdirAll("./sessions", 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create sessions dir: %v\n", err)
		os.Exit(1)
	}
	store, err := session.NewJSONLStorage(sessPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to init session storage: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	sess, err := session.NewSession(store)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to init session: %v\n", err)
		os.Exit(1)
	}
	if sess.ID() == "" {
		_ = sess.SetID(fmt.Sprintf("demo-%d", time.Now().UnixNano()))
	}

	// Agent: restored history + system prompt + compaction config.
	a := agent.New(agent.AgentOptions{
		InitialState: &agent.AgentState{
			Model:    model,
			Tools:    allTools,
			Messages: session.BuildSessionContext(sess.Entries()).Messages,
		},
		Compaction: buildCompactionConfig(model, extAPIKey),
	})
	a.SetSystemPrompt(systemPrompt)
	a.Subscribe(newEventPrinter())

	state := &demoState{
		model:        model,
		mem:          mem,
		memPath:      memPath,
		a:            a,
		sess:         sess,
		sessPath:     sessPath,
		wfExtractor:  wfExtractor,
		wfDir:        wfDir,
		systemPrompt: systemPrompt,
		learner:      learner,
	}

	fmt.Println("=== crux-agent-runtime demo ===")
	fmt.Printf("Model: %s (%s)\n", model.ID, model.Provider)
	fmt.Printf("Session: %s\n", sess.ID())
	fmt.Println("Tools: echo, calculator, get_time, remember, read_file, write_file, bash, glob, grep")
	fmt.Printf("Memory: %s\n", memPath)
	fmt.Printf("Session file: %s\n", sessPath)
	fmt.Println("Commands: /quit, /memory, /clear, /new, /session, /learn")
	fmt.Println("---")

	runREPL(ctx, state)
}

// envOr returns os.Getenv(key) or defaultValue if unset.
func envOr(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}
