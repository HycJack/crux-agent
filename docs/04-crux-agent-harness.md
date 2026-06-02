# crux-agent-harness 模块设计

## 一、模块概述

`crux-agent-harness` 是 Crux 框架的可选上层模块，提供横切关注点的可插拔组件。所有子包都是**独立于 runtime** 的，只消费 `crux-ai` 类型，因此可以自由组合使用。

## 二、核心组件

### 2.1 目录结构

```
crux-agent-harness/
├── token/                     # Token计数
│   ├── token.go               # Tiktoken计数
│   ├── messages.go            # 消息token估算
│   └── token_test.go          # 测试
├── context/                   # 上下文管理
│   ├── context.go             # 预算和状态检查
│   ├── compactor.go           # 压缩策略
│   └── pipeline.go            # 压缩管道
├── approval/                  # 审批网关
│   └── gate.go                # 规则型审批
├── checkpoint/                # 检查点
│   └── checkpoint.go          # 撤销/重做快照
├── session/                   # 会话持久化
│   ├── session.go             # 会话接口
│   ├── jsonl.go               # JSONL实现
│   └── types.go               # 会话类型
├── observe/                   # 可观测性
│   └── observe.go             # 结构化日志
├── prompt/                    # 提示词构建
│   └── prompt.go              # 系统提示词构建器
└── skills/                    # 技能管理
    └── skills.go              # SKILL.md加载器
```

### 2.2 模块职责

| 子包 | 职责 | 关键功能 |
|------|------|----------|
| `token` | Token计数 | Tiktoken-backed计数，进程级计数器池 |
| `context` | 上下文管理 | Token预算、状态检查、压缩管道 |
| `approval` | 审批网关 | 规则型工具执行审批 |
| `checkpoint` | 检查点 | 快照栈，支持撤销/重做 |
| `session` | 会话持久化 | JSONL格式会话树 |
| `observe` | 可观测性 | 结构化日志、计时、Token使用记录 |
| `prompt` | 提示词构建 | 系统提示词生成器 |
| `skills` | 技能管理 | SKILL.md加载器 |

## 三、Token 计数模块

### 3.1 核心功能

提供基于 Tiktoken 的 token 计数功能，支持：
- 文本 token 计数
- 消息序列 token 估算
- 进程级计数器池

### 3.2 MessageCounter

```go
type MessageCounter struct {
    encoder *tiktoken.Encoding
    model   string
}

func NewMessageCounter(model string) (*MessageCounter, error)
func (mc *MessageCounter) CountText(text string) int
func (mc *MessageCounter) EstimateRequestTokens(systemPrompt string, messages []core.Message, tools []core.Tool) TokenEstimate
```

### 3.3 TokenEstimate

```go
type TokenEstimate struct {
    System    int // 系统提示词token数
    Messages  int // 消息token数
    Tools     int // 工具定义token数
    Total     int // 总token数
}
```

### 3.4 消息估算

```go
func (mc *MessageCounter) EstimateRequestTokens(systemPrompt string, messages []core.Message, tools []core.Tool) TokenEstimate {
    est := TokenEstimate{}
    
    // 计算系统提示词
    est.System = mc.CountText(systemPrompt)
    
    // 计算消息
    for _, msg := range messages {
        est.Messages += mc.EstimateMessageTokens(msg)
    }
    
    // 计算工具
    for _, tool := range tools {
        est.Tools += mc.EstimateToolTokens(tool)
    }
    
    est.Total = est.System + est.Messages + est.Tools
    return est
}
```

## 四、上下文管理模块

### 4.1 Budget（预算）

```go
type Budget struct {
    ContextWindow int // 模型最大上下文窗口
    MaxOutput     int // 预留输出token数
    Headroom      int // 安全余量
}

func DefaultBudget(contextWindow int) Budget {
    return Budget{
        ContextWindow: contextWindow,
        MaxOutput:     8192,
        Headroom:      1024,
    }
}

func (b Budget) Available() int {
    avail := b.ContextWindow - b.MaxOutput - b.Headroom
    if avail < 0 {
        return 0
    }
    return avail
}
```

