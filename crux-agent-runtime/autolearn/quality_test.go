package autolearn

import "testing"

// --- Fact quality gate ---

func TestIsValuableValue(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		// ✅ Should be kept
		{"小明", true},
		{"杭州", true},
		{"中文", true},
		{"Go 开发", true},
		{"MacBook Pro", true},
		{"项目名=crux-agent", true},

		// ❌ Transient/temporal — should be rejected
		{"今天心情不好", false},
		{"我现在在调试", false},
		{"刚才买了 Y", false},
		{"马上开会", false},
		{"今天周五", false},

		// ❌ Pure politeness — should be rejected
		{"你好", false},
		{"谢谢", false},
		{"好的", false},
		{"嗯", false},
		{"OK", false},

		// ❌ Questions — should be rejected
		{"你是谁?", false},
		{"怎么办？", false},

		// ❌ Too short — should be rejected
		{"", false},
		{"a", false},
		{"的", false},
	}
	for _, tt := range tests {
		got := isValuableValue(tt.value)
		if got != tt.want {
			t.Errorf("isValuableValue(%q) = %v, want %v", tt.value, got, tt.want)
		}
	}
}

func TestParseExtractionResult_DropsLowQualityValues(t *testing.T) {
	// Simulate LLM output that includes a mix of valuable and low-quality
	// entries. Only the valuable ones should survive.
	resp := `user.name=小明
user.location=今天杭州
fact.mood=今天心情不好
task.current=改这个 bug
fact.greeting=你好
fact.ask=怎么办？
fact.device=MacBook Pro
assistant.name=小七`

	got := parseExtractionResult(resp, SourceLLMExtract)
	keys := map[string]string{}
	for _, tg := range got {
		keys[tg.Key] = tg.Value
	}

	// Expected to survive
	if keys["user.name"] != "小明" {
		t.Errorf("user.name missing/wrong: %q", keys["user.name"])
	}
	if keys["assistant.name"] != "小七" {
		t.Errorf("assistant.name missing/wrong: %q", keys["assistant.name"])
	}
	if keys["fact.device"] != "MacBook Pro" {
		t.Errorf("fact.device missing/wrong: %q", keys["fact.device"])
	}

	// Expected to be filtered
	if _, ok := keys["user.location"]; ok {
		t.Errorf("user.location=今天杭州 should be rejected (transient prefix)")
	}
	if _, ok := keys["fact.mood"]; ok {
		t.Errorf("fact.mood should be rejected (transient)")
	}
	if _, ok := keys["task.current"]; ok {
		t.Errorf("task.current should be rejected (not in key whitelist)")
	}
	if _, ok := keys["fact.greeting"]; ok {
		t.Errorf("fact.greeting should be rejected (polite)")
	}
	if _, ok := keys["fact.ask"]; ok {
		t.Errorf("fact.ask should be rejected (question)")
	}
}

func TestParseExtractionResult_HonorsExplicitMarkers(t *testing.T) {
	// Explicit user input like "[remember:task.current=修这个 bug]" should
	// be honored even though it would fail the LLM quality gate.
	resp := "task.current=修这个 bug"
	got := parseExtractionResult(resp, SourceUserInput)
	if len(got) != 1 {
		t.Fatalf("expected 1 trigger from explicit marker, got %d", len(got))
	}
	if got[0].Key != "task.current" || got[0].Value != "修这个 bug" {
		t.Errorf("explicit marker should be honored verbatim: %+v", got[0])
	}
}

// --- Workflow quality gate ---

func TestIsConcreteStep(t *testing.T) {
	good := []string{
		"运行 npm run build",
		"打开 /etc/hosts 添加 127.0.0.1 api.local",
		"执行 git add -A && git commit",
		"调用 curl https://api.example.com/health",
	}
	for _, s := range good {
		if !isConcreteStep(s) {
			t.Errorf("concrete step %q was rejected", s)
		}
	}

	bad := []string{
		"确认环境正常",
		"看看日志",
		"考虑是否需要重启",
		"判断要不要回滚",
		"尝试修复",
		"分析问题",
		"询问用户偏好",
	}
	for _, s := range bad {
		if isConcreteStep(s) {
			t.Errorf("vague step %q was accepted", s)
		}
	}
}

