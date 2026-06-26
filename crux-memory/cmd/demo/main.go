// Command demo runs an end-to-end memory pipeline simulation:
//   1. Capture a synthetic conversation into L0
//   2. Force the pipeline to run (L1 → L2 → L3)
//   3. Print the resulting files
//
// Usage:
//   MODEL_API_KEY=sk-... MODEL_BASE_URL=https://api.openai.com/v1 \
//     MODEL_NAME=gpt-4o-mini \
//     go run ./cmd/demo
//
// If MODEL_API_KEY is empty, the demo still runs but skips LLM-driven
// stages (no L1 extraction, no L2 summarization, no L3 generation).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	openai "github.com/openai/openai-go"

	"github.com/crux-memory/crux-memory/hooks"
	"github.com/crux-memory/crux-memory/l0"
	"github.com/crux-memory/crux-memory/llm"
	"github.com/crux-memory/crux-memory/pipeline"
)

func main() {
	ctx := context.Background()

	// Resolve data dir (CLI arg or /tmp/crux-memory-demo).
	dataDir := "/tmp/crux-memory-demo"
	if len(os.Args) > 1 {
		dataDir = os.Args[1]
	}
	if err := os.RemoveAll(dataDir); err != nil {
		log.Fatalf("clean: %v", err)
	}

	// Build LLM client (optional).
	var llmClient *llm.Client
	apiKey := os.Getenv("MODEL_API_KEY")
	baseURL := os.Getenv("MODEL_BASE_URL")
	model := os.Getenv("MODEL_NAME")
	if model == "" {
		model = "gpt-4o-mini"
	}
	if apiKey != "" || baseURL != "" {
		llmClient = llm.NewClient(baseURL, apiKey, model)
		log.Printf("[demo] LLM enabled: %s @ %s", model, baseURL)
	} else {
		llmClient = llm.NewClient("", "", model)
		llmClient.SetMockFn(mockLLM)
		log.Printf("[demo] LLM mock mode (no API key)")
	}

	// Build pipeline.
	p, err := pipeline.New(dataDir, llmClient, pipeline.Config{
		MessagesPerTick: 2,
		MinInterval:     1 * time.Second,
		MaxInterval:     5 * time.Second,
	})
	if err != nil {
		log.Fatalf("pipeline: %v", err)
	}
	hook := hooks.NewAgentHook(p)

	// Simulate a conversation about a frontend dev learning pinyin.
	sessionID := "demo-session-001"
	messages := []struct {
		role l0.Role
		text string
	}{
		{l0.RoleUser, "我想学拼音，有什么好方法吗？"},
		{l0.RoleAssistant, "可以每天读 10 分钟，注意口型。"},
		{l0.RoleUser, "我喜欢用真实人声而不是机器声来学。"},
		{l0.RoleAssistant, "可以用 du.hanyupinyin.cn 的真人录音。"},
		{l0.RoleUser, "我住在北京，做前端开发，平时用 TypeScript。"},
		{l0.RoleAssistant, "前端 + 拼音学习，要不要做个小项目结合？"},
		{l0.RoleUser, "对，我想做一个拼音点读小程序给孩子用。"},
		{l0.RoleAssistant, "建议先做单韵母和复韵母，配真人发音。"},
		{l0.RoleUser, "我们孩子 6 岁，已经认识 a/o/e。"},
		{l0.RoleAssistant, "那可以跳过基础介绍直接进拼读练习。"},
	}

	fmt.Printf("\n=== Step 1: Capture %d messages into L0 ===\n", len(messages))
	for i, m := range messages {
		ev := hooks.AgentEvent{
			Type:      roleToEventType(m.role),
			SessionID: sessionID,
			Role:      string(m.role),
			Content:   m.text,
		}
		hook.OnEvent(ctx, ev)
		fmt.Printf("  [%d] %s: %s\n", i+1, m.role, m.text)
	}
	// Give async tick a moment to run.
	time.Sleep(2 * time.Second)

	fmt.Printf("\n=== Step 2: Force pipeline tick ===\n")
	if err := p.Run(ctx, sessionID); err != nil {
		log.Printf("[demo] pipeline run error: %v", err)
	}

	fmt.Printf("\n=== Step 3: Files produced ===\n")
	walk(dataDir)
}

func roleToEventType(r l0.Role) string {
	switch r {
	case l0.RoleUser:
		return "user_message"
	case l0.RoleAssistant:
		return "assistant_message"
	}
	return "message"
}