### 4.2 Status（状态）

```go
type Status struct {
    Used      int     // 当前使用token数
    Available int     // 可用token数
    Ratio     float64 // 使用比例 (used/available)
}

func CheckStatus(counter *token.MessageCounter, systemPrompt string, messages []core.Message, tools []core.Tool, budget Budget) Status {
    est := counter.EstimateRequestTokens(systemPrompt, messages, tools)
    avail := budget.Available()
    ratio := float64(est.Total) / float64(avail)
    if avail == 0 {
        ratio = 0
    }
    return Status{
        Used:      est.Total,
        Available: avail,
        Ratio:     ratio,
    }
}

func NeedsCompaction(status Status, threshold float64) bool {
    return status.Ratio >= threshold
}
```

### 4.3 Compactor（压缩器）

```go
type Compactor interface {
    Compact(ctx context.Context, req CompactionRequest, opts ...core.SimpleStreamOptions) (string, error)
}

// 压缩请求
type CompactionRequest struct {
    ToSummarize  []core.Message // 需要摘要的消息
    ToKeep       []core.Message // 需要保留的消息
    TokensBefore int            // 压缩前token数
    TokensKept   int            // 保留消息的token数
    SplitIndex   int            // 分割索引
}
```

### 4.4 压缩策略

| 策略 | 实现 | 适用场景 |
|------|------|----------|
| LLMCompactor | 使用LLM生成摘要 | 需要语义理解 |
| SlidingWindowCompactor | 滑动窗口，保留最近消息 | 简单场景 |
| HybridCompactor | 混合策略 | 平衡效果和成本 |

#### LLMCompactor

```go
type LLMCompactor struct {
    model core.Model
}

func NewLLMCompactor(model core.Model) *LLMCompactor {
    return &LLMCompactor{model: model}
}

func (c *LLMCompactor) Compact(ctx context.Context, req CompactionRequest, opts ...core.SimpleStreamOptions) (string, error) {
    // 构建摘要提示词
    prompt := buildSummaryPrompt(req.ToSummarize)
    
    // 调用LLM生成摘要
    msg, err := ai.CompleteSimple(ctx, c.model, []core.Message{
        core.UserMessage{Content: prompt},
    }, opts...)
    if err != nil {
        return "", err
    }
    
    // 提取摘要内容
    return extractSummary(msg), nil
}
```

### 4.5 Pipeline（管道）

```go
type Pipeline struct {
    mu     sync.RWMutex
    config PipelineConfig
    mc     *token.MessageCounter
}

type PipelineConfig struct {
    Model               core.Model
    Budget              Budget
    CompactionThreshold float64   // 压缩阈值 (0.0-1.0)
    MinMessagesToKeep   int       // 最少保留消息数
    Compactor           Compactor // 压缩策略
    OnCompaction        func(*CompactionResult)
}

func DefaultPipelineConfig(model core.Model, contextWindow int) PipelineConfig {
    return PipelineConfig{
        Model:               model,
        Budget:              DefaultBudget(contextWindow),
        CompactionThreshold: 0.9,
        MinMessagesToKeep:   10,
    }
}
```

### 4.6 压缩流程

