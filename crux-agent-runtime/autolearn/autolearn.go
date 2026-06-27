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
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"crux-agent-runtime/memory"

	"github.com/hycjack/crux-ai/core"
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

	// signalExtractor is consulted after regex signals fire. Optional.
	signalExtractor *LLMSignalExtractor

	// WorkflowDir, if set, is the base directory where auto-extracted
	// SKILL.md files are written (each skill in its own subdirectory).
	// When empty, workflow extraction is a no-op.
	WorkflowDir string
}

// New creates a new auto-learner.
func New(mem *memory.Memory, settings Settings) *AutoLearner {
	return &AutoLearner{
		settings: settings,
		mem:      mem,
	}
}

// SetWorkflowDir configures where auto-extracted SKILL.md files go.
// Pass empty string to disable workflow extraction.
func (a *AutoLearner) SetWorkflowDir(dir string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.WorkflowDir = dir
}

// SetSignalExtractor wires up an LLM-backed extractor used when the
// regex-based signal detector decides that a user input likely contains
// memorable information. Pass nil to disable LLM extraction.
func (a *AutoLearner) SetSignalExtractor(ext *LLMSignalExtractor) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.signalExtractor = ext
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

// --- 3. Signal-based natural-language extraction ---
//
// Architecture:
//   - Regex ONLY decides whether the user input is worth extracting from.
//     It returns a list of SignalKinds ("name", "location", "language",
//     "preference", "fact") with NO extracted values.
//   - When at least one signal fires, an LLM extractor is invoked to read
//     the input and produce structured key=value pairs.
//   - This avoids the brittleness of regex capture (e.g. "你是谁" being
//     misread as "assistant.name = 谁") and lets the model handle the
//     ambiguity of natural language.

type SignalKind string

const (
	SignalName       SignalKind = "name"       // user/assistant name mention
	SignalLocation   SignalKind = "location"   // location/region mention
	SignalLanguage   SignalKind = "language"   // language preference
	SignalPreference SignalKind = "preference" // general preference
	SignalFact       SignalKind = "fact"       // any other memorable fact
)

