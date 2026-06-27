package autolearn

// 工作流自动提取：从对话里识别可复用的工作流，并以 SKILL.md 形式落地。
//
// 两路输出策略：
//  1. 结构性提取：让 LLM 输出 `WORKFLOW_START ... WORKFLOW_END` 块，按字段
//     解析为 Skill 结构体，再用模板渲染为 SKILL.md。
//  2. 直出式提取：把 skill-writer/SKILL.md 完整内容作为参考规范塞给 LLM，
//     让 LLM 直接输出符合规范的完整 SKILL.md（包在 SKILL_START/END 块里）。

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/hycjack/crux-ai/core"
)

// Skill 表示从对话中自动提取的可复用工作流。
//
// 完整 SKILL.md 由 LLM 直接生成（遵循 skill-writer 规范），本结构体只保存
// 结构化的中间结果（用于回退渲染）。
type Skill struct {
	Name        string    // skill 名称（kebab-case）
	Trigger     string    // 触发场景描述
	Description string    // 简短描述（≤200 字符）
	Steps       []string  // 分步操作列表
	Output      string    // 预期输出
	Source      string    // 来源对话摘要
	CreatedAt   time.Time // 创建时间
}

// workflowBlockRegex 匹配 LLM 输出的 WORKFLOW 块。
//
// 格式示例：
//
//	WORKFLOW_START
//	NAME: test-driven-go
//	TRIGGER: 用户要求写 Go 函数时
//	DESCRIPTION: 强制 TDD 流程
//	STEP: 定义函数签名
//	STEP: 写失败测试
//	STEP: 实现函数直到测试通过
//	OUTPUT: 通过测试的 Go 代码
//	SOURCE: 用户在 3 次对话中都先写测试再实现
//	WORKFLOW_END
var workflowBlockRegex = regexp.MustCompile(`(?s)WORKFLOW_START\s*\n(.*?)\n\s*WORKFLOW_END`)

// vagueStepVerbs — steps starting with these verbs are SOP-incompatible.
// They depend on judgement or describe exploration rather than concrete
// action. Even if the LLM produces a STEP line, we drop it here as a
// safety net.
var vagueStepVerbs = []string{
	"确认", "检查", "看看", "查看", "观察", "考虑", "思考", "想想",
	"判断", "评估", "分析", "探讨", "研究", "尝试", "试试",
	"决定", "尽量", "设法", "小心", "注意", "提醒",
	"询问", "问", "沟通", "确认是否", "检查是否",
}

// singleShotTriggers — trigger phrases that imply a one-shot fix to a
// specific issue, not a reusable SOP. Examples: "fix this bug",
// "answer this question".
var singleShotTriggers = []string{
	"修这个", "改这个", "解决这个", "修复这个",
	"这个 bug", "这个错误", "这个报错",
	"回答这个", "帮我写这个", "这个 PR",
	"这一个", "本对话", "这次",
}

// isConcreteStep returns true if the step is a concrete, executable SOP
// action. Rejects steps that start with vague judgement verbs.
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

// isReusableTrigger returns true if the trigger describes a reusable
// scenario, not a one-shot fix.
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

// parseWorkflowBlocks 从 LLM 输出解析 WORKFLOW 块。
// Applies SOP quality gates: drops skills whose TRIGGER is single-shot
// ("修这个 bug") and drops vague-judgement steps from remaining skills.
func parseWorkflowBlocks(response string) []Skill {
	var skills []Skill
	matches := workflowBlockRegex.FindAllStringSubmatch(response, -1)
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		body := m[1]
		skill := Skill{CreatedAt: time.Now()}
		var steps []string
		for _, line := range strings.Split(body, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
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
		// 校验：必须至少有 NAME、TRIGGER、≥3 个具体 STEP，否则视为无效块。
		if skill.Name == "" || skill.Trigger == "" || len(skill.Steps) < 3 {
			continue
		}
		// SOP 门槛：trigger 必须是可复用场景，不能是单次修复。
		if !isReusableTrigger(skill.Trigger) {
			continue
		}
		skills = append(skills, skill)
	}
	return skills
}

// sanitizeName 把 skill 名规范化为 kebab-case。
func sanitizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	reg := regexp.MustCompile(`[^a-z0-9\-_]+`)
	s = reg.ReplaceAllString(s, "-")
	reg2 := regexp.MustCompile(`-+`)
	s = reg2.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-_")
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}

// RenderSKILLMd 把结构化 Skill 渲染为 SKILL.md 内容。
//
// 这是回退路径：模板简单但符合基本规范。优先使用 WorkflowExtractor.ExtractSkillMd
// （让 LLM 按 skill-writer 规范直接生成完整 SKILL.md）。
func (s Skill) RenderSKILLMd() string {
	var sb strings.Builder

	sb.WriteString("---\n")
	fmt.Fprintf(&sb, "name: %s\n", s.Name)
	fmt.Fprintf(&sb, "description: %s\n", s.buildDescription())
	sb.WriteString("---\n\n")

	fmt.Fprintf(&sb, "# %s\n\n", s.Name)

	// 触发场景
	trigger := strings.TrimRight(s.Trigger, ".。")
	if trigger != "" {
		fmt.Fprintf(&sb, "> **Use when** the user %s\n\n", trigger)
	}

	// 描述
	if s.Description != "" {
		fmt.Fprintf(&sb, "## 概述\n\n%s\n\n", s.Description)
	}

	// 步骤（编号清单，祈使语气）
	if len(s.Steps) > 0 {
		sb.WriteString("## 步骤\n\n")
		for i, step := range s.Steps {
			fmt.Fprintf(&sb, "%d. %s\n", i+1, step)
		}
		sb.WriteString("\n")
	}

	// 输出
	if s.Output != "" {
		fmt.Fprintf(&sb, "## 输出\n\n%s\n\n", s.Output)
	}

	// 来源
	if s.Source != "" {
		fmt.Fprintf(&sb, "---\n\n> 来源：%s\n", s.Source)
	}

	return sb.String()
}