```go
func (p *Pipeline) Compact(ctx context.Context, systemPrompt string, messages []core.Message, 
    tools []core.Tool, opts ...core.SimpleStreamOptions) ([]core.Message, *CompactionResult, error) {
    
    // 1. 规划压缩
    req, err := PlanCompaction(p.mc, systemPrompt, messages, tools, p.config.Budget, p.config.MinMessagesToKeep)
    if err != nil {
        return messages, nil, nil // 无需压缩
    }
    
    // 2. 执行压缩
    summary, err := p.config.Compactor.Compact(ctx, *req, opts...)
    if err != nil {
        return messages, nil, err
    }
    
    // 3. 构建压缩后的消息
    summaryMsg := core.UserMessage{
        Role:    "user",
        Content: summaryPrefix + summary + summarySuffix,
    }
    compacted := append([]core.Message{summaryMsg}, req.ToKeep...)
    
    // 4. 计算新的token使用
    newEst := p.mc.EstimateRequestTokens(systemPrompt, compacted, tools)
    
    // 5. 生成结果
    result := &CompactionResult{
        Summary:      summary,
        TokensBefore: req.TokensBefore,
        TokensAfter:  newEst.Total,
        TokensSaved:  req.TokensBefore - newEst.Total,
        KeptCount:    len(req.ToKeep),
    }
    
    // 6. 通知回调
    if p.config.OnCompaction != nil {
        p.config.OnCompaction(result)
    }
    
    return compacted, result, nil
}
```

### 4.7 压缩规划

```go
func PlanCompaction(counter *token.MessageCounter, systemPrompt string, messages []core.Message, 
    tools []core.Tool, budget Budget, minKeep int) (*CompactionRequest, error) {
    
    // 检查消息数量
    if len(messages) <= minKeep {
        return nil, fmt.Errorf("not enough messages to compact")
    }
    
    // 估算当前token使用
    est := counter.EstimateRequestTokens(systemPrompt, messages, tools)
    avail := budget.Available()
    
    // 检查是否需要压缩
    if est.Total <= avail {
        return nil, fmt.Errorf("no compaction needed")
    }
    
    // 二分查找分割点
    low := 0
    high := len(messages) - minKeep
    
    for low < high {
        mid := (low + high) / 2
        kept := messages[mid:]
        keptEst := counter.EstimateRequestTokens(systemPrompt, kept, tools)
        
        if keptEst.Total <= avail {
            high = mid // 尝试保留更多
        } else {
            low = mid + 1 // 需要保留更少
        }
    }
    
    splitIdx := low
    toSummarize := messages[:splitIdx]
    toKeep := messages[splitIdx:]
    
    return &CompactionRequest{
        ToSummarize:  toSummarize,
        ToKeep:       toKeep,
        TokensBefore: est.Total,
        TokensKept:   counter.EstimateRequestTokens(systemPrompt, toKeep, tools).Total,
        SplitIndex:   splitIdx,
    }, nil
}
```

## 五、审批网关模块

### 5.1 Gate（网关）

```go
type Gate struct {
    mu         sync.RWMutex
    rules      []Rule
    onAsk      func(Request) Result // 决策询问回调
    defaultDec Decision             // 默认决策
}

func New() *Gate {
    return &Gate{defaultDec: DecisionAllow}
}

func NewStrict() *Gate {
    return &Gate{defaultDec: DecisionBlock}
}
```

### 5.2 Decision（决策）

```go
type Decision int

const (
    DecisionAllow Decision = iota // 允许执行
    DecisionBlock                 // 阻止执行
    DecisionAsk                   // 询问用户
)
```

### 5.3 Rule（规则）

```go
type Rule struct {
    Name    string
    Match   func(Request) bool // 匹配函数
    Approve Decision           // 匹配后的决策
    Reason  string             // 决策原因
}

func (g *Gate) AddRule(rule Rule) {
    g.mu.Lock()
    defer g.mu.Unlock()
    g.rules = append(g.rules, rule)
}
```

### 5.4 规则匹配器

```go
// 按名称匹配
func MatchByName(name string) func(Request) bool {
    return func(r Request) bool { return r.ToolName == name }
}

// 按前缀匹配
func MatchByPrefix(prefix string) func(Request) bool {
    return func(r Request) bool {
        return len(r.ToolName) >= len(prefix) && r.ToolName[:len(prefix)] == prefix
    }
}

// 匹配所有
func Always() func(Request) bool {
    return func(r Request) bool { return true }
}
```