func TestIsReusableTrigger(t *testing.T) {
	good := []string{
		"用户要求部署到 staging 环境时",
		"开发者在 PR 合并前需要做",
		"每次新功能上线前",
	}
	for _, s := range good {
		if !isReusableTrigger(s) {
			t.Errorf("reusable trigger %q was rejected", s)
		}
	}

	bad := []string{
		"修这个 bug",
		"改这个错误",
		"解决这个报错",
		"回答这个具体问题",
		"帮我写这个 PR",
		"这次的需求",
	}
	for _, s := range bad {
		if isReusableTrigger(s) {
			t.Errorf("single-shot trigger %q was accepted", s)
		}
	}
}

func TestParseWorkflowBlocks_AcceptsSOPWorkflow(t *testing.T) {
	resp := `WORKFLOW_START
NAME: deploy-staging
TRIGGER: 每次需要部署到 staging 环境时
DESCRIPTION: 标准部署流程
STEP: 运行 npm run build
STEP: 运行 npm run test
STEP: 执行 deploy.sh staging
STEP: 运行 curl 健康检查
OUTPUT: staging 服务返回 200 且所有测试通过
SOURCE: 团队 5 次部署都按此流程
WORKFLOW_END`

	skills := parseWorkflowBlocks(resp)
	if len(skills) != 1 {
		t.Fatalf("expected 1 SOP skill, got %d", len(skills))
	}
	if skills[0].Name != "deploy-staging" {
		t.Errorf("name = %q", skills[0].Name)
	}
	if len(skills[0].Steps) != 4 {
		t.Errorf("steps count = %d, want 4", len(skills[0].Steps))
	}
}

func TestParseWorkflowBlocks_DropsSingleShotTrigger(t *testing.T) {
	resp := `WORKFLOW_START
NAME: fix-this-bug
TRIGGER: 修这个 bug
STEP: 打开文件 X
STEP: 修改代码 Y
STEP: 运行测试
OUTPUT: 测试通过
WORKFLOW_END`

	skills := parseWorkflowBlocks(resp)
	if len(skills) != 0 {
		t.Errorf("single-shot workflow should be dropped, got %d skills", len(skills))
	}
}

func TestParseWorkflowBlocks_DropsVagueSteps(t *testing.T) {
	// Steps include vague verbs ("确认", "看看") that should be filtered out.
	// After filtering, only 1 concrete step remains (< 3) → workflow dropped.
	resp := `WORKFLOW_START
NAME: cleanup
TRIGGER: 用户要求清理环境时
STEP: 确认环境正常
STEP: 看看日志有没有错
STEP: 考虑要不要备份
OUTPUT: 环境干净
WORKFLOW_END`

	skills := parseWorkflowBlocks(resp)
	if len(skills) != 0 {
		t.Errorf("workflow with only vague steps should be dropped, got %d", len(skills))
	}
}

func TestParseWorkflowBlocks_KeepsConcreteStepsAmongVague(t *testing.T) {
	// 3 concrete steps + 2 vague → keep only concrete (still ≥ 3).
	resp := `WORKFLOW_START
NAME: deploy-staging
TRIGGER: 用户要求部署到 staging 时
STEP: 运行 npm run build
STEP: 确认编译成功
STEP: 执行 deploy.sh
STEP: 运行 curl 健康检查
STEP: 看看日志
OUTPUT: 健康检查 200
WORKFLOW_END`

	skills := parseWorkflowBlocks(resp)
	if len(skills) != 1 {
		t.Fatalf("expected 1, got %d", len(skills))
	}
	if len(skills[0].Steps) != 3 {
		t.Errorf("expected 3 concrete steps after filtering, got %d: %v", len(skills[0].Steps), skills[0].Steps)
	}
}

func TestParseWorkflowBlocks_DropsBelowThreeSteps(t *testing.T) {
	resp := `WORKFLOW_START
NAME: tiny
TRIGGER: 用户要求执行某操作时
STEP: 运行 build
STEP: 执行 test
WORKFLOW_END`
	if skills := parseWorkflowBlocks(resp); len(skills) != 0 {
		t.Errorf("workflow with <3 steps should be dropped, got %d", len(skills))
	}
}