// buildDescription 构造 frontmatter 中的 trigger-rich description。
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

// escapeYamlString 把字符串安全地包成 YAML 双引号字符串。
func escapeYamlString(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\t", "\\t")
	return "\"" + s + "\""
}

// WriteSKILLMd 把 skill 写入目录 baseDir/<name>/SKILL.md。
// 如果目录已存在同名 skill，默认覆盖（用户后续可手工调整）。
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

// --- LLM 提取器 ---

// buildWorkflowExtractionPrompt 构造从对话中提取 workflow 的 prompt。
// 独立于事实提取，让 LLM 单独输出 WORKFLOW 块。
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

// buildSkillWriterPrompt 构造"按 skill-writer 规范生成完整 SKILL.md"的 prompt。
// 这是更高级的路径：先检测可复用 workflow，然后用 skill-writer 规范直接生成完整 SKILL.md。
//
// skillWriterDoc 参数是 skill-writer/SKILL.md 的完整内容，作为参考规范。
func buildSkillWriterPrompt(skillWriterDoc string) string {
	var sb strings.Builder
	sb.WriteString(skillWriterPromptHeader)
	sb.WriteString(skillWriterDoc)
	sb.WriteString("\n```\n\n")
	sb.WriteString(skillWriterPromptBody)
	return sb.String()
}

// skillWriterPromptHeader / skillWriterPromptBody 用 raw string 拆分，
// 避免在 sb.WriteString 里嵌套 ASCII 双引号引发解析问题。
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

// skillMdBlockRegex 匹配 LLM 输出的完整 SKILL.md 块。
var skillMdBlockRegex = regexp.MustCompile(`(?s)SKILL_START\s*\n(.*?)\n\s*SKILL_END`)

// parseSkillMdBlocks 从 LLM 输出解析 SKILL_START...SKILL_END 块。
// 返回 0~N 个完整 SKILL.md 内容字符串。
func parseSkillMdBlocks(response string) []string {
	matches := skillMdBlockRegex.FindAllStringSubmatch(response, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		s := strings.TrimSpace(m[1])
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// WorkflowExtractor 独立的工作流提取器。
// 行为与 LLMSimpleExtractor 类似，但专注于识别可复用工作流而非事实。
type WorkflowExtractor struct {
	// SummarizeFunc 调用 LLM 同步获取响应。由调用方注入。
	SummarizeFunc func(ctx context.Context, prompt string) (string, error)

	// SkillWriterDoc skill-writer 的 SKILL.md 完整内容。
	// 加载后，ExtractSkillMd 会用它作为参考规范，让 LLM 直接输出符合
	// skill-writer 标准的 SKILL.md。如果为空，则回退到结构化提取
	// （Extract → Skill.RenderSKILLMd）。
	SkillWriterDoc string
}

// Extract 提取 0~N 个结构化 Skill（回退路径，LLM 输出 WORKFLOW_START 块）。
func (e *WorkflowExtractor) Extract(ctx context.Context, messages []core.Message) ([]Skill, error) {
	if e.SummarizeFunc == nil {
		return nil, fmt.Errorf("workflow: SummarizeFunc not set")
	}

	var sb strings.Builder
	sb.WriteString(buildWorkflowExtractionPrompt())
	e.appendMessages(&sb, messages)

	response, err := e.SummarizeFunc(ctx, sb.String())
	if err != nil {
		return nil, err
	}

	return parseWorkflowBlocks(response), nil
}

// ExtractSkillMd 让 LLM 直接生成完整的 SKILL.md 内容（按 skill-writer 规范）。
//
// 工作流程：
//  1. 把对话消息附加到 prompt
//  2. 把 skill-writer/SKILL.md 作为参考规范告诉 LLM
//  3. LLM 输出 SKILL_START...SKILL_END 块，里面是完整 SKILL.md
//  4. 解析后返回字符串切片（每个元素是一个完整 SKILL.md）
//
// 返回的字符串可以直接 WriteFile 到 <dir>/<name>/SKILL.md。
func (e *WorkflowExtractor) ExtractSkillMd(ctx context.Context, messages []core.Message) ([]string, error) {
	if e.SummarizeFunc == nil {
		return nil, fmt.Errorf("workflow: SummarizeFunc not set")
	}
	if e.SkillWriterDoc == "" {
		return nil, fmt.Errorf("workflow: SkillWriterDoc not set; cannot use ExtractSkillMd")
	}

	var sb strings.Builder
	sb.WriteString(buildSkillWriterPrompt(e.SkillWriterDoc))
	e.appendMessages(&sb, messages)

	response, err := e.SummarizeFunc(ctx, sb.String())
	if err != nil {
		return nil, err
	}

	return parseSkillMdBlocks(response), nil
}

// appendMessages 把对话消息追加到 builder。
func (e *WorkflowExtractor) appendMessages(sb *strings.Builder, messages []core.Message) {
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
// 用于确定文件应该写到哪个子目录。
func ExtractSkillName(skillMd string) string {
	re := regexp.MustCompile(`(?m)^name:\s*([a-z0-9][a-z0-9\-_]*)\s*$`)
	m := re.FindStringSubmatch(skillMd)
	if len(m) >= 2 {
		return sanitizeName(m[1])
	}
	return ""
}