### 5.5 评估流程

```go
func (g *Gate) Evaluate(req Request) Result {
    g.mu.RLock()
    rules := make([]Rule, len(g.rules))
    copy(rules, g.rules)
    onAsk := g.onAsk
    defaultDec := g.defaultDec
    g.mu.RUnlock()
    
    // 按顺序评估规则
    for _, rule := range rules {
        if rule.Match(req) {
            if rule.Approve == DecisionAsk && onAsk != nil {
                return onAsk(req)
            }
            return Result{Decision: rule.Approve, Reason: rule.Reason}
        }
    }
    
    // 返回默认决策
    return Result{Decision: defaultDec}
}
```

### 5.6 预设规则集

```go
func DangerousTools() []Rule {
    return []Rule{
        {Name: "bash_execute", Match: MatchByName("bash"), Approve: DecisionAsk, Reason: "Shell execution requires approval"},
        {Name: "file_write", Match: MatchByName("write_file"), Approve: DecisionAsk, Reason: "File write requires approval"},
        {Name: "file_delete", Match: MatchByName("delete_file"), Approve: DecisionBlock, Reason: "File deletion is blocked"},
    }
}
```

## 六、检查点模块

### 6.1 Checkpoint（检查点）

```go
type Checkpoint struct {
    mu      sync.Mutex
    stack   []Snapshot
    current int
}

type Snapshot struct {
    Messages     []core.Message
    Model        core.Model
    SystemPrompt string
    Timestamp    time.Time
}

func New() *Checkpoint {
    return &Checkpoint{
        stack:   make([]Snapshot, 0),
        current: -1,
    }
}
```

### 6.2 核心操作

```go
// 保存快照
func (c *Checkpoint) Save(messages []core.Message, model core.Model, systemPrompt string) {
    c.mu.Lock()
    defer c.mu.Unlock()
    
    // 截断当前位置之后的快照
    c.stack = c.stack[:c.current+1]
    
    // 添加新快照
    c.stack = append(c.stack, Snapshot{
        Messages:     append([]core.Message{}, messages...),
        Model:        model,
        SystemPrompt: systemPrompt,
        Timestamp:    time.Now(),
    })
    c.current++
}

// 撤销
func (c *Checkpoint) Undo() (*Snapshot, bool) {
    c.mu.Lock()
    defer c.mu.Unlock()
    
    if c.current <= 0 {
        return nil, false
    }
    
    c.current--
    return &c.stack[c.current], true
}

// 重做
func (c *Checkpoint) Redo() (*Snapshot, bool) {
    c.mu.Lock()
    defer c.mu.Unlock()
    
    if c.current >= len(c.stack)-1 {
        return nil, false
    }
    
    c.current++
    return &c.stack[c.current], true
}

// 清除所有快照
func (c *Checkpoint) Clear() {
    c.mu.Lock()
    defer c.mu.Unlock()
    
    c.stack = make([]Snapshot, 0)
    c.current = -1
}
```

## 七、会话模块

### 7.1 会话类型

```go
type Session struct {
    ID          string
    Messages    []SessionEntry
    CurrentBranch string
    Branches    map[string]Branch
}

type SessionEntry interface {
    GetTimestamp() time.Time
    entryTag()
}

// 条目类型
type MessageEntry struct {
    Message core.Message
}

type CustomMessageEntry struct {
    Role      string
    Content   string
    Timestamp time.Time
}

type BranchSummary struct {
    BranchID  string
    Summary   string
    Timestamp time.Time
}

type CompactionEntry struct {
    OriginalCount int
    SummarizedCount int
    Timestamp    time.Time
}

type ModelChangeEntry struct {
    OldModel core.Model
    NewModel core.Model
    Timestamp time.Time
}

type ThinkingChangeEntry struct {
    OldLevel core.ThinkingLevel
    NewLevel core.ThinkingLevel
    Timestamp time.Time
}

type SessionInfo struct {
    Title       string
    Description string
    CreatedAt   time.Time
    UpdatedAt   time.Time
}

type LabelEntry struct {
    Label     string
    Timestamp time.Time
}
```

