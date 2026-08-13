package defaults

// 工作流自动提取：从对话里识别可复用的工作流，并以 SKILL.md 形式落地。

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	core "github.com/hycjack/crux-ai/core"
)

// Skill 表示从对话中自动提取的可复用工作流。
type Skill struct {
	Name        string    // skill 名称（kebab-case）
	Trigger     string    // 触发场景描述
	Description string    // 简短描述（≤200 字符）
	Steps       []string  // 分步操作列表
	Output      string    // 预期输出
	Source      string    // 来源对话摘要
	CreatedAt   time.Time // 创建时间
}

var vagueStepVerbs = []string{
	"确认", "检查", "看看", "查看", "观察", "考虑", "思考", "想想",
	"判断", "评估", "分析", "探讨", "研究", "尝试", "试试",
	"决定", "尽量", "设法", "小心", "注意", "提醒",
	"询问", "问", "沟通", "确认是否", "检查是否",
}

var singleShotTriggers = []string{
	"修这个", "改这个", "解决这个", "修复这个",
	"这个 bug", "这个错误", "这个报错",
	"回答这个", "帮我写这个", "这个 PR",
	"这一个", "本对话", "这次",
}

func isConcreteStep(step string) bool {
	s := strings.TrimSpace(step)
	if s == "" {
		return false
	}
	for _, v := range vagueStepVerbs {
		if strings.HasPrefix(s, v) {
			return false
		}
	}
	return true
}

func isReusableTrigger(trigger string) bool {
	t := strings.TrimSpace(trigger)
	if t == "" {
		return false
	}
	for _, bad := range singleShotTriggers {
		if strings.Contains(t, bad) {
			return false
		}
	}
	return true
}

// extractBlock extracts the first block delimited by startMark/endMark,
// returning its trimmed inner content and the index just past endMark.
// Returns ok=false if the delimiter pair is not found.
func extractBlock(s, startMark, endMark string) (inner string, next int, ok bool) {
	start := strings.Index(s, startMark)
	if start < 0 {
		return "", 0, false
	}
	after := s[start+len(startMark):]
	end := strings.Index(after, endMark)
	if end < 0 {
		return "", 0, false
	}
	return strings.TrimSpace(after[:end]), start + len(startMark) + end + len(endMark), true
}

func parseWorkflowBlocks(response string) []Skill {
	var skills []Skill
	rest := response
	for {
		body, next, ok := extractBlock(rest, "WORKFLOW_START", "WORKFLOW_END")
		if !ok {
			break
		}
		skill := parseWorkflowBody(body)
		if skill.Name != "" && skill.Trigger != "" && len(skill.Steps) >= 3 && isReusableTrigger(skill.Trigger) {
			skills = append(skills, skill)
		}
		rest = rest[next:]
	}
	return skills
}

func parseWorkflowBody(body string) Skill {
	skill := Skill{CreatedAt: time.Now()}
	var steps []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "NAME:"):
			skill.Name = sanitizeName(strings.TrimSpace(strings.TrimPrefix(line, "NAME:")))
		case strings.HasPrefix(line, "TRIGGER:"):
			skill.Trigger = strings.TrimSpace(strings.TrimPrefix(line, "TRIGGER:"))
		case strings.HasPrefix(line, "DESCRIPTION:"):
			skill.Description = strings.TrimSpace(strings.TrimPrefix(line, "DESCRIPTION:"))
		case strings.HasPrefix(line, "STEP:"):
			step := strings.TrimSpace(strings.TrimPrefix(line, "STEP:"))
			if step != "" && isConcreteStep(step) {
				steps = append(steps, step)
			}
		case strings.HasPrefix(line, "OUTPUT:"):
			skill.Output = strings.TrimSpace(strings.TrimPrefix(line, "OUTPUT:"))
		case strings.HasPrefix(line, "SOURCE:"):
			skill.Source = strings.TrimSpace(strings.TrimPrefix(line, "SOURCE:"))
		}
	}
	skill.Steps = steps
	return skill
}

