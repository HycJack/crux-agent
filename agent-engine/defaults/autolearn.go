package defaults

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/hycjack/agent-engine/plugin"
	core "github.com/hycjack/crux-ai/core"
)

// ─── Trigger source constants ───────────────────────────────────────────────

type triggerSource string

const (
	sourceUserInput  triggerSource = "user"
	sourceToolResult triggerSource = "tool"
	sourceLLMExtract triggerSource = "extract"
)

// Trigger 是单条记忆触发事件。
type Trigger struct {
	Source  triggerSource
	Key     string
	Value   string
	Context string // Source context (for LLM extraction)
	Time    time.Time
}

// ─── Explicit marker extraction (string scanning, no regex) ─────────────────

// ExtractFromUserInput extracts explicit memory markers from user input:
// "[remember:key=value]" and "[memorize:key=value]".
func ExtractFromUserInput(text string) []Trigger {
	triggers := []Trigger{}
	now := time.Now()

	scanMarker := func(marker string) {
		searchFrom := 0
		for {
			start := strings.Index(text[searchFrom:], marker)
			if start < 0 {
				return
			}
			start += searchFrom
			after := text[start+len(marker):]

			end := strings.Index(after, "]")
			var inner string
			if end < 0 {
				inner = after
			} else {
				inner = after[:end]
			}

			if key, value, ok := splitKeyValue(inner); ok {
				triggers = append(triggers, Trigger{
					Source: sourceUserInput,
					Key:    key,
					Value:  value,
					Time:   now,
				})
			}
			searchFrom = start + len(marker)
		}
	}

	scanMarker("[remember:")
	scanMarker("[memorize:")
	return triggers
}

// ExtractFromToolResult extracts explicit memory markers from tool output:
// "REMEMBER:key=value".
func ExtractFromToolResult(text string) []Trigger {
	triggers := []Trigger{}
	now := time.Now()

	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "REMEMBER:") {
			continue
		}
		if key, value, ok := splitKeyValue(strings.TrimPrefix(line, "REMEMBER:")); ok {
			triggers = append(triggers, Trigger{
				Source: sourceToolResult,
				Key:    key,
				Value:  value,
				Time:   now,
			})
		}
	}
	return triggers
}

// ─── KEY=VALUE parsing (no regex) ───────────────────────────────────────────

// splitKeyValue splits a "KEY=VALUE" / "KEY:VALUE" / "KEY：VALUE" string on
// the first separator, requiring the KEY to be a single whitespace-free
// token. Returns (key, value, true) on success.
func splitKeyValue(s string) (string, string, bool) {
	t := strings.TrimSpace(s)
	if t == "" || t == "NONE" || strings.HasPrefix(t, "#") {
		return "", "", false
	}

	sep := strings.IndexAny(t, "=:：")
	if sep < 0 {
		return "", "", false
	}
	key := strings.TrimSpace(t[:sep])
	if key == "" || strings.ContainsAny(key, " \t") {
		return "", "", false
	}
	value := strings.TrimSpace(t[sep+1:])
	value = strings.Trim(value, "\"'「」『』")
	if value == "" {
		return "", "", false
	}
	return key, value, true
}

// ─── Quality gates for LLM-extracted values ────────────────────────────────

var transientValueKeywords = []string{
	"今天", "明天", "昨天", "刚才", "刚刚", "现在", "目前", "此刻",
	"马上", "立刻", "今晚", "今早", "今明", "周一", "周二", "周三", "周四", "周五", "周六", "周日",
	"早上", "中午", "下午", "晚上", "凌晨",
}

var politeValueKeywords = []string{
	"你好", "您好", "谢谢", "感谢", "好的", "嗯", "是的", "不是", "对的",
	"再见", "拜拜", "ok", "OK", "Ok",
}

const valuableMinLen = 2

func isValuableValue(value string) bool {
	v := strings.TrimSpace(value)
	if utf8.RuneCountInString(v) < valuableMinLen {
		return false
	}
	if strings.ContainsAny(v, "?？") {
		return false
	}
	for _, kw := range transientValueKeywords {
		if strings.HasPrefix(v, kw) {
			return false
		}
	}
	if startsWithPersonalPronoun(v) {
		for _, kw := range transientValueKeywords {
			if strings.Contains(v, kw) {
				return false
			}
		}
	}
	for _, kw := range politeValueKeywords {
		if v == kw {
			return false
		}
	}
	return true
}

