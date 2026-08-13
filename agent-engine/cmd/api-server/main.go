// api-server 是一个 OpenAI-compatible HTTP API 服务。
//
// 它将 agent-engine 暴露为标准的 /v1/chat/completions 接口，
// 支持流式 (SSE) 和非流式两种模式，兼容任何 OpenAI SDK / curl 调用。
//
// 配置方式：.env 文件或环境变量
//
//	AI_PROVIDER=openai         # 走 OpenAI 协议
//	AI_MODEL=DeepSeek-V4-Flash # 模型名
//	OPENAI_API_KEY=sk-xxx      # API Key
//	BASE_URL=https://.../v1    # 自定义端点（海康 MaaS / DeepSeek / 智谱 等）
//
// 启动:
//
//	go run ./cmd/api-server/
//
// 测试:
//
//	curl http://localhost:8080/v1/chat/completions \
//	  -H "Content-Type: application/json" \
//	  -d '{"model":"DeepSeek-V4-Flash","messages":[{"role":"user","content":"你好"}],"stream":true}'
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
	"strings"
	"syscall"
	"time"

	"github.com/hycjack/agent-engine/api"
	"github.com/hycjack/agent-engine/engine"
	"github.com/hycjack/crux-ai/ai"
	"github.com/hycjack/crux-ai/core"

	// 注册所有内置 provider（包括 openai-compat router）
	_ "github.com/hycjack/crux-ai/providers"
)

// ─── CLI flags ─────────────────────────────────────────────────────────────

var (
	portFlag       = flag.String("port", "8080", "HTTP listen port")
	hostFlag       = flag.String("host", "0.0.0.0", "HTTP listen host")
	providerFlag   = flag.String("provider", "", "provider id (overrides .env)")
	modelFlag      = flag.String("model", "", "model id (overrides .env)")
	systemPrompt   = flag.String("system-prompt", "You are a helpful assistant.", "default system prompt")
	corsFlag       = flag.Bool("cors", true, "enable CORS headers")
	verboseFlag    = flag.Bool("verbose", false, "log streaming errors")
	compactFlag    = flag.Bool("compact", false, "enable auto context compaction")
)

// ─── .env loader ───────────────────────────────────────────────────────────

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

	// 1. 加载 .env
	loadEnv(".env")
	loadEnv("../.env")

	// 2. 解析配置（CLI > 环境变量 > 默认值）
	providerName := *providerFlag
	if providerName == "" {
		providerName = os.Getenv("AI_PROVIDER")
	}
	if providerName == "" {
		providerName = "openai"
	}

	modelID := *modelFlag
	if modelID == "" {
		modelID = os.Getenv("AI_MODEL")
	}
	if modelID == "" {
		modelID = "gpt-4o"
	}

	baseURL := os.Getenv("BASE_URL")

	fmt.Fprintf(os.Stderr, "╭──────────────────────────────────────────────────────╮\n")
	fmt.Fprintf(os.Stderr, "│ agent-engine OpenAI-compatible API Server          │\n")
	fmt.Fprintf(os.Stderr, "├──────────────────────────────────────────────────────┤\n")
	fmt.Fprintf(os.Stderr, "│ provider : %-44s │\n", providerName)
	fmt.Fprintf(os.Stderr, "│ model    : %-44s │\n", modelID)
	if baseURL != "" {
		fmt.Fprintf(os.Stderr, "│ base_url : %-44s │\n", baseURL)
	}
	fmt.Fprintf(os.Stderr, "╰──────────────────────────────────────────────────────╯\n")

	// 3. 解析 Model（支持自定义 BaseURL）
	model, err := ai.GetModel(core.KnownProvider(providerName), modelID)
	if err != nil {
		// 模型不在注册表中，手动构造
		fmt.Fprintf(os.Stderr, "[warn] model %s/%s not in registry: %v\n", providerName, modelID, err)
		fmt.Fprintf(os.Stderr, "[warn] using manual model config (base_url=%s)\n", baseURL)
		model = core.Model{
			ID:       modelID,
			Provider: core.KnownProvider(providerName),
			API:      core.APIOpenAICompletions, // ← 关键：走 OpenAI 兼容协议
		}
	}
	if baseURL != "" {
		model.BaseURL = baseURL
	}

	// 4. 解析 API Key
	apiKey := resolveAPIKey(core.KnownProvider(providerName))
	if apiKey == "" {
		// 尝试通用的 OPENAI_API_KEY（很多第三方服务也用这个）
		apiKey = os.Getenv("OPENAI_API_KEY")
	}
	if apiKey == "" {
		fmt.Fprintf(os.Stderr, "[warn] no API key found, requests will likely fail\n")
	} else {
		fmt.Fprintf(os.Stderr, "[info] API key resolved (%d chars)\n", len(apiKey))
	}

	// 5. 可选的 Compaction
	var compCfg engine.CompactionConfig
	if *compactFlag {
		compCfg = engine.CompactionConfig{
			MaxTokens:      100000,
			ReserveTokens:  4096,
			OverflowRetries: 1,
		}
		fmt.Fprintf(os.Stderr, "[info] compaction enabled (budget=%d)\n", compCfg.MaxTokens)
	}

	// 6. 创建 Handler
	handler := api.NewChatHandler(api.HandlerConfig{
		Model:        model,
		SystemPrompt: *systemPrompt,
		SimpleStreamOptions: core.SimpleStreamOptions{
			StreamOptions: core.StreamOptions{
				APIKey: apiKey,
			},
		},
		Compaction:          compCfg,
		EnableCORS:          *corsFlag,
		LogStreamingErrors:  *verboseFlag,
		RequestTimeout:      5 * time.Minute,
		ResponseIDGenerator: defaultResponseID,
	})

	// 7. 启动 HTTP 服务
	addr := fmt.Sprintf("%s:%s", *hostFlag, *portFlag)
	mux := http.NewServeMux()
	mux.Handle("POST /v1/chat/completions", handler)

	// 健康检查
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status":   "ok",
			"provider": providerName,
			"model":    modelID,
			"time":     time.Now().Unix(),
		})
	})

	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	// 优雅关闭
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		fmt.Fprintf(os.Stderr, "\n[shutdown] received signal, shutting down...\n")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()

	fmt.Fprintf(os.Stderr, "[server] listening on http://%s\n", addr)
	fmt.Fprintf(os.Stderr, "[server] POST /v1/chat/completions (OpenAI-compatible)\n")
	fmt.Fprintf(os.Stderr, "[server] GET  /health\n")

	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("[fatal] server error: %v", err)
	}
	fmt.Fprintf(os.Stderr, "[server] stopped\n")
}

// ─── Helpers ───────────────────────────────────────────────────────────────

func resolveAPIKey(p core.KnownProvider) string {
	envKey := envKeyForProvider(p)
	if envKey != "" {
		if key := os.Getenv(envKey); key != "" {
			return key
		}
	}
	return ""
}

func envKeyForProvider(p core.KnownProvider) string {
	switch p {
	case core.ProviderAnthropic:
		return "ANTHROPIC_API_KEY"
	case core.ProviderOpenAI:
		return "OPENAI_API_KEY"
	case core.ProviderGoogle:
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
		return ""
	}
	// 通用 fallback — 很多 OpenAI 兼容服务也用 OPENAI_API_KEY
	return "OPENAI_API_KEY"
}

var idCounter int64

func defaultResponseID() string {
	idCounter++
	return fmt.Sprintf("chatcmpl-%d", idCounter)
}