// sanitizeName 把 skill 名规范化为 kebab-case（无正则）。
func sanitizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var sb strings.Builder
	prevDash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			sb.WriteRune(r)
			prevDash = false
		} else if !prevDash && sb.Len() > 0 {
			sb.WriteByte('-')
			prevDash = true
		}
	}
	out := strings.Trim(sb.String(), "-_")
	if len(out) > 60 {
		out = out[:60]
	}
	return out
}

func (s Skill) RenderSKILLMd() string {
	var sb strings.Builder
	sb.WriteString("---\n")
	fmt.Fprintf(&sb, "name: %s\n", s.Name)
	fmt.Fprintf(&sb, "description: %s\n", s.buildDescription())
	sb.WriteString("---\n\n")
	fmt.Fprintf(&sb, "# %s\n\n", s.Name)

	trigger := strings.TrimRight(s.Trigger, ".。")
	if trigger != "" {
		fmt.Fprintf(&sb, "> **Use when** the user %s\n\n", trigger)
	}
	if s.Description != "" {
		fmt.Fprintf(&sb, "## 概述\n\n%s\n\n", s.Description)
	}
	if len(s.Steps) > 0 {
		sb.WriteString("## 步骤\n\n")
		for i, step := range s.Steps {
			fmt.Fprintf(&sb, "%d. %s\n", i+1, step)
		}
		sb.WriteString("\n")
	}
	if s.Output != "" {
		fmt.Fprintf(&sb, "## 输出\n\n%s\n\n", s.Output)
	}
	if s.Source != "" {
		fmt.Fprintf(&sb, "---\n\n> 来源：%s\n", s.Source)
	}
	return sb.String()
}

func (s Skill) buildDescription() string {
	core := s.Description
	if core == "" {
		core = s.Trigger
	}
	if core == "" {
		core = "Auto-extracted workflow"
	}
	core = strings.TrimSpace(core)
	trigger := strings.TrimRight(s.Trigger, ".。")
	if trigger == "" {
		trigger = "asks for this kind of task"
	}
	return escapeYamlString(fmt.Sprintf("%s. Use when the user %s.", core, trigger))
}

func escapeYamlString(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\t", "\\t")
	return "\"" + s + "\""
}

func (s Skill) WriteSKILLMd(baseDir string) (string, error) {
	if s.Name == "" {
		return "", fmt.Errorf("workflow: skill name is empty")
	}
	if baseDir == "" {
		return "", fmt.Errorf("workflow: baseDir is empty")
	}
	dir := filepath.Join(baseDir, s.Name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "SKILL.md")
	content := s.RenderSKILLMd()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", err
	}
	return path, nil
}

func buildWorkflowExtractionPrompt() string {
	return `你是 SOP（标准操作流程）提取助手。请分析下面的对话，判断**是否体现了一个能写成 SOP 的可复用工作流**。

【什么样的对话需要提取 workflow】
- 用户在一次对话中执行了多步操作（≥3 步）形成完整流程
- 用户明确说"以后都这样做"、"记住这个流程"、"每次 X 都要 Y"等
- 用户在多个轮次中重复了类似的步骤序列
- 一次对话内解决了某种结构化问题（如部署、PR review、错误排查、构建发布）

【SOP 质量门槛——必须同时满足】
1. **可复用**：能在未来的多种相似场景下重复使用，不是只解决"这一个 bug"。
2. **步骤具体**：每一步都是一个**可执行的具体动作**（祈使语气），不是"考虑 X"、"评估 Y"、"必要时 Z"这类含糊步骤。
   - ✅ 好步骤："运行 npm run build"、"打开 /etc/hosts 添加一行 127.0.0.1 api.local"、"运行 go test ./..."
   - ❌ 坏步骤："确认环境正常"、"仔细思考"、"看看日志"、"尝试修复"
3. **步骤确定性**：每一步的输入/产出可预期，不依赖个人判断。
4. **可验证产出**：存在明确可验证的最终输出（"测试通过"、"PR 创建成功"、"部署完成且健康检查 200"）。

【输出格式】
- 如果**有**可提取的 SOP 工作流，按下面格式输出（**只输出一次 WORKFLOW 块**）：
  WORKFLOW_START
  NAME: <kebab-case 名称，如 test-driven-go>
  TRIGGER: <触发场景，1 句话，明确指出"在什么情况下使用本流程">
  DESCRIPTION: <简短描述，≤100 字符>
  STEP: <步骤 1：以动词开头的具体动作，含必要命令/路径/参数>
  STEP: <步骤 2：同上>
  STEP: <步骤 3：同上>
  OUTPUT: <预期产出，含可验证标准>
  SOURCE: <来源说明，如"用户在 3 轮对话中重复此流程">
  WORKFLOW_END

- 如果**没有**可提取的 SOP → 单独输出 NOWORKFLOW。

【严格要求】
- NAME 必须用 kebab-case（只含小写字母、数字、横线）
- 步骤数量 ≥ 3，每一步都必须以动词开头且是具体动作
- 不接受"修这个 bug"、"回答用户 X"这类一次性、不具复用性的工作流
- 不接受"询问用户偏好"、"判断是否..."这类依赖判断的步骤
- 不要输出 WORKFLOW_START 之外的内容
- 不要重复提取同名的 workflow

对话：
`
}

