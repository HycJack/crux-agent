// Package autolearn provides automatic memory extraction triggers.
//
// Trigger sources:
//  1. Explicit markers: user input contains "[remember:key=value]"
//  2. Tool result markers: output contains "REMEMBER:key=value"
//  3. LLM extraction: async LLM call every N turns to extract facts
//  4. Natural language: regex-based extraction for common patterns
//
// Design principles:
//   - Async execution: doesn't block main conversation
//   - Incremental dedup: same key only updates once
//   - Can be disabled: via Settings.AutoLearn = false
package autolearn

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/hycjack/crux-ai/core"
	"crux-agent-runtime/memory"
)

// TriggerSource marks the trigger origin.
type TriggerSource string

const (
	SourceUserInput  TriggerSource = "user"    // User input
	SourceToolResult TriggerSource = "tool"    // Tool result
	SourceLLMExtract TriggerSource = "extract" // LLM extraction
)

// Settings configures auto-learning.
type Settings struct {
	// AutoLearn enables LLM-based extraction.
	AutoLearn bool

	// ExtractEveryN triggers LLM extraction every N turns.
	ExtractEveryN int

	// MinConfidence is the confidence threshold for extraction (0-1).
	MinConfidence float64
}

// DefaultSettings returns sensible defaults.
func DefaultSettings() Settings {
	return Settings{
		AutoLearn:     false, // Disabled by default (avoids LLM cost)
		ExtractEveryN: 5,
		MinConfidence: 0.7,
	}
}

// Trigger is a single memory trigger event.
type Trigger struct {
	Source  TriggerSource
	Key     string
	Value   string
	Context string // Source context (for LLM extraction)
	Time    time.Time
}

// Extractor is the interface for memory extraction.
type Extractor interface {
	// Extract extracts memorable facts from conversation history.
	Extract(ctx context.Context, messages []core.Message) ([]Trigger, error)
}

// AutoLearner coordinates various trigger sources.
type AutoLearner struct {
	settings Settings
	mem      *memory.Memory
	mu       sync.Mutex
	counter  int
}

// New creates a new auto-learner.
func New(mem *memory.Memory, settings Settings) *AutoLearner {
	return &AutoLearner{
		settings: settings,
		mem:      mem,
	}
}

// Settings returns the current settings.
func (a *AutoLearner) Settings() Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.settings
}

// --- 1. Explicit marker triggers ---

var (
	// Matches "请记住：xxx=yyy" or "记住 xxx=yyy" or "[remember:xxx=yyy]"
	rememberRegex = regexp.MustCompile(`(?:请记住[：:]?|记住[：:]\s*|\[remember:\s*)([^\s=]+)\s*=\s*([^\n\]]+?)(?:\]|$|\n)`)
	// Matches "[memorize:key=value]"
	memorizeRegex = regexp.MustCompile(`\[memorize:\s*([^\s=]+)\s*=\s*([^\]]+?)\]`)
)

// ExtractFromUserInput extracts memory markers from user input.
//
// Patterns:
//
//	"请记住：user.name=小明"
//	"记住：user.name=小明"
//	"[remember:user.name=小明]"
//	"[memorize:user.name=小明]"
func ExtractFromUserInput(text string) []Trigger {
	triggers := []Trigger{}
	now := time.Now()

	for _, m := range rememberRegex.FindAllStringSubmatch(text, -1) {
		if len(m) >= 3 {
			triggers = append(triggers, Trigger{
				Source: SourceUserInput,
				Key:    strings.TrimSpace(m[1]),
				Value:  strings.TrimSpace(m[2]),
				Time:   now,
			})
		}
	}
	for _, m := range memorizeRegex.FindAllStringSubmatch(text, -1) {
		if len(m) >= 3 {
			triggers = append(triggers, Trigger{
				Source: SourceUserInput,
				Key:    strings.TrimSpace(m[1]),
				Value:  strings.TrimSpace(m[2]),
				Time:   now,
			})
		}
	}

	return triggers
}

// --- 2. Tool result triggers ---

// ExtractFromToolResult extracts memory markers from tool output.
//
// Patterns:
//
//	"REMEMBER:user.name=小明"
//	"SAVE_MEMORY:preference.language=zh-CN"
func ExtractFromToolResult(text string) []Trigger {
	triggers := []Trigger{}
	now := time.Now()

	remRegex := regexp.MustCompile(`REMEMBER:\s*([^\s=]+)\s*=\s*([^\n]+)`)
	for _, m := range remRegex.FindAllStringSubmatch(text, -1) {
		if len(m) >= 3 {
			triggers = append(triggers, Trigger{
				Source: SourceToolResult,
				Key:    strings.TrimSpace(m[1]),
				Value:  strings.TrimSpace(m[2]),
				Time:   now,
			})
		}
	}

	return triggers
}