### 7.2 JSONL 持久化

```go
type JSONLSession struct {
    mu     sync.Mutex
    file   *os.File
    encoder *json.Encoder
}

func NewJSONLSession(path string) (*JSONLSession, error) {
    file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
    if err != nil {
        return nil, err
    }
    
    return &JSONLSession{
        file:   file,
        encoder: json.NewEncoder(file),
    }, nil
}

func (s *JSONLSession) Append(entry SessionEntry) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    return s.encoder.Encode(entry)
}

func (s *JSONLSession) ReadAll() ([]SessionEntry, error) {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    // 重新读取文件
    s.file.Seek(0, io.SeekStart)
    
    var entries []SessionEntry
    decoder := json.NewDecoder(s.file)
    
    for {
        var entry SessionEntry
        if err := decoder.Decode(&entry); err == io.EOF {
            break
        } else if err != nil {
            return nil, err
        }
        entries = append(entries, entry)
    }
    
    return entries, nil
}
```

## 八、可观测性模块

### 8.1 Observer（观测器）

```go
type Observer struct {
    mu      sync.Mutex
    writer  io.Writer
    timer   *TurnTimer
    usage   TokenUsageRecorder
}

func New(writer io.Writer) *Observer {
    return &Observer{
        writer:  writer,
        timer:   NewTurnTimer(),
        usage:   NewTokenUsageRecorder(),
    }
}
```

### 8.2 TurnTimer（轮次计时器）

```go
type TurnTimer struct {
    mu      sync.Mutex
    start   time.Time
    elapsed time.Duration
}

func (t *TurnTimer) Start() {
    t.mu.Lock()
    defer t.mu.Unlock()
    t.start = time.Now()
}

func (t *TurnTimer) Stop() time.Duration {
    t.mu.Lock()
    defer t.mu.Unlock()
    t.elapsed = time.Since(t.start)
    return t.elapsed
}

func (t *TurnTimer) Elapsed() time.Duration {
    t.mu.Lock()
    defer t.mu.Unlock()
    return t.elapsed
}
```

### 8.3 TokenUsageRecorder（Token使用记录器）

```go
type TokenUsageRecorder struct {
    mu        sync.Mutex
    totalInput  int
    totalOutput int
    turns      []TurnUsage
}

type TurnUsage struct {
    TurnNumber int
    Input      int
    Output     int
    Cost       float64
    Timestamp  time.Time
}

func (r *TokenUsageRecorder) Record(turn int, usage core.Usage, cost float64) {
    r.mu.Lock()
    defer r.mu.Unlock()
    
    r.totalInput += usage.Input
    r.totalOutput += usage.Output
    r.turns = append(r.turns, TurnUsage{
        TurnNumber: turn,
        Input:      usage.Input,
        Output:     usage.Output,
        Cost:       cost,
        Timestamp:  time.Now(),
    })
}

func (r *TokenUsageRecorder) Summary() TokenUsageSummary {
    r.mu.Lock()
    defer r.mu.Unlock()
    
    totalCost := 0.0
    for _, t := range r.turns {
        totalCost += t.Cost
    }
    
    return TokenUsageSummary{
        TotalTurns:  len(r.turns),
        TotalInput:  r.totalInput,
        TotalOutput: r.totalOutput,
        TotalCost:   totalCost,
    }
}
```

### 8.4 结构化日志

```go
func (o *Observer) LogEvent(event AgentEvent) {
    entry := LogEntry{
        Timestamp: time.Now(),
        EventType: getEventType(event),
        Data:      serializeEvent(event),
    }
    
    o.mu.Lock()
    defer o.mu.Unlock()
    
    json.NewEncoder(o.writer).Encode(entry)
}

type LogEntry struct {
    Timestamp time.Time `json:"timestamp"`
    EventType string    `json:"eventType"`
    Data      any       `json:"data"`
}
```