func buildSkillWriterPrompt(skillWriterDoc string) string {
	var sb strings.Builder
	sb.WriteString(skillWriterPromptHeader)
	sb.WriteString(skillWriterDoc)
	sb.WriteString("\n```\n\n")
	sb.WriteString(skillWriterPromptBody)
	return sb.String()
}

const skillWriterPromptHeader = `你是 SOP skill 自动生成器。请按 ` + "`skill-writer`" + ` 规范，从对话中识别**能写成 SOP 的可复用工作流**，
并直接生成符合规范的完整 SKILL.md 内容。

【SOP 质量门槛——必须同时满足】
1. **可复用**：能在未来多种相似场景下重复使用，不只是解决某一个具体 bug。
2. **步骤具体**：每一步都是可执行的具体动作（祈使语气），不是含糊的「考虑 X」「判断 Y」「必要时 Z」。
   - ✅ 好步骤：运行 npm run build / git add -A && git commit / go test ./...
   - ❌ 坏步骤：确认环境正常 / 仔细思考 / 看看日志 / 尝试修复
3. **确定性**：步骤不依赖个人判断，相同输入产生相同动作。
4. **可验证产出**：有明确可验证的最终输出（测试通过、PR 创建成功、健康检查 200 等）。

【参考规范：skill-writer 的核心要求】
` + "```\n"

const skillWriterPromptBody = `
【你的任务】
1. 阅读下面的对话，判断是否包含**符合 SOP 门槛**的工作流
2. 如果**没有** → 单独输出 NOWORKFLOW
3. 如果**有** → 按 skill-writer 规范生成**完整 SKILL.md 内容**，输出格式：

   SKILL_START
   <完整的 SKILL.md 内容，包含 YAML frontmatter、imperative 步骤、trigger-rich description>
   SKILL_END

【SKILL.md 必须包含】
- YAML frontmatter: name (kebab-case) + description (trigger-rich, 包含 "Use when" 子句)
- title (# name)
- 触发条件（blockquote 格式，明确说明在什么场景使用本 SOP）
- 步骤（编号清单，每步以动词开头、包含必要命令/路径/参数，是可执行的具体动作）
- 输出（预期产物，含可验证标准）
- 在文末用一段引用文本标注 ` + "`> Auto-generated by autolearn from conversation`" + `

【严格拒绝】
- 一次性、不具复用性的流程（修这个特定的 bug、回答这个具体问题）
- 依赖判断的步骤（评估是否需要...、询问用户偏好...）
- 含糊步骤（确认 X、看看 Y、尝试 Z）
- 步骤少于 3 个

【其他要求】
- 不要修改或评论参考规范
- SKILL.md 内容必须自包含、可直接写入文件
- 不要在 SKILL_START/END 之外输出解释

对话：
`