// mockLLM returns canned JSON responses based on which system prompt is in use.
// This lets the demo exercise the full L0→L1→L2→L3 pipeline offline.
func mockLLM(ctx context.Context, system string, messages []openai.ChatCompletionMessageParamUnion) ([]byte, error) {
	switch {
	case strings.Contains(system, "Scene Summarizer"):
		return json.Marshal(map[string]string{"summary": "User learning pinyin for child, prefers human voice"})
	case strings.Contains(system, "L1 Atomic Memory Extractor"):
		// Build a deterministic L1 response from the latest user message
		// (we don't actually need to parse messages for the mock — return
		// a realistic fixed scene + 4 memories).
		return []byte(`{
		  "scenes": [
		    {
		      "scene_name": "user-profile",
		      "message_ids": ["l0_demo_1","l0_demo_5"],
		      "memories": [
		        {"content": "User lives in Beijing", "type": "fact", "priority": 8, "source_message_ids": ["l0_demo_5"], "metadata": {}},
		        {"content": "User is a frontend developer", "type": "fact", "priority": 7, "source_message_ids": ["l0_demo_5"], "metadata": {}},
		        {"content": "User prefers TypeScript", "type": "preference", "priority": 7, "source_message_ids": ["l0_demo_5"], "metadata": {}}
		      ]
		    },
		    {
		      "scene_name": "pinyin-learning",
		      "message_ids": ["l0_demo_1","l0_demo_3","l0_demo_7"],
		      "memories": [
		        {"content": "User is learning pinyin", "type": "fact", "priority": 8, "source_message_ids": ["l0_demo_1"], "metadata": {}},
		        {"content": "User prefers real human voice over TTS for pinyin", "type": "preference", "priority": 9, "source_message_ids": ["l0_demo_3"], "metadata": {}},
		        {"content": "User wants to build a pinyin reading mini-program for their child", "type": "goal", "priority": 9, "source_message_ids": ["l0_demo_7"], "metadata": {}},
		        {"content": "User's child is 6 years old and already knows a/o/e", "type": "fact", "priority": 6, "source_message_ids": ["l0_demo_9"], "metadata": {}}
		      ]
		    }
		  ],
		  "last_scene_name": "pinyin-learning"
		}`), nil
	case strings.Contains(system, "Persona Architect"):
		return []byte(`{
		  "persona": "# User Narrative Profile\n\n> **Archetype**: 前端开发者 × 拼音学习家长，在用工具化思维带孩子学拼音。\n\n> **Basic Information**\n- 居住地: 北京\n- 职业: 前端开发\n- 家庭: 有一个 6 岁孩子\n\n> **Long-term Preferences**\n- 偏好真实人声 (而非 TTS 机器声)\n- 使用 TypeScript\n- 用项目化方式解决问题\n\n## 📖 Chapter 1: Context & Current State\n北京前端开发者，正在做一个拼音点读小程序给孩子用。孩子 6 岁，已认识 a/o/e，可以跳过基础介绍直接进拼读练习。\n\n## 🎨 Chapter 2: The Texture of Life\n白天写 TypeScript，晚上陪孩子学拼音。倾向用真实人声（推荐 du.hanyupinyin.cn 的资源）做教育内容，而不是 TTS。\n\n## 🤖 Chapter 3: Interaction & Cognitive Protocol\n\n### 3.1 How to Speak\n直接、给链接、给代码片段。少铺垫，少客套。\n\n### 3.2 How to Think\n工具化思维 — 先问\"能不能用现有工具/网站解决\"，再考虑自建。\n\n## 🧩 Chapter 4: Deep Insights & Evolution\n\n- **Productive Contradictions**: 想做点读小程序 (自建) 但同时推荐用现成真人录音库 (复用)\n- **Evolution Trajectory**: 从\"问方法\" → \"明确偏好\" → \"确定目标\" → \"细化需求\"\n- **Emergent Traits**:\n  - PragmaticBuilder - 倾向于用现成资源解决问题\n  - ChildFirst - 教育内容以孩子接受度为准\n  - AntiSyntheticVoice - 明确排斥 TTS 机器声"
		}`), nil
	}
	return nil, fmt.Errorf("mockLLM: unknown system prompt")
}

func walk(root string) {
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			fmt.Printf("  📁 %s\n", path)
			return nil
		}
		size := info.Size()
		marker := "📄"
		if size == 0 {
			marker = "📄 (empty)"
		}
		fmt.Printf("  %s %s  (%d B)\n", marker, path, size)
		return nil
	})
	if err != nil {
		log.Printf("walk: %v", err)
	}
}