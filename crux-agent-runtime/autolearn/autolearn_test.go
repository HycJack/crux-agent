package autolearn

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hycjack/crux-ai/core"
	"crux-agent-runtime/memory"
)

func TestExtractFromUserInput_Remember(t *testing.T) {
	tests := []struct {
		input string
		key   string
		value string
	}{
		{"请记住：user.name=小明", "user.name", "小明"},
		{"记住：user.name=小明", "user.name", "小明"},
		{"[remember:user.name=小明]", "user.name", "小明"},
		{"[memorize:user.name=小明]", "user.name", "小明"},
	}

	for _, tt := range tests {
		triggers := ExtractFromUserInput(tt.input)
		if len(triggers) == 0 {
			t.Errorf("no triggers for %q", tt.input)
			continue
		}
		if triggers[0].Key != tt.key || triggers[0].Value != tt.value {
			t.Errorf("input=%q: got key=%q value=%q, want key=%q value=%q",
				tt.input, triggers[0].Key, triggers[0].Value, tt.key, tt.value)
		}
	}
}

func TestExtractFromUserInput_NoMatch(t *testing.T) {
	triggers := ExtractFromUserInput("今天天气怎么样？")
	if len(triggers) != 0 {
		t.Errorf("expected 0 triggers, got %d", len(triggers))
	}
}

func TestExtractFromToolResult(t *testing.T) {
	tests := []struct {
		input string
		key   string
	}{
		{"REMEMBER:user.name=小明", "user.name"},
		{"REMEMBER: preference.language = zh-CN", "preference.language"},
	}

	for _, tt := range tests {
		triggers := ExtractFromToolResult(tt.input)
		if len(triggers) == 0 {
			t.Errorf("no triggers for %q", tt.input)
			continue
		}
		if triggers[0].Key != tt.key {
			t.Errorf("input=%q: got key=%q, want %q", tt.input, triggers[0].Key, tt.key)
		}
	}
}

func TestExtractFromNaturalLanguage_AssistantName(t *testing.T) {
	tests := []string{
		"你叫小七",
		"你的名字叫小七",
		"你是小七",
	}

	for _, input := range tests {
		triggers := ExtractFromNaturalLanguage(input)
		found := false
		for _, tr := range triggers {
			if tr.Key == "assistant.name" && tr.Value == "小七" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("input=%q: expected assistant.name=小七", input)
		}
	}
}

func TestExtractFromNaturalLanguage_UserName(t *testing.T) {
	tests := []string{
		"我叫张三",
		"我的名字叫张三",
	}

	for _, input := range tests {
		triggers := ExtractFromNaturalLanguage(input)
		found := false
		for _, tr := range triggers {
			if tr.Key == "user.name" && tr.Value == "张三" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("input=%q: expected user.name=张三", input)
		}
	}
}

func TestExtractFromNaturalLanguage_Location(t *testing.T) {
	triggers := ExtractFromNaturalLanguage("我来自杭州")
	found := false
	for _, tr := range triggers {
		if tr.Key == "user.location" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected user.location trigger")
	}
}

func TestExtractFromNaturalLanguage_Language(t *testing.T) {
	triggers := ExtractFromNaturalLanguage("请用中文回答")
	found := false
	for _, tr := range triggers {
		if tr.Key == "user.preferred_language" && tr.Value == "中文" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected user.preferred_language=中文")
	}
}

func TestExtractFromNaturalLanguage_NoMatch(t *testing.T) {
	triggers := ExtractFromNaturalLanguage("今天天气怎么样？")
	if len(triggers) != 0 {
		t.Errorf("expected 0 triggers, got %d", len(triggers))
	}
}

func TestAutoLearner_ProcessUserInput(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "memory.json")

	mem, _ := memory.New(path)
	learner := New(mem, DefaultSettings())

	count := learner.ProcessUserInput("请记住：user.name=小明")
	if count != 1 {
		t.Errorf("expected 1 trigger, got %d", count)
	}

	val, ok := mem.Get("user.name")
	if !ok || val != "小明" {
		t.Errorf("expected user.name=小明, got %q (exists=%v)", val, ok)
	}
}

func TestAutoLearner_ProcessToolResult(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "memory.json")

	mem, _ := memory.New(path)
	learner := New(mem, DefaultSettings())

	count := learner.ProcessToolResult("REMEMBER:task.status=完成")
	if count != 1 {
		t.Errorf("expected 1 trigger, got %d", count)
	}

	val, ok := mem.Get("task.status")
	if !ok || val != "完成" {
		t.Errorf("expected task.status=完成, got %q (exists=%v)", val, ok)
	}
}