## 九、提示词构建模块

### 9.1 PromptBuilder（提示词构建器）

```go
type PromptBuilder struct {
    systemPrompt string
    sections     []Section
}

type Section struct {
    Name    string
    Content string
    Enabled bool
}

func NewBuilder() *PromptBuilder {
    return &PromptBuilder{
        sections: make([]Section, 0),
    }
}

func (p *PromptBuilder) AddSection(name, content string) *PromptBuilder {
    p.sections = append(p.sections, Section{
        Name:    name,
        Content: content,
        Enabled: true,
    })
    return p
}

func (p *PromptBuilder) DisableSection(name string) *PromptBuilder {
    for i := range p.sections {
        if p.sections[i].Name == name {
            p.sections[i].Enabled = false
        }
    }
    return p
}

func (p *PromptBuilder) Build() string {
    var parts []string
    
    // 添加系统提示词
    if p.systemPrompt != "" {
        parts = append(parts, p.systemPrompt)
    }
    
    // 添加各部分
    for _, section := range p.sections {
        if section.Enabled {
            parts = append(parts, fmt.Sprintf("## %s\n\n%s", section.Name, section.Content))
        }
    }
    
    return strings.Join(parts, "\n\n")
}
```

### 9.2 技能集成

```go
func (p *PromptBuilder) AddSkills(skills []Skill) *PromptBuilder {
    if len(skills) == 0 {
        return p
    }
    
    var skillParts []string
    for _, skill := range skills {
        if skill.DisableModelInvocation {
            continue
        }
        skillParts = append(skillParts, fmt.Sprintf("- %s: %s", skill.Name, skill.Description))
    }
    
    if len(skillParts) > 0 {
        p.AddSection("Available Skills", strings.Join(skillParts, "\n"))
    }
    
    return p
}
```

## 十、技能管理模块

### 10.1 Skill（技能）

```go
type Skill struct {
    Name                 string
    Description          string
    InputSchema          json.RawMessage
    OutputSchema         json.RawMessage
    DisableModelInvocation bool
    Metadata             map[string]any
}
```

### 10.2 SKILL.md 格式

```markdown
---
name: "bash"
description: "Execute shell commands"
disable_model_invocation: false
---

## Description

Execute shell commands on the system.

## Parameters

| Name | Type | Description |
|------|------|-------------|
| command | string | The command to execute |

## Output

The output of the command.
```

### 10.3 加载器

```go
func LoadSkill(path string) (Skill, error) {
    content, err := os.ReadFile(path)
    if err != nil {
        return Skill{}, err
    }
    
    // 解析 YAML frontmatter
    parts := strings.SplitN(string(content), "---", 3)
    if len(parts) < 3 {
        return Skill{}, fmt.Errorf("invalid SKILL.md format")
    }
    
    // 解析元数据
    var meta Skill
    if err := yaml.Unmarshal([]byte(parts[1]), &meta); err != nil {
        return Skill{}, err
    }
    
    // 提取描述
    meta.Description = extractDescription(parts[2])
    
    return meta, nil
}

func LoadSkills(dir string) ([]Skill, error) {
    files, err := filepath.Glob(filepath.Join(dir, "*", "SKILL.md"))
    if err != nil {
        return nil, err
    }
    
    var skills []Skill
    for _, file := range files {
        skill, err := LoadSkill(file)
        if err != nil {
            // 跳过无效的技能文件
            continue
        }
        skills = append(skills, skill)
    }
    
    return skills, nil
}
```

## 十一、模块间协作

### 11.1 典型使用流程

