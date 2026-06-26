// Command memoryd runs the crux-memory pipeline as a standalone HTTP daemon.
// It exposes a tiny webhook API that crux-harness (or any other producer)
// can POST turn events to:
//
//	POST /v1/capture
//	  {"session_id":"...","type":"user_message","role":"user","content":"..."}
//
//	POST /v1/tick
//	  {"session_id":"..."}
//
//	GET  /v1/state?session=...
//	  Returns the latest L1/L2/L3 file contents for inspection.
//
//	GET  /healthz
//
// This is the integration point that does NOT require modifying cruxd
// source code: run memoryd alongside cruxd, and have cruxd (or a thin
// shim) forward events. The harness.Consumer type (in ../harness) is the
// preferred in-process integration.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	openai "github.com/openai/openai-go"

	"github.com/crux-memory/crux-memory/l0"
	"github.com/crux-memory/crux-memory/llm"
	"github.com/crux-memory/crux-memory/pipeline"
)

func main() {
	var (
		bind    = flag.String("bind", "127.0.0.1:8420", "listen address")
		dataDir = flag.String("data", "/var/lib/crux-memory", "data directory")
		baseURL = flag.String("base-url", os.Getenv("MODEL_BASE_URL"), "LLM base URL")
		apiKey  = flag.String("api-key", os.Getenv("MODEL_API_KEY"), "LLM API key")
		model   = flag.String("model", os.Getenv("MODEL_NAME"), "LLM model")
	)
	if *model == "" {
		*model = "gpt-4o-mini"
	}
	flag.Parse()

	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		log.Fatalf("memoryd: data dir: %v", err)
	}

	var llmClient *llm.Client
	if *apiKey != "" || *baseURL != "" {
		llmClient = llm.NewClient(*baseURL, *apiKey, *model)
		log.Printf("[memoryd] LLM enabled: %s @ %s", *model, *baseURL)
	} else {
		// Offline mock so the daemon is still useful for local dev / tests.
		llmClient = llm.NewClient("", "", *model)
		llmClient.SetMockFn(mockLLMAdapter)
		log.Printf("[memoryd] LLM mock mode (no MODEL_API_KEY)")
	}

	p, err := pipeline.New(*dataDir, llmClient, pipeline.Config{
		MessagesPerTick: 4,
		MinInterval:     30 * time.Second,
		MaxInterval:     5 * time.Minute,
	})
	if err != nil {
		log.Fatalf("memoryd: pipeline: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/v1/capture", makeCaptureHandler(p))
	mux.HandleFunc("/v1/tick", makeTickHandler(p))
	mux.HandleFunc("/v1/state", makeStateHandler(*dataDir))

	srv := &http.Server{
		Addr:              *bind,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("[memoryd] listening on %s, data=%s", *bind, *dataDir)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("memoryd: serve: %v", err)
		}
	}()
	<-ctx.Done()
	log.Printf("[memoryd] shutting down...")
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
}

type captureReq struct {
	SessionID string `json:"session_id"`
	Type      string `json:"type"`
	Role      string `json:"role"`
	Content   string `json:"content"`
}

func makeCaptureHandler(p *pipeline.Pipeline) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req captureReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if req.SessionID == "" || req.Content == "" {
			http.Error(w, "session_id and content required", http.StatusBadRequest)
			return
		}
		role := l0.RoleAssistant
		switch req.Role {
		case "user":
			role = l0.RoleUser
		case "tool":
			role = l0.RoleTool
		case "system":
			role = l0.RoleSystem
		}
		if err := p.Capture(r.Context(), req.SessionID, role, req.Content); err != nil {
			http.Error(w, "capture: "+err.Error(), http.StatusInternalServerError)
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			if err := p.MaybeTick(ctx); err != nil {
				log.Printf("[memoryd] tick: %v", err)
			}
		}()
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintln(w, "captured")
	}
}