func TestAutoLearner_MaybeExtract_Disabled(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "memory.json")

	mem, _ := memory.New(path)
	settings := DefaultSettings()
	settings.AutoLearn = false
	learner := New(mem, settings)

	triggered := learner.MaybeExtract(context.Background(), nil, nil)
	if triggered {
		t.Error("should not trigger when AutoLearn is false")
	}
}

func TestAutoLearner_MaybeExtract_NilExtractor(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "memory.json")

	mem, _ := memory.New(path)
	settings := DefaultSettings()
	settings.AutoLearn = true
	learner := New(mem, settings)

	triggered := learner.MaybeExtract(context.Background(), nil, nil)
	if triggered {
		t.Error("should not trigger with nil extractor")
	}
}

func TestAutoLearner_MaybeExtract_TurnCounting(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "memory.json")

	mem, _ := memory.New(path)
	settings := DefaultSettings()
	settings.AutoLearn = true
	settings.ExtractEveryN = 3
	learner := New(mem, settings)

	extractor := &LLMSimpleExtractor{
		SummarizeFunc: func(ctx context.Context, prompt string) (string, error) {
			return "NONE", nil
		},
	}

	msgs := []core.Message{
		core.UserMessage{Role: core.MessageRoleUser, Content: "hello"},
	}

	// First 2 calls should not trigger.
	if learner.MaybeExtract(context.Background(), msgs, extractor) {
		t.Error("should not trigger on turn 1")
	}
	if learner.MaybeExtract(context.Background(), msgs, extractor) {
		t.Error("should not trigger on turn 2")
	}
	// Third call should trigger.
	if !learner.MaybeExtract(context.Background(), msgs, extractor) {
		t.Error("should trigger on turn 3")
	}
}

func TestLLMSimpleExtractor_Extract(t *testing.T) {
	extractor := &LLMSimpleExtractor{
		SummarizeFunc: func(ctx context.Context, prompt string) (string, error) {
			return "user.name=张三\nuser.location=杭州", nil
		},
	}

	msgs := []core.Message{
		core.UserMessage{Role: core.MessageRoleUser, Content: "我叫张三，来自杭州"},
	}

	triggers, err := extractor.Extract(context.Background(), msgs)
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}
	if len(triggers) != 2 {
		t.Fatalf("expected 2 triggers, got %d", len(triggers))
	}
	if triggers[0].Key != "user.name" || triggers[0].Value != "张三" {
		t.Errorf("unexpected trigger: %v", triggers[0])
	}
}

func TestLLMSimpleExtractor_Extract_NilFunc(t *testing.T) {
	extractor := &LLMSimpleExtractor{}
	_, err := extractor.Extract(context.Background(), nil)
	if err == nil {
		t.Error("expected error with nil SummarizeFunc")
	}
}

func TestLLMSimpleExtractor_Extract_FilterInvalidKeys(t *testing.T) {
	extractor := &LLMSimpleExtractor{
		SummarizeFunc: func(ctx context.Context, prompt string) (string, error) {
			return "user.name=张三\ninvalid_key=value\nfact.bug=空指针", nil
		},
	}

	triggers, _ := extractor.Extract(context.Background(), nil)
	// invalid_key should be filtered out.
	for _, tr := range triggers {
		if tr.Key == "invalid_key" {
			t.Error("invalid_key should be filtered")
		}
	}
}

func TestParseExtractionResult(t *testing.T) {
	response := "user.name=张三\nuser.location=杭州\nNONE\n"
	triggers := parseExtractionResult(response, SourceLLMExtract)

	if len(triggers) != 2 {
		t.Fatalf("expected 2 triggers, got %d", len(triggers))
	}
	if triggers[0].Key != "user.name" || triggers[0].Value != "张三" {
		t.Errorf("unexpected trigger[0]: %v", triggers[0])
	}
}

func TestParseExtractionResult_EmptyLines(t *testing.T) {
	response := "\n\nNONE\n\n# comment\n"
	triggers := parseExtractionResult(response, SourceLLMExtract)
	if len(triggers) != 0 {
		t.Errorf("expected 0 triggers, got %d", len(triggers))
	}
}

func TestParseExtractionResult_Dedup(t *testing.T) {
	response := "user.name=张三\nuser.name=李四"
	triggers := parseExtractionResult(response, SourceLLMExtract)
	if len(triggers) != 1 {
		t.Errorf("expected 1 trigger (dedup), got %d", len(triggers))
	}
}

func TestParseExtractionResult_QuotedValues(t *testing.T) {
	response := `user.name="张三"`
	triggers := parseExtractionResult(response, SourceLLMExtract)
	if len(triggers) != 1 {
		t.Fatalf("expected 1 trigger, got %d", len(triggers))
	}
	if triggers[0].Value != "张三" {
		t.Errorf("expected unquoted value, got %q", triggers[0].Value)
	}
}