// --- 3. Natural language extraction ---

var (
	namingAssistantRegex = regexp.MustCompile(`(?i)(?:你|助手|机器人|AI)[的]?名字(?:叫|是)\s*[''"「]?([^'"」\s，。,.!?！？]{1,20})`)
	beingCalledRegex     = regexp.MustCompile(`(?i)(?:你|助手|机器人|AI)(?:就是|叫|是)\s*[''"「]?([^'"」\s，。,.!?！？]{1,20})`)
	namingUserRegex      = regexp.MustCompile(`(?i)(?:我)[的]?名字(?:叫|是)\s*[''"「]?([^'"」\s，。,.!?！？]{1,20})`)
	introUserNameRegex   = regexp.MustCompile(`(?i)^(?:我(?:叫|是))\s*[''"「]?([^'"」\s，。,.!?！？\n]{1,20})`)
	introCityRegex       = regexp.MustCompile(`(?i)(?:我)?(?:来自|在|住|是)\s*([\p{Han}A-Za-z]{2,15}(?:市|省|区|县|国|州)?)`)
	preferredLangRegex   = regexp.MustCompile(`(?i)(?:用|请用|使用|讲|说)\s*([\p{Han}A-Za-z]+)\s*(?:回答|交流|沟通|回复)`)
)

// ExtractFromNaturalLanguage extracts common facts without LLM.
func ExtractFromNaturalLanguage(text string) []Trigger {
	triggers := []Trigger{}
	now := time.Now()
	seen := make(map[string]bool)

	add := func(key, value string) {
		if value == "" || seen[key] {
			return
		}
		seen[key] = true
		triggers = append(triggers, Trigger{
			Source: SourceUserInput,
			Key:    key,
			Value:  value,
			Time:   now,
		})
	}

	for _, m := range namingAssistantRegex.FindAllStringSubmatch(text, -1) {
		if len(m) >= 2 {
			add("assistant.name", strings.TrimSpace(m[1]))
		}
	}
	for _, m := range beingCalledRegex.FindAllStringSubmatch(text, -1) {
		if len(m) >= 2 {
			add("assistant.name", strings.TrimSpace(m[1]))
		}
	}
	for _, m := range namingUserRegex.FindAllStringSubmatch(text, -1) {
		if len(m) >= 2 {
			add("user.name", strings.TrimSpace(m[1]))
		}
	}
	for _, m := range introUserNameRegex.FindAllStringSubmatch(text, -1) {
		if len(m) >= 2 {
			v := strings.TrimSpace(m[1])
			if !isCommonWord(v) {
				add("user.name", v)
			}
		}
	}
	for _, m := range introCityRegex.FindAllStringSubmatch(text, -1) {
		if len(m) >= 2 {
			add("user.location", strings.TrimSpace(m[1]))
		}
	}
	for _, m := range preferredLangRegex.FindAllStringSubmatch(text, -1) {
		if len(m) >= 2 {
			add("user.preferred_language", strings.TrimSpace(m[1]))
		}
	}

	return triggers
}

func isCommonWord(s string) bool {
	common := []string{"是", "的", "你", "我", "他", "她", "它", "要", "有", "在", "从", "到", "叫", "了", "吗", "吧"}
	for _, c := range common {
		if s == c {
			return true
		}
	}
	return false
}

// --- 4. LLM extraction ---

// LLMSimpleExtractor calls an LLM to extract memorable facts.
type LLMSimpleExtractor struct {
	// SummarizeFunc calls the LLM synchronously.
	SummarizeFunc func(ctx context.Context, prompt string) (string, error)
}