func startsWithPersonalPronoun(v string) bool {
	pronouns := []string{"我", "我们", "你", "你们", "他", "她", "他们", "她们", "它"}
	for _, p := range pronouns {
		if strings.HasPrefix(v, p) {
			return true
		}
	}
	return false
}

var allowedKeyPrefixes = []string{
	"user.", "assistant.", "project.",
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

func parseExtractionResult(response string, source triggerSource) []Trigger {
	triggers := []Trigger{}
	now := time.Now()
	seen := make(map[string]bool)

	for _, line := range strings.Split(response, "\n") {
		key, value, ok := splitKeyValue(line)
		if !ok {
			continue
		}
		if source == sourceLLMExtract && !allowedKeyPrefix(key) {
			continue
		}
		if len(value) > 200 {
			value = value[:200]
		}
		if source == sourceLLMExtract && !isValuableValue(value) {
			continue
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

// ─── LLM signal extractor ───────────────────────────────────────────────────

// LLMSignalExtractor extracts structured facts from a chunk of text using an
// LLM. There is no regex pre-filtering: the model itself judges whether the
// text contains anything worth remembering and emits KEY=VALUE lines (or
// NONE). The whitelist + value quality gates still apply on the output.
type LLMSignalExtractor struct {
	SummarizeFunc func(ctx context.Context, prompt string) (string, error)
}

// ExtractFromText builds an extraction prompt from the text and parses the
// LLM's KEY=VALUE output into triggers.
func (e *LLMSignalExtractor) ExtractFromText(ctx context.Context, text string) ([]Trigger, error) {
	if e.SummarizeFunc == nil {
		return nil, fmt.Errorf("SummarizeFunc not set")
	}
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	response, err := e.SummarizeFunc(ctx, buildExtractionPrompt(text))
	if err != nil {
		return nil, err
	}
	return parseExtractionResult(response, sourceLLMExtract), nil
}

func buildExtractionPrompt(text string) string {
	var sb strings.Builder
	sb.WriteString("你是记忆提取助手。阅读下面的用户输入，判断是否真的包含值得**长期记住**的事实。\n")
	sb.WriteString("如果有，按 `KEY=VALUE` 每行一条输出；如果没有值得记住的内容，输出 `NONE`。\n\n")
	sb.WriteString("【什么算「有价值的事实」——严格筛选】\n")
	sb.WriteString("✅ 值得记住：\n")
	sb.WriteString("- 用户稳定身份/属性：姓名、所在地、长期职业、长期偏好\n")
	sb.WriteString("- 用户明确表达的长期偏好（\"以后都用 X\"、\"我不喜欢 Y\"、\"回答简短点\"）\n")
	sb.WriteString("- 用户为 AI 设定的稳定属性：\"你叫 X\"、\"你风格 Y\"\n")
	sb.WriteString("- 长期有用的关键事实/已做决策（影响后续多次对话）\n\n")
	sb.WriteString("❌ 不要记住：\n")
	sb.WriteString("- 寒暄/客套（\"你好\"、\"今天天气不错\"、\"我先睡了\"）\n")
	sb.WriteString("- 临时状态（\"我现在在调试 X\"、\"我今天心情不好\"、\"今天周五\"）\n")
	sb.WriteString("- 一次性事件（\"我刚买了 Y\"、\"刚刚发生 Z\"）\n")
	sb.WriteString("- 当前任务细节（\"我在改这个 bug\"、\"我正在写 XX 文件\"）\n")
	sb.WriteString("- 模糊/无法验证的内容（\"我大概 30 左右\"）\n")
	sb.WriteString("- 对话噪声（\"好的\"、\"嗯\"、\"继续\"）\n\n")
	sb.WriteString("【允许的 KEY 白名单】\n")
	sb.WriteString("- user.name / user.location / user.preferred_language / user.preferred_response_style\n")
	sb.WriteString("- user.likes / user.dislikes: 长期喜欢/讨厌\n")
	sb.WriteString("- assistant.name / assistant.personality\n")
	sb.WriteString("- fact.<具体> / decision.<具体> / constraint.<具体> / project.<具体>\n\n")
	sb.WriteString("【严格禁止】\n")
	sb.WriteString("- 不要把疑问句当成陈述（例如 \"你是谁\" 不要输出 assistant.name）。\n")
	sb.WriteString("- 不要输出白名单之外的 key。\n")
	sb.WriteString("- 不要编造信息；用户没说就输出 NONE。\n")
	sb.WriteString("- 宁可漏记，不要乱记：拿不准就 NONE。\n\n")
	sb.WriteString("【用户输入】\n")
	sb.WriteString(text)
	sb.WriteString("\n\n【输出】\n")
	return sb.String()
}

// ─── AutoLearner ────────────────────────────────────────────────────────────

// AutoLearner provides automatic memory extraction from conversations.
type AutoLearner struct {
	mu             sync.Mutex
	mem            *Memory
	counter        int
	extractEveryN  int
	signalExtractor *LLMSignalExtractor
}

// NewAutoLearner creates a new auto-learner backed by the given memory store.
func NewAutoLearner(mem *Memory, extractEveryN int) *AutoLearner {
	if extractEveryN <= 0 {
		extractEveryN = 5
	}
	return &AutoLearner{
		mem:           mem,
		extractEveryN: extractEveryN,
	}
}

// SetSignalExtractor wires up an LLM-backed extractor used to pull facts out
// of each user input. Pass nil to disable LLM extraction.
func (a *AutoLearner) SetSignalExtractor(ext *LLMSignalExtractor) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.signalExtractor = ext
}

// ProcessUserInput scans user input for explicit markers and, when an LLM
// extractor is configured, asks the model directly for memorable facts.
// Returns the number of triggers applied.
func (a *AutoLearner) ProcessUserInput(text string) int {
	return a.ProcessUserInputCtx(context.Background(), text)
}

// ProcessUserInputCtx is the context-aware variant of ProcessUserInput.
func (a *AutoLearner) ProcessUserInputCtx(ctx context.Context, text string) int {
	if a.mem == nil {
		return 0
	}

	// Explicit markers ([remember:...], [memorize:...]) are applied
	// synchronously and never go through the LLM — the user is asserting
	// intent.
	count := a.apply(ExtractFromUserInput(text))

	a.mu.Lock()
	ext := a.signalExtractor
	a.mu.Unlock()
	if ext == nil {
		return count
	}

	// Let the LLM judge whether the input contains anything worth
	// remembering; no regex pre-filtering here.
	triggers, err := ext.ExtractFromText(ctx, text)
	if err != nil {
		return count
	}
	return count + a.apply(triggers)
}

// ProcessToolResult scans tool output for REMEMBER markers.
func (a *AutoLearner) ProcessToolResult(text string) int {
	if a.mem == nil {
		return 0
	}
	return a.apply(ExtractFromToolResult(text))
}

// MaybeExtract triggers extraction if the turn counter matches the interval.
// The extractor is called asynchronously (fire-and-forget).
func (a *AutoLearner) MaybeExtract(ctx context.Context, messages []core.Message, extractor plugin.Extractor) bool {
	a.mu.Lock()
	a.counter++
	counter := a.counter
	a.mu.Unlock()

	if counter%a.extractEveryN != 0 {
		return false
	}
	if extractor == nil {
		return false
	}

	go func() {
		extractCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		triggers, err := extractor.Extract(extractCtx, messages)
		if err != nil {
			return
		}
		for _, t := range triggers {
			a.mem.SetWithCategory(t.Key, t.Value, t.Source)
		}
	}()

	return true
}

// apply applies triggers to memory (dedup + persist).
func (a *AutoLearner) apply(triggers []Trigger) int {
	if a.mem == nil {
		return 0
	}
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

// ─── DefaultExtractor ───────────────────────────────────────────────────────

// DefaultExtractor is a no-op extractor that returns nothing.
// For LLM-based extraction, users should provide their own plugin.Extractor.
type DefaultExtractor struct{}

func (e *DefaultExtractor) Extract(ctx context.Context, messages []core.Message) ([]plugin.Trigger, error) {
	return nil, nil
}

// compile-time assertions
var _ plugin.AutoLearnPlugin = (*AutoLearner)(nil)
var _ plugin.Extractor = (*DefaultExtractor)(nil)