// SignalPatterns are regexes used purely to DETECT whether extraction is
// worth running. They do not capture values — value extraction is left to
// the LLM.
var (
	signalNamePatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(?:你|助手|机器人|AI)[的]?名字`),
		regexp.MustCompile(`(?i)(?:你|助手|机器人|AI)(?:就是|叫|是)\s*[A-Za-z\p{Han}]`),
		regexp.MustCompile(`(?i)我(?:叫|的名字叫|的名字是)`),
		regexp.MustCompile(`(?i)^(?:我(?:叫|是))\s*[A-Za-z\p{Han}]`),
		regexp.MustCompile(`(?:我是|我叫)\s*[A-Za-z\p{Han}]{2,}`),
	}
	signalLocationPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?:来自|住在|住|在)\s*[A-Za-z\p{Han}]{2,}`),
		regexp.MustCompile(`(?:我的)?(?:家乡|籍贯|出生地)`),
	}
	signalLanguagePatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(?:用|请用|使用|讲|说)\s*[A-Za-z\p{Han}]+(?:回答|交流|沟通|回复)`),
		regexp.MustCompile(`(?:我[会讲会]|我会)\s*[A-Za-z\p{Han}]{2,}`),
		regexp.MustCompile(`(?:语言|母语|外语)`),
	}
	signalPreferencePatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?:我喜欢|我讨厌|我偏好|我不喜欢|我习惯|不要|请别)`),
		regexp.MustCompile(`(?:回答|回复|输出).{0,15}(?:简洁|详细|简短|正式|随意|礼貌|专业)`),
	}
	signalFactPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?:记住|提醒我|备忘|记下来)`),
		regexp.MustCompile(`(?:重要|关键|注意)`),
	}
)

// detectSignals returns the set of signals triggered by user text.
// Returns nil if the input looks like a question or has no memorable info.
func detectSignals(text string) []SignalKind {
	t := strings.TrimSpace(text)
	if t == "" {
		return nil
	}
	if looksLikePureQuestion(t) {
		return nil
	}

	matched := func(pats []*regexp.Regexp) bool {
		for _, p := range pats {
			if p.MatchString(t) {
				return true
			}
		}
		return false
	}

	var sigs []SignalKind
	if matched(signalNamePatterns) {
		sigs = append(sigs, SignalName)
	}
	if matched(signalLocationPatterns) {
		sigs = append(sigs, SignalLocation)
	}
	if matched(signalLanguagePatterns) {
		sigs = append(sigs, SignalLanguage)
	}
	if matched(signalPreferencePatterns) {
		sigs = append(sigs, SignalPreference)
	}
	if matched(signalFactPatterns) {
		sigs = append(sigs, SignalFact)
	}
	return sigs
}

// looksLikePureQuestion returns true when the input is overwhelmingly a
// question (ends with ?/?/吗, or starts with typical question words, or
// has an "<X>是<question-word>" structure like "你是谁").
// In those cases we skip extraction entirely because there's nothing to
// remember about a question.
func looksLikePureQuestion(text string) bool {
	t := strings.TrimSpace(text)
	if strings.HasSuffix(t, "?") || strings.HasSuffix(t, "？") {
		return true
	}
	for _, q := range []string{"谁是", "什么是", "哪个是", "为什么", "怎么", "如何", "能否", "可否", "是不是", "请问"} {
		if strings.HasPrefix(t, q) {
			return true
		}
	}
	// "<subject>是/叫/有<question-word>" form: 你是谁 / 你叫什么 / 我有什么
	questionPronouns := []string{"谁", "什么", "哪", "哪儿", "哪里", "怎么", "咋", "为何", "为什么", "几", "多少"}
	for _, p := range questionPronouns {
		if strings.Contains(t, p) && containsAny(t, []string{"是", "叫", "有", "在"}) {
			// Heuristic: if the sentence ends without punctuation and has
			// no comma+declarative content, treat as a question.
			if !strings.HasSuffix(t, "。") && !strings.HasSuffix(t, ".") {
				return true
			}
		}
	}
	return false
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// ExtractFromNaturalLanguage is retained for backwards compatibility with
// existing tests. It now returns no triggers directly — callers must use
// ProcessUserInput, which combines signal detection with LLM extraction.
func ExtractFromNaturalLanguage(text string) []Trigger {
	return nil
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
	sb.WriteString("【什么算「有价值的事实」——严格筛选】\n")
	sb.WriteString("✅ 值得记住：\n")
	sb.WriteString("- 用户稳定身份/属性：姓名、所在地、长期职业、长期偏好\n")
	sb.WriteString("- 用户明确表达的长期偏好（\"以后都用 X\"、\"我不喜欢 Y\"、\"回答简短点\"）\n")
	sb.WriteString("- 用户为 AI 设定的稳定属性：\"你叫 X\"、\"你风格 Y\"\n")
	sb.WriteString("- 长期有用的关键事实/已做决策（影响后续多次对话）\n\n")
	sb.WriteString("❌ 不要记住：\n")
	sb.WriteString("- 寒暄/客套（\"你好\"、\"今天天气不错\"、\"我先睡了\"）\n")
	sb.WriteString("- 临时状态（\"我现在在调试 X\"、\"我今天心情不好\"）\n")
	sb.WriteString("- 一次性事件（\"我刚买了 Y\"、\"刚刚发生 Z\"）\n")
	sb.WriteString("- 当前任务细节（\"我在改这个 bug\"、\"我正在写 XX 文件\"）\n")
	sb.WriteString("- 模糊/无法验证的内容（\"我大概 30 左右\"）\n")
	sb.WriteString("- 对话噪声（\"好的\"、\"嗯\"、\"继续\"）\n\n")
	sb.WriteString("【允许的 KEY 白名单】\n")
	sb.WriteString("- user.name: 用户姓名（稳定身份）\n")
	sb.WriteString("- user.location: 所在地\n")
	sb.WriteString("- user.preferred_language: 语言偏好\n")
	sb.WriteString("- user.preferred_response_style: 回答风格\n")
	sb.WriteString("- user.likes / user.dislikes: 长期喜欢/讨厌\n")
	sb.WriteString("- assistant.name: AI 名字\n")
	sb.WriteString("- assistant.personality: AI 人设风格\n")
	sb.WriteString("- project.tech_stack: 项目长期技术栈\n")
	sb.WriteString("- project.client: 项目客户\n")
	sb.WriteString("- fact.<具体>: 长期有用的关键事实\n")
	sb.WriteString("- decision.<具体>: 已做决策\n")
	sb.WriteString("- constraint.<具体>: 长期约束\n\n")
	sb.WriteString("【严格禁止】\n")
	sb.WriteString("- 不要输出白名单之外的 key。\n")
	sb.WriteString("- 不要输出客套话或解释。\n")
	sb.WriteString("- 宁可漏记，不要乱记：拿不准就 NONE。\n\n")
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

// LLMSignalExtractor extracts structured facts from a single user input,
// guided by the signal kinds produced by the regex-based detector.
//
// This pairs with the signal-detection layer: regex says "this input
// probably contains a name/location/preference", and this extractor asks
// the LLM to read the input and produce clean key=value pairs.
type LLMSignalExtractor struct {
	SummarizeFunc func(ctx context.Context, prompt string) (string, error)
}

// ExtractFromText runs the LLM on the user text + detected signals and
// returns parsed triggers. Returns an empty slice if no signals were given.
func (e *LLMSignalExtractor) ExtractFromText(ctx context.Context, text string, signals []SignalKind) ([]Trigger, error) {
	if e.SummarizeFunc == nil {
		return nil, fmt.Errorf("SummarizeFunc not set")
	}
	if len(signals) == 0 {
		return nil, nil
	}

	prompt := buildSignalPrompt(text, signals)

	response, err := e.SummarizeFunc(ctx, prompt)
	if err != nil {
		return nil, err
	}
	return parseExtractionResult(response, SourceLLMExtract), nil
}

func buildSignalPrompt(text string, signals []SignalKind) string {
	var sb strings.Builder
	sb.WriteString("你是记忆提取助手。下面是一句用户输入，规则系统已检测到它可能包含以下类型的信息：\n")
	for _, s := range signals {
		sb.WriteString("- ")
		sb.WriteString(signalHint(string(s)))
		sb.WriteString("\n")
	}
	sb.WriteString("\n【任务】阅读用户输入，判断是否真的包含值得**长期记住**的事实。\n")
	sb.WriteString("如果有，按 `KEY=VALUE` 每行一条输出；如果只是疑问句、寒暄、或者不值得记住，输出 `NONE`。\n\n")
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
	sb.WriteString("- 当前任务细节（\"我在改这个 bug\"、\"我正在写 XX 文件\"）——这些 task.current 之外的临时进度都没用\n")
	sb.WriteString("- 模糊/无法验证的内容（\"我大概 30 左右\"）\n")
	sb.WriteString("- 对话噪声（\"好的\"、\"嗯\"、\"继续\"）\n\n")
	sb.WriteString("【允许的 KEY 白名单】\n")
	sb.WriteString("- user.name: 用户姓名（稳定身份）\n")
	sb.WriteString("- user.location: 所在地\n")
	sb.WriteString("- user.preferred_language: 语言偏好\n")
	sb.WriteString("- user.preferred_response_style: 回答风格\n")
	sb.WriteString("- user.likes / user.dislikes: 长期喜欢/讨厌\n")
	sb.WriteString("- assistant.name: AI 名字\n")
	sb.WriteString("- assistant.personality: AI 人设风格\n")
	sb.WriteString("- fact.<具体>: 长期有用的关键事实\n")
	sb.WriteString("- decision.<具体>: 已做决策\n\n")
	sb.WriteString("【严格禁止】\n")
	sb.WriteString("- 不要把疑问句当成陈述（例如 \"你是谁\" 不要输出 assistant.name）。\n")
	sb.WriteString("- 不要输出白名单之外的 key。\n")
	sb.WriteString("- 不要编造信息；用户没说就输出 NONE。\n")
	sb.WriteString("- 不要输出客套话或解释。\n")
	sb.WriteString("- 宁可漏记，不要乱记：拿不准就 NONE。\n\n")
	sb.WriteString("【用户输入】\n")
	sb.WriteString(text)
	sb.WriteString("\n\n【输出】\n")
	return sb.String()
}

func signalHint(kind string) string {
	switch kind {
	case string(SignalName):
		return "name (用户或助手名字)"
	case string(SignalLocation):
		return "location (所在地/家乡)"
	case string(SignalLanguage):
		return "language (语言偏好)"
	case string(SignalPreference):
		return "preference (回答风格/喜欢/讨厌)"
	case string(SignalFact):
		return "fact (任何其他值得长期记住的事实)"
	default:
		return kind
	}
}

// allowedKeyPrefixes is the whitelist for LLM-extracted keys.
// Note: task.* is intentionally excluded — current task is transient and
// does not belong in long-term memory.
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

// transientValueKeywords — values starting with these are time-bound and
// not worth remembering long-term. Reject them at the parse layer so the
// LLM can't sneak them past the prompt rules.
var transientValueKeywords = []string{
	"今天", "明天", "昨天", "刚才", "刚刚", "现在", "目前", "此刻",
	"马上", "立刻", "今晚", "今早", "今明", "周一", "周二", "周三", "周四", "周五", "周六", "周日",
	"早上", "中午", "下午", "晚上", "凌晨",
}

// politeValueKeywords — pure politeness/acknowledgement should never be
// stored as facts.
var politeValueKeywords = []string{
	"你好", "您好", "谢谢", "感谢", "好的", "嗯", "是的", "不是", "对的",
	"再见", "拜拜", "ok", "OK", "Ok",
}

// valuableMinLen is the minimum length a fact value must have. One-char or
// pure-punctuation values are noise.
const valuableMinLen = 2

// isValuableValue returns true if the extracted value is worth keeping.
// Filters question marks, transient temporals, pure politeness, and noise.
func isValuableValue(value string) bool {
	v := strings.TrimSpace(value)
	if utf8.RuneCountInString(v) < valuableMinLen {
		return false
	}
	// Question marks in value means LLM captured a question, not a fact.
	if strings.ContainsAny(v, "?？") {
		return false
	}
	// Two transient checks:
	//  (a) Leading transient temporal: "今天心情不好" / "刚才买了 Y"
	//  (b) Personal-pronoun prefix + transient keyword inside:
	//      "我现在在调试" / "我们马上开会" — almost always ephemeral state.
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
	// Pure politeness.
	for _, kw := range politeValueKeywords {
		if v == kw {
			return false
		}
	}
	return true
}

// startsWithPersonalPronoun returns true if v starts with 我 / 我们 /
// 你 / 你们 / 他 etc. Used to expand the transient filter to catch
// sentences like "我现在在调试 X" where the transient temporal is in
// the middle rather than at the head.
func startsWithPersonalPronoun(v string) bool {
	pronouns := []string{"我", "我们", "你", "你们", "他", "她", "他们", "她们", "它"}
	for _, p := range pronouns {
		if strings.HasPrefix(v, p) {
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
		// Quality gate: skip noise, transient, polite, and question-form
		// values. Only LLM-sourced output goes through this filter;
		// explicit markers (e.g. "[remember:user.name=小明]") are honored
		// verbatim because the user is asserting intent.
		if source == SourceLLMExtract && !isValuableValue(value) {
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

// --- Main flow ---

// ProcessUserInput processes user input and saves extracted memories.
// Returns the number of triggers applied.
//
// Flow:
//  1. Parse explicit markers like [remember:key=value] — apply directly.
//  2. Run regex-based signal detection on the remaining text.
//  3. If at least one signal fires AND a signal extractor is configured,
//     call the LLM to read the input and produce clean key=value pairs.
//  4. Apply resulting triggers.
func (a *AutoLearner) ProcessUserInput(text string) int {
	return a.ProcessUserInputCtx(context.Background(), text)
}

// ProcessUserInputCtx is the context-aware variant of ProcessUserInput,
// useful when the caller needs to plumb cancellation/timeouts into the
// LLM extraction step.
func (a *AutoLearner) ProcessUserInputCtx(ctx context.Context, text string) int {
	if a.mem == nil {
		return 0
	}

	// 1. Explicit markers are applied synchronously and never go through
	// the LLM — the user is asserting intent.
	explicit := ExtractFromUserInput(text)
	count := a.apply(explicit)

	// 2. Signal detection: regex only decides WHETHER to extract, never WHAT.
	signals := detectSignals(text)
	if len(signals) == 0 {
		return count
	}

	a.mu.Lock()
	ext := a.signalExtractor
	a.mu.Unlock()
	if ext == nil {
		// No LLM available; the signal layer has no fallback path.
		return count
	}

	// 3. Ask the LLM to read the text and produce clean key=value pairs.
	triggers, err := ext.ExtractFromText(ctx, text, signals)
	if err != nil {
		return count
	}
	count += a.apply(triggers)
	return count
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

// MaybeExtractWorkflow 异步检查是否需要 LLM 提取工作流。
//
// 优先调用 ExtractSkillMd（按 skill-writer 规范直接生成完整 SKILL.md），
// 回退到 Extract（结构化提取 → 模板渲染）。
// 返回实际写入的 skill 数量。
func (a *AutoLearner) MaybeExtractWorkflow(ctx context.Context, messages []core.Message, extractor *WorkflowExtractor) int {
	a.mu.Lock()
	dir := a.WorkflowDir
	a.mu.Unlock()
	if !a.settings.AutoLearn || extractor == nil || dir == "" {
		return 0
	}

	// 路径 1：直接生成符合 skill-writer 规范的完整 SKILL.md
	if extractor.SkillWriterDoc != "" {
		contents, err := extractor.ExtractSkillMd(ctx, messages)
		if err == nil && len(contents) > 0 {
			count := 0
			for _, content := range contents {
				name := ExtractSkillName(content)
				if name == "" {
					continue
				}
				subdir := filepath.Join(dir, name)
				if err := os.MkdirAll(subdir, 0755); err != nil {
					continue
				}
				path := filepath.Join(subdir, "SKILL.md")
				if err := os.WriteFile(path, []byte(content), 0644); err != nil {
					continue
				}
				count++
			}
			return count
		}
		// 直出失败时也回退到路径 2
	}

	// 路径 2（回退）：结构化提取 → 模板渲染
	skills, err := extractor.Extract(ctx, messages)
	if err != nil || len(skills) == 0 {
		return 0
	}

	count := 0
	for _, s := range skills {
		if _, err := s.WriteSKILLMd(dir); err != nil {
			continue
		}
		count++
	}
	return count
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