type tickReq struct {
	SessionID string `json:"session_id"`
}

func makeTickHandler(p *pipeline.Pipeline) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req tickReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := p.Run(r.Context(), req.SessionID); err != nil {
			http.Error(w, "tick: "+err.Error(), http.StatusInternalServerError)
			return
		}
		fmt.Fprintln(w, "ticked")
	}
}

func makeStateHandler(dataDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session := r.URL.Query().Get("session")
		if session == "" {
			http.Error(w, "session query param required", http.StatusBadRequest)
			return
		}
		out := map[string]interface{}{}
		// L0
		if b, err := os.ReadFile(filepath.Join(dataDir, session+".jsonl")); err == nil {
			out["l0_bytes"] = len(b)
		}
		// L1
		if b, err := os.ReadFile(filepath.Join(dataDir, "l1", "memories.jsonl")); err == nil {
			out["l1_bytes"] = len(b)
		}
		// L2 scenes
		if entries, err := os.ReadDir(filepath.Join(dataDir, "l2", "scenes")); err == nil {
			var names []string
			for _, e := range entries {
				names = append(names, e.Name())
			}
			out["l2_scenes"] = names
		}
		// L3 persona
		if b, err := os.ReadFile(filepath.Join(dataDir, "l3", "persona.md")); err == nil {
			out["l3_bytes"] = len(b)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}
}

// mockLLMAdapter returns canned JSON for offline mode. Mirrors the demo
// mock so the daemon produces the same shape of output without an API key.
func mockLLMAdapter(ctx context.Context, system string, messages []openai.ChatCompletionMessageParamUnion) ([]byte, error) {
	switch {
	case strings.Contains(system, "Scene Summarizer"):
		return json.Marshal(map[string]string{"summary": "User learning pinyin, prefers human voice"})
	case strings.Contains(system, "L1 Atomic Memory Extractor"):
		return []byte(`{
		  "scenes": [
		    {
		      "scene_name": "user-profile",
		      "message_ids": ["l0_a","l0_b"],
		      "memories": [
		        {"content": "User lives in Beijing", "type": "fact", "priority": 8, "source_message_ids": ["l0_b"], "metadata": {}},
		        {"content": "User is a frontend developer", "type": "fact", "priority": 7, "source_message_ids": ["l0_b"], "metadata": {}},
		        {"content": "User prefers TypeScript", "type": "preference", "priority": 7, "source_message_ids": ["l0_b"], "metadata": {}}
		      ]
		    },
		    {
		      "scene_name": "pinyin-learning",
		      "message_ids": ["l0_c","l0_d"],
		      "memories": [
		        {"content": "User is learning pinyin", "type": "fact", "priority": 8, "source_message_ids": ["l0_c"], "metadata": {}},
		        {"content": "User prefers real human voice over TTS for pinyin", "type": "preference", "priority": 9, "source_message_ids": ["l0_d"], "metadata": {}},
		        {"content": "User's child is 6 years old and knows a/o/e", "type": "fact", "priority": 6, "source_message_ids": ["l0_d"], "metadata": {}}
		      ]
		    }
		  ],
		  "last_scene_name": "pinyin-learning"
		}`), nil
	case strings.Contains(system, "Persona Architect"):
		return []byte(`{
		  "persona": "# User Narrative Profile\n\n> **Archetype**: 前端开发者 × 拼音学习家长。\n\n> **Basic Information**\n- 居住地: 北京\n- 职业: 前端开发\n- 家庭: 6 岁孩子\n\n## 📖 Chapter 1: Context & Current State\n北京前端开发者，正在做拼音点读小程序给孩子用。\n\n## 🤖 Chapter 3: Interaction & Cognitive Protocol\n\n### 3.1 How to Speak\n直接、给链接、给代码片段。少铺垫，少客套。"
		}`), nil
	}
	return nil, fmt.Errorf("mockLLMAdapter: unknown system prompt")
}