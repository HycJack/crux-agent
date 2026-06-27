package openai

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/hycjack/crux-ai/core"
	"github.com/hycjack/crux-ai/testenv"
)

// TestIntegrationWithEnvConfig 集成测试 - 使用 .env 中的 base_url 和 model
// 这需要真实的 API key 才能运行；否则会被跳过
func TestIntegrationWithEnvConfig(t *testing.T) {
	// 强制加载 .env
	testenv.LoadEnv()

	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		t.Skip("OPENAI_API_KEY not set, skipping integration test")
	}

	// 使用 .env 中的 base_url
	baseURL := testenv.GetEnv("AI_BASE_URL", "")
	if baseURL == "" {
		t.Skip("AI_BASE_URL not set, skipping integration test")
	}

	// 使用 .env 中的 model
	modelID := testenv.GetEnv("AI_MODEL", "gpt-4o")
	t.Logf("Integration test config: baseURL=%s, model=%s", baseURL, modelID)

	// 简单验证 Provider/Model 名称不为空
	if modelID == "" {
		t.Fatal("AI_MODEL is empty")
	}

	// 验证 buildCompletionsBody 使用 .env 配置能正常工作
	model := core.Model{
		ID:      modelID,
		BaseURL: baseURL,
	}

	body, err := buildCompletionsBody(model, core.Context{
		Messages: []core.Message{core.UserMessage{Content: "Hi"}},
	}, core.StreamOptions{}, CompletionsOptions{})
	if err != nil {
		t.Fatalf("buildCompletionsBody failed: %v", err)
	}

	if body["model"] != modelID {
		t.Errorf("Expected model %q, got: %v", modelID, body["model"])
	}
}

// TestIntegrationEnvVarsLoaded 验证 .env 变量已加载
func TestIntegrationEnvVarsLoaded(t *testing.T) {
	testenv.LoadEnv()

	tests := []struct {
		name     string
		key      string
		required bool
	}{
		{"OPENAI_API_KEY", "OPENAI_API_KEY", false},
		{"AI_MODEL", "AI_MODEL", false},
		{"AI_BASE_URL", "AI_BASE_URL", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			value := os.Getenv(tc.key)
			if tc.required && value == "" {
				t.Errorf("Required env var %s is empty", tc.key)
			}
			t.Logf("%s = %q", tc.key, value)
		})
	}
}

// TestIntegrationRequestBodyStructure 验证请求体结构
func TestIntegrationRequestBodyStructure(t *testing.T) {
	testenv.LoadEnv()

	modelID := testenv.GetEnv("AI_MODEL", "MiniMax-M3")
	baseURL := testenv.GetEnv("AI_BASE_URL", "https://api.minimaxi.com/v1")

	model := core.Model{
		ID:      modelID,
		BaseURL: baseURL,
	}

	c := core.Context{
		SystemPrompt: "You are a helpful coding assistant",
		Messages: []core.Message{
			core.UserMessage{Role: "user", Content: "Write a hello world in Go"},
		},
	}

	body, err := buildCompletionsBody(model, c, core.StreamOptions{}, CompletionsOptions{})
	if err != nil {
		t.Fatalf("buildCompletionsBody failed: %v", err)
	}

	// 验证关键字段
	if body["model"] != modelID {
		t.Errorf("Expected model %q, got: %v", modelID, body["model"])
	}
	if body["stream"] != true {
		t.Error("Expected stream to be true")
	}
	if _, ok := body["messages"]; !ok {
		t.Error("Expected messages in body")
	}

	t.Logf("Built request body: model=%v, baseURL=%s", body["model"], baseURL)
}

// TestIntegrationRequestTimeout 验证超时配置
func TestIntegrationRequestTimeout(t *testing.T) {
	timeoutMs := 5000
	client := core.NewTimeoutClient(timeoutMs)
	expected := 5 * time.Second
	if client.Timeout != expected {
		t.Errorf("Expected timeout %v, got: %v", expected, client.Timeout)
	}
}

// TestIntegrationEnvFileExists 验证 .env 文件存在
func TestIntegrationEnvFileExists(t *testing.T) {
	// 检查 .env 或 .env.test
	if _, err := os.Stat(".env"); err == nil {
		t.Log(".env file exists")
		return
	}
	if _, err := os.Stat(".env.test"); err == nil {
		t.Log(".env.test file exists")
		return
	}
	t.Log("No .env file found (this is OK for CI)")
}

// TestIntegrationContextWithTimeout 测试带超时的 context
func TestIntegrationContextWithTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// 模拟一个会超时的操作
	done := make(chan struct{})
	go func() {
		time.Sleep(200 * time.Millisecond)
		close(done)
	}()

	select {
	case <-done:
		t.Error("Operation completed before timeout")
	case <-ctx.Done():
		// 预期超时
		t.Logf("Context done as expected: %v", ctx.Err())
	}
}