func parseSkillMdBlocks(response string) []string {
	var out []string
	rest := response
	for {
		inner, next, ok := extractBlock(rest, "SKILL_START", "SKILL_END")
		if !ok {
			break
		}
		if inner != "" {
			out = append(out, inner)
		}
		rest = rest[next:]
	}
	return out
}

// WorkflowExtractor 独立的工作流提取器。
type WorkflowExtractor struct {
	// SummarizeFunc 调用 LLM 同步获取响应。由调用方注入。
	SummarizeFunc func(ctx context.Context, prompt string) (string, error)

	// SkillWriterDoc skill-writer 的 SKILL.md 完整内容。
	// 加载后，ExtractSkillMd 会用它作为参考规范，让 LLM 直接输出符合
	// skill-writer 标准的 SKILL.md。如果为空，则回退到结构化提取。
	SkillWriterDoc string
}

// Extract 提取 0~N 个结构化 Skill（回退路径，LLM 输出 WORKFLOW_START 块）。
func (e *WorkflowExtractor) Extract(ctx context.Context, messages []core.Message) ([]Skill, error) {
	if e.SummarizeFunc == nil {
		return nil, fmt.Errorf("workflow: SummarizeFunc not set")
	}
	var sb strings.Builder
	sb.WriteString(buildWorkflowExtractionPrompt())
	appendWorkflowMessages(&sb, messages)
	response, err := e.SummarizeFunc(ctx, sb.String())
	if err != nil {
		return nil, err
	}
	return parseWorkflowBlocks(response), nil
}

// ExtractSkillMd 让 LLM 直接生成完整的 SKILL.md 内容（按 skill-writer 规范）。
func (e *WorkflowExtractor) ExtractSkillMd(ctx context.Context, messages []core.Message) ([]string, error) {
	if e.SummarizeFunc == nil {
		return nil, fmt.Errorf("workflow: SummarizeFunc not set")
	}
	if e.SkillWriterDoc == "" {
		return nil, fmt.Errorf("workflow: SkillWriterDoc not set; cannot use ExtractSkillMd")
	}
	var sb strings.Builder
	sb.WriteString(buildSkillWriterPrompt(e.SkillWriterDoc))
	appendWorkflowMessages(&sb, messages)
	response, err := e.SummarizeFunc(ctx, sb.String())
	if err != nil {
		return nil, err
	}
	return parseSkillMdBlocks(response), nil
}

// MaybeExtractWorkflow writes any extracted workflows as SKILL.md files into
// dir, preferring the skill-writer path when SkillWriterDoc is set, and
// falling back to structured extraction. Returns the number written.
func (e *WorkflowExtractor) MaybeExtractWorkflow(ctx context.Context, messages []core.Message, dir string) int {
	if e.SummarizeFunc == nil || dir == "" {
		return 0
	}
	if e.SkillWriterDoc != "" {
		contents, err := e.ExtractSkillMd(ctx, messages)
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
	}
	skills, err := e.Extract(ctx, messages)
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

func appendWorkflowMessages(sb *strings.Builder, messages []core.Message) {
	for _, msg := range messages {
		switch m := msg.(type) {
		case core.UserMessage:
			fmt.Fprintf(sb, "用户: %v\n", m.Content)
		case core.AssistantMessage:
			var text string
			for _, b := range m.Content {
				if c, ok := b.(core.TextContent); ok {
					text += c.Text
				}
			}
			fmt.Fprintf(sb, "助手: %s\n", text)
		}
	}
}

// ExtractSkillName 从完整 SKILL.md 内容中提取 frontmatter 的 name 字段。
func ExtractSkillName(skillMd string) string {
	for _, line := range strings.Split(skillMd, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "name:") {
			continue
		}
		name := strings.TrimSpace(strings.TrimPrefix(line, "name:"))
		if name == "" {
			return ""
		}
		return sanitizeName(name)
	}
	return ""
}