// Extract implements the Extractor interface.
func (e *LLMSimpleExtractor) Extract(ctx context.Context, messages []core.Message) ([]Trigger, error) {
	if e.SummarizeFunc == nil {
		return nil, fmt.Errorf("SummarizeFunc not set")
	}

	var sb strings.Builder
	sb.WriteString("你是记忆提取助手。请从下面的对话中找出需要**长期记住**的事实。\n\n")
	sb.WriteString("【输出格式】每行一条 `KEY=VALUE`，单独成行。\n")
	sb.WriteString("  - KEY 必须是下方白名单内的 key。\n")
	sb.WriteString("  - VALUE 是简短的事实值。\n")
	sb.WriteString("  - 没有任何值得记住的内容 → 单独输出 `NONE`。\n\n")
	sb.WriteString("【允许的 KEY 白名单】\n")
	sb.WriteString("- user.name: 用户姓名\n")
	sb.WriteString("- user.location: 所在地\n")
	sb.WriteString("- user.preferred_language: 语言偏好\n")
	sb.WriteString("- user.preferred_response_style: 回答风格\n")
	sb.WriteString("- assistant.name: AI 名字\n")
	sb.WriteString("- task.current: 当前任务\n")
	sb.WriteString("- project.tech_stack: 技术栈\n")
	sb.WriteString("- fact.<具体>: 关键事实\n")
	sb.WriteString("- decision.<具体>: 已做决策\n\n")
	sb.WriteString("【严格禁止】不要输出白名单之外的 key，不要输出客套话。\n\n")
	sb.WriteString("对话：\n")
	for _, msg := range messages {
		switch m := msg.(type) {
		case core.UserMessage:
			fmt.Fprintf(&sb, "用户: %v\n", m.Content)
		case core.AssistantMessage:
			var text string
			for _, b := range m.Content {
				if c, ok := b.(core.TextContent); ok {
					text += c.Text
				}
			}
			fmt.Fprintf(&sb, "助手: %s\n", text)
		}
	}

	response, err := e.SummarizeFunc(ctx, sb.String())
	if err != nil {
		return nil, err
	}

	return parseExtractionResult(response, SourceLLMExtract), nil
}

// allowedKeyPrefixes is the whitelist for LLM-extracted keys.
var allowedKeyPrefixes = []string{
	"user.", "assistant.", "task.", "project.",
	"fact.", "decision.", "constraint.",
	"relation.", "family.", "pet.",
	"health.", "diet.", "date.", "asset.",
	"style.", "tool.", "goal.", "pain.",
}

func allowedKeyPrefix(key string) bool {
	for _, p := range allowedKeyPrefixes {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

var extractionRegex = regexp.MustCompile(`^([^\s=:：]+)\s*[=:：]\s*(.+)$`)

func parseExtractionResult(response string, source TriggerSource) []Trigger {
	triggers := []Trigger{}
	now := time.Now()
	seen := make(map[string]bool)

	for _, line := range strings.Split(response, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "NONE" || strings.HasPrefix(line, "#") {
			continue
		}
		m := extractionRegex.FindStringSubmatch(line)
		if len(m) < 3 {
			continue
		}
		key := strings.TrimSpace(m[1])
		value := strings.TrimSpace(m[2])
		value = strings.Trim(value, "\"'「」『』")

		if key == "" || value == "" {
			continue
		}
		if source == SourceLLMExtract && !allowedKeyPrefix(key) {
			continue
		}
		if len(value) > 200 {
			value = value[:200]
		}
		if seen[key] {
			continue
		}
		seen[key] = true

		triggers = append(triggers, Trigger{
			Source: source,
			Key:    key,
			Value:  value,
			Time:   now,
		})
	}
	return triggers
}

// --- Main flow ---

// ProcessUserInput processes user input and saves extracted memories.
// Returns the number of triggers applied.
func (a *AutoLearner) ProcessUserInput(text string) int {
	if a.mem == nil {
		return 0
	}
	triggers := append(ExtractFromUserInput(text), ExtractFromNaturalLanguage(text)...)
	return a.apply(triggers)
}

// ProcessToolResult processes tool output and saves extracted memories.
func (a *AutoLearner) ProcessToolResult(text string) int {
	if a.mem == nil {
		return 0
	}
	triggers := ExtractFromToolResult(text)
	return a.apply(triggers)
}

// MaybeExtract checks if LLM extraction should be triggered.
// Triggers every ExtractEveryN turns. Returns whether extraction was triggered.
func (a *AutoLearner) MaybeExtract(ctx context.Context, messages []core.Message, extractor Extractor) bool {
	if !a.settings.AutoLearn || extractor == nil || a.mem == nil {
		return false
	}

	a.mu.Lock()
	a.counter++
	shouldExtract := a.counter%a.settings.ExtractEveryN == 0
	a.mu.Unlock()

	if !shouldExtract {
		return false
	}

	triggers, err := extractor.Extract(ctx, messages)
	if err != nil {
		return false
	}

	a.apply(triggers)
	return true
}

// apply applies triggers to memory (dedup + persist).
func (a *AutoLearner) apply(triggers []Trigger) int {
	count := 0
	for _, t := range triggers {
		if t.Key == "" || t.Value == "" {
			continue
		}
		a.mem.SetWithCategory(t.Key, t.Value, string(t.Source))
		count++
	}
	if count > 0 {
		_ = a.mem.Save()
	}
	return count
}