```
┌─────────────────────────────────────────────────────────────────┐
│                      Agent Run                                  │
├─────────────────────────────────────────────────────────────────┤
│  1. Pipeline.Check() → 检查token状态                            │
│         ↓                                                       │
│  2. Pipeline.ShouldCompact() → 判断是否需要压缩                   │
│         ↓                                                       │
│  3. Pipeline.Compact() → 执行压缩                                │
│         ↓                                                       │
│  4. Gate.Evaluate() → 审批工具调用                               │
│         ↓                                                       │
│  5. Checkpoint.Save() → 保存检查点                               │
│         ↓                                                       │
│  6. Observer.LogEvent() → 记录事件                               │
│         ↓                                                       │
│  7. Session.Append() → 持久化会话                                │
└─────────────────────────────────────────────────────────────────┘
```

### 11.2 与 Runtime 集成

```go
// 创建 Agent
agent := agent.New(agent.AgentOptions{
    InitialState: &agent.AgentState{
        Model:        model,
        SystemPrompt: promptBuilder.Build(),
        Tools:        tools,
    },
})

// 设置上下文压缩钩子
agent.State().PrepareNextTurn = func(config *agent.AgentLoopConfig, msg core.AssistantMessage, 
    results []core.ToolResultMessage, messages []core.Message) {
    
    // 检查是否需要压缩
    if pipeline.ShouldCompact(config.SystemPrompt, messages, config.Tools) {
        compacted, _, err := pipeline.Compact(ctx, config.SystemPrompt, messages, config.Tools)
        if err == nil {
            // 更新消息列表
            messages = compacted
        }
    }
}

// 设置审批钩子
agent.State().BeforeToolCall = func(ctx agent.BeforeToolCallContext) *agent.ToolCallBlock {
    result := gate.Evaluate(approval.Request{
        ToolName: ctx.ToolCall.Name,
        ToolID:   ctx.ToolCall.ID,
        Args:     ctx.ToolCall.Arguments,
    })
    
    if result.Decision == approval.DecisionBlock {
        return &agent.ToolCallBlock{Block: true, Reason: result.Reason}
    }
    
    if result.Decision == approval.DecisionAsk {
        // 询问用户...
        return &agent.ToolCallBlock{Block: true, Reason: "Waiting for user approval"}
    }
    
    return nil
}
```

## 十二、设计原则

### 12.1 无运行时依赖

所有 harness 组件只依赖 `crux-ai`，不依赖 `crux-agent-runtime`：

```go
// 正确：只依赖 crux-ai
import (
    "crux-ai/core"
    "crux-ai/ai"
)

// 错误：不应该依赖 runtime
import (
    "crux-agent-runtime/agent"  // ❌ 不允许
)
```

### 12.2 可插拔设计

每个组件都是独立的，可以按需使用：

```go
// 只使用压缩功能
pipeline, _ := context.NewPipeline(context.DefaultPipelineConfig(model, 128000))

// 只使用审批功能
gate := approval.New()
gate.AddRule(approval.Rule{
    Name:    "block-dangerous",
    Match:   approval.MatchByName("bash"),
    Approve: approval.DecisionAsk,
    Reason:  "Shell execution requires approval",
})

// 组合使用多个组件
observer := observe.New(os.Stdout)
checkpoint := checkpoint.New()
```

### 12.3 线程安全

所有组件都使用 `sync.Mutex` 或 `sync.RWMutex` 保护共享状态：

```go
type Pipeline struct {
    mu     sync.RWMutex
    config PipelineConfig
    mc     *token.MessageCounter
}
```

### 12.4 可配置性

通过配置结构提供灵活的定制：

```go
config := context.PipelineConfig{
    Model:               model,
    Budget:              context.DefaultBudget(128000),
    CompactionThreshold: 0.85,  // 自定义阈值
    MinMessagesToKeep:   5,     // 自定义保留数量
    Compactor:           context.NewSlidingWindowCompactor(),  // 自定义压缩策略
    OnCompaction: func(result *context.CompactionResult) {
        log.Printf("Compacted: %d → %d tokens saved", result.TokensSaved)
    },
}
```