# crux-agent-runtime 架构设计文档

> 版本: v0.1.0 | 更新: 2026-06-17
> 状态: 设计方案

---

## 1. 核心问题

### 问题 1: Skill 系统如何设计？

当前 `agent.AgentTool` 是硬编码的工具定义，无法：
- 动态加载/卸载技能
- 从文件/URL 加载技能定义
- 第三方开发者贡献技能
- 技能之间共享上下文

### 问题 2: 如何接入第三方 Sandbox？

当前没有沙箱隔离，工具直接访问宿主系统，无法：
- 限制文件读写路径
- 限制命令执行
- 限制网络访问
- 接入 Docker/Firecracker 等容器沙箱

### 问题 3: Harness 和 Runtime 如何区分？

当前所有功能都在 `agent-runtime` 中，职责不清：
- Runtime 应该只负责"运行"
- Harness 应该负责"治理"（策略、审批、审计）

---

## 2. 设计原则

### 2.1 Skill 系统设计原则

1. **接口驱动** — Skill 接口，任何实现都可以注册
2. **声明式定义** — 技能用 YAML/JSON 声明，无需写代码
3. **沙箱感知** — 技能通过 Context 访问沙箱，而非直接访问系统
4. **热加载** — 运行时加载/卸载技能

### 2.2 Sandbox 设计原则

1. **接口抽象** — Sandbox 接口，可替换实现
2. **默认拒绝** — 未明确允许的操作一律拒绝
3. **分层权限** — 读/写/执行/网络 分别控制
4. **可组合** — 多个沙箱规则可组合

### 2.3 Harness vs Runtime 分离原则

| 维度 | Runtime | Harness |
|------|---------|---------|
| **职责** | 运行 Agent 循环 | 治理 Agent 行为 |
| **关注点** | 流式调用、工具执行 | 策略、审批、审计、观测 |
| **状态** | 无状态（可重入） | 有状态（持久化） |
| **依赖** | 只依赖 core (crux-ai) | 依赖 runtime + core |
| **可替换** | 整个 runtime 可替换 | harness 可选 |

---

## 3. Skill 系统设计

### 3.1 Skill 接口

```go
// Skill 是一个可注册到 Agent 的技能。
// || 技能接口
type Skill interface {
    // Name 返回技能名称（唯一标识）
    Name() string

    // Description 返回技能描述（供 LLM 理解）
    Description() string

    // ToolSchemas 返回此技能提供的工具定义（一个技能可提供多个工具）
    ToolSchemas() []core.ToolSchema

    // Handle 执行工具调用
    Handle(ctx SkillContext, call core.ToolCall) SkillResult

    // Init 初始化技能（可选）
    Init(config map[string]any) error

    // Close 清理资源（可选）
    Close() error
}

// SkillContext 提供技能执行上下文
type SkillContext struct {
    Context   context.Context
    Sandbox   Sandbox           // 沙箱访问
    Memory    MemoryProvider    // 记忆访问
    Session   SessionProvider   // 会话访问
    Logger    *slog.Logger
    Metadata  map[string]any    // 自定义元数据
}

// SkillResult 是技能执行结果
type SkillResult struct {
    Content   string
    IsError   bool
    Terminate bool              // 是否终止 Agent 循环
    Metadata  map[string]any    // 自定义元数据
}
```

### 3.2 Skill 注册表

```go
// SkillRegistry 管理所有已注册的技能
type SkillRegistry struct {
    mu       sync.RWMutex
    skills   map[string]Skill
    schemas  []core.ToolSchema
    context  SkillContext
}

func NewSkillRegistry(ctx SkillContext) *SkillRegistry { ... }

// Register 注册一个技能
func (r *SkillRegistry) Register(skill Skill) error

// Unregister 注销一个技能
func (r *SkillRegistry) Unregister(name string)

// Get 获取技能
func (r *SkillRegistry) Get(name string) (Skill, bool)

// ToolSchemas 返回所有技能的工具定义
func (r *SkillRegistry) ToolSchemas() []core.ToolSchema

// Execute 执行工具调用
func (r *SkillRegistry) Execute(ctx context.Context, call core.ToolCall) SkillResult
```

### 3.3 内置技能

```
skills/
├── registry.go        # 技能注册表
├── terminal/          # Shell 命令执行
│   └── terminal.go
├── readfile/          # 文件读取
│   └── readfile.go
├── writefile/         # 文件写入
│   └── writefile.go
├── listfiles/         # 目录列表
│   └── listfiles.go
├── memory/            # 长期记忆（LLM 可调用）
│   └── memory.go
├── websearch/         # 网页搜索
│   └── websearch.go
├── delegate/          # 跨 Agent 委托
│   └── delegate.go
└── compaction/        # 上下文压缩
    └── compaction.go
```

### 3.4 自定义技能（插件模式）

```go
// 1. 实现 Skill 接口
type MySkill struct{}

func (s *MySkill) Name() string        { return "my_skill" }
func (s *MySkill) Description() string { return "自定义技能" }
func (s *MySkill) ToolSchemas() []core.ToolSchema {
    return []core.ToolSchema{{
        Name:        "my_tool",
        Description: "执行自定义操作",
        Parameters:  json.RawMessage(`{...}`),
    }}
}
func (s *MySkill) Handle(ctx SkillContext, call core.ToolCall) SkillResult {
    // 通过 ctx.Sandbox 访问沙箱
    if err := ctx.Sandbox.CheckRead("/path"); err != nil {
        return SkillResult{Content: "权限不足", IsError: true}
    }
    // 执行操作
    return SkillResult{Content: "成功"}
}

// 2. 注册技能
registry.Register(&MySkill{})
```

### 3.5 YAML 声明式技能

```yaml
# skills/my-skill.yaml
name: my_skill
description: 自定义技能
version: "1.0"

tools:
  - name: my_tool
    description: 执行自定义操作
    parameters:
      type: object
      properties:
        input:
          type: string
          description: 输入参数
      required: [input]

# 执行方式（可选）
executor:
  type: http
  url: https://api.example.com/tools/my_tool
  method: POST
  headers:
    Authorization: "Bearer ${API_KEY}"
  
# 或者使用命令行
executor:
  type: command
  command: "python3 /path/to/skill.py"
  args: ["--input", "{{.input}}"]
```

```go
// 加载 YAML 技能
skill, _ := skills.LoadFromYAML("skills/my-skill.yaml")
registry.Register(skill)
```

---

## 4. Sandbox 系统设计

### 4.1 Sandbox 接口

```go
// Sandbox 控制 Agent 可以访问什么
// || 沙箱接口
type Sandbox interface {
    // CheckRead 检查路径是否可读
    CheckRead(path string) error

    // CheckWrite 检查路径是否可写
    CheckWrite(path string) error

    // CheckExec 检查命令是否可执行
    CheckExec(cmd string) error

    // CheckNetwork 检查网络访问是否允许
    CheckNetwork(addr string) error

    // Scope 返回当前沙箱的权限范围（用于日志/审计）
    Scope() Scope
}

// Scope 描述沙箱的权限范围
type Scope struct {
    Mode        AccessMode  // "restricted" 或 "full"
    ReadPaths   []string
    WritePaths  []string
    AllowCmds   []string
    BlockCmds   []string
    ProjectPath string
}

type AccessMode string
const (
    AccessModeRestricted AccessMode = "restricted"
    AccessModeFull       AccessMode = "full"
)
```

### 4.2 内置沙箱实现

```go
// 1. NoneSandbox — 无限制（开发环境）
type NoneSandbox struct{}
func (s *NoneSandbox) CheckRead(path string) error    { return nil }
func (s *NoneSandbox) CheckWrite(path string) error   { return nil }
func (s *NoneSandbox) CheckExec(cmd string) error     { return nil }
func (s *NoneSandbox) CheckNetwork(addr string) error { return nil }

// 2. ProcessSandbox — 进程级限制（生产环境）
type ProcessSandbox struct {
    readPaths   []string
    writePaths  []string
    allowedCmds []string
    blockedCmds []string
    timeout     int
}

// 3. DockerSandbox — Docker 容器隔离
type DockerSandbox struct {
    image       string
    volumes     []string
    networkMode string
}

// 4. FirecrackerSandbox — 轻量级 VM 隔离
type FirecrackerSandbox struct {
    kernelImage string
    rootFS      string
    memSizeMB   int
}
```

### 4.3 第三方 Sandbox 集成

```go
// 接口适配器：将第三方 Sandbox 适配为我们的接口

// 1. E2B Sandbox (https://e2b.dev)
type E2BSandbox struct {
    client *e2b.Client
    envID  string
}

func (s *E2BSandbox) CheckRead(path string) error {
    // E2B 的沙箱本身是隔离的，这里可以添加额外的路径检查
    return nil
}

func (s *E2BSandbox) CheckExec(cmd string) error {
    // 通过 E2B 的 API 执行命令
    return nil
}

// 2. Modal Sandbox (https://modal.com)
type ModalSandbox struct {
    client *modal.Client
    appID  string
}

// 3. Fly.io Machines
type FlySandbox struct {
    client  *fly.Client
    machine string
}

// 4. 自定义 HTTP Sandbox（微服务模式）
type HTTPSandbox struct {
    baseURL string
    client  *http.Client
}

func (s *HTTPSandbox) CheckExec(cmd string) error {
    // 调用远程沙箱服务执行命令
    resp, _ := s.client.Post(s.baseURL+"/exec", ...)
    // ...
}
```

### 4.4 Sandbox 注入

```go
// Agent 配置中注入沙箱
config := agent.AgentLoopConfig{
    Model:    model,
    Tools:    tools,
    StreamFn: streamFn,
    Sandbox:  processSandbox, // 注入沙箱
}

// 技能通过 SkillContext 访问沙箱
func (s *TerminalSkill) Handle(ctx SkillContext, call core.ToolCall) SkillResult {
    // 检查命令是否可执行
    if err := ctx.Sandbox.CheckExec("ls"); err != nil {
        return SkillResult{Content: "命令被拒绝", IsError: true}
    }
    
    // 执行命令
    cmd := exec.Command("sh", "-c", "ls")
    output, err := cmd.CombinedOutput()
    // ...
}
```

---

## 5. Harness vs Runtime 分离

### 5.1 职责划分

```
┌─────────────────────────────────────────────────────────────┐
│                        Harness (治理层)                       │
├─────────────────────────────────────────────────────────────┤
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐    │
│  │ Policy   │  │ Approval │  │  Hooks   │  │  Audit   │    │
│  │ (策略)    │  │ (审批)    │  │ (钩子)    │  │ (审计)    │    │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘    │
│                                                             │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐    │
│  │ Skills   │  │ Sandbox  │  │  Budget  │  │ Observe  │    │
│  │ (技能)    │  │ (沙箱)    │  │ (预算)    │  │ (观测)    │    │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘    │
├─────────────────────────────────────────────────────────────┤
│                        Runtime (运行层)                       │
├─────────────────────────────────────────────────────────────┤
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐    │
│  │  Agent   │  │ Session  │  │ Context  │  │  Memory  │    │
│  │  Loop    │  │ (会话)    │  │ (上下文)  │  │ (记忆)    │    │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘    │
│                                                             │
│  ┌──────────┐  ┌──────────┐                                  │
│  │AutoLearn │  │  Event   │                                  │
│  │(自动学习) │  │  Stream  │                                  │
│  └──────────┘  └──────────┘                                  │
└─────────────────────────────────────────────────────────────┘
```

### 5.2 详细职责表

#### Runtime 层（agent-runtime）

| 包 | 职责 | 不负责 |
|---|------|--------|
| `agent` | 运行 LLM→tool 循环 | 不决定用哪个模型 |
| `session` | 持久化对话历史 | 不决定保留多久 |
| `context` | Token 计数和压缩 | 不决定压缩策略 |
| `memory` | 存储长期记忆 | 不决定记忆什么 |
| `autolearn` | 自动提取记忆 | 不决定提取策略 |

**核心特征**：Runtime 是**无策略的执行引擎**，只负责"怎么做"，不决定"做什么"。

#### Harness 层（crux-harness）

| 包 | 职责 | 依赖 |
|---|------|------|
| `policy` | 声明式规则：允许/拒绝/需要审批 | 无 |
| `dispatch` | 工具调用单一入口（chokepoint）| policy + hooks |
| `approval` | 人工审批工作流 | 无 |
| `hooks` | 事件扇出（审计、指标、脱敏）| 无 |
| `skills` | 技能注册表 | sandbox |
| `sandbox` | 沙箱隔离 | 无 |
| `turn` | Turn FSM（持久化状态机）| dispatch + approval |
| `budget` | Token/Cost 预算控制 | hooks |
| `observe` | 指标和追踪 | hooks |

**核心特征**：Harness 是**治理层**，决定"做什么"和"不允许做什么"。

### 5.3 依赖关系

```
crux-harness
  ├── policy          → (standalone)
  ├── approval        → (standalone)
  ├── hooks           → (standalone)
  ├── sandbox         → (standalone)
  ├── budget          → hooks
  ├── observe         → hooks
  ├── skills          → sandbox
  ├── dispatch        → policy + hooks + skills
  ├── turn            → dispatch + approval
  └── api             → turn + skills + hooks
      │
      └──→ crux-agent-runtime (runtime 层)
             ├── agent
             ├── session
             ├── context
             ├── memory
             └── autolearn
                 │
                 └──→ crux-ai (LLM 层)
```

### 5.4 代码示例

#### Runtime 层（纯执行）

```go
// runtime 只负责执行，不关心策略
config := agent.AgentLoopConfig{
    Model:    model,
    Tools:    tools,
    StreamFn: streamFn,
}
stream := agent.AgentLoop(ctx, messages, config)
```

#### Harness 层（治理）

```go
// harness 添加治理能力
policyEngine := policy.NewEngine(rules)
sandbox := sandbox.NewProcess(processCfg)
dispatcher := dispatch.New(dispatch.Config{
    Policy:   policyEngine,
    Executor: skills.New(sandbox, memDir).Executor(),
})

// Turn FSM 包装
turnMachine := turn.New(store, trigger, logger)
turn.RegisterDefaultStates(turnMachine, turn.StatesConfig{
    AgentRunner: turn.NewAgentRunner(turn.AgentRunnerConfig{
        Dispatcher: dispatcher,
        Policy:     policyEngine.Check,
        Approval:   approvalSvc,
    }),
})
```

---

## 6. 完整架构图

```
┌─────────────────────────────────────────────────────────────────┐
│                         crux-chat (UI)                           │
│                     HTTP API + SSE + REPL                        │
└────────────────────────┬────────────────────────────────────────┘
                         │
┌────────────────────────▼────────────────────────────────────────┐
│                       crux-harness (治理层)                       │
├─────────────────────────────────────────────────────────────────┤
│  ┌─────────┐ ┌──────────┐ ┌─────────┐ ┌─────────┐ ┌─────────┐ │
│  │  API    │ │   Turn   │ │  Skills │ │ Sandbox │ │  Hooks  │ │
│  │ Server  │ │   FSM    │ │Registry │ │         │ │  Fanout │ │
│  └────┬────┘ └────┬─────┘ └────┬────┘ └────┬────┘ └────┬────┘ │
│       │           │            │            │           │       │
│  ┌────▼───────────▼────────────▼────────────▼───────────▼────┐ │
│  │                  dispatch.Dispatcher                      │ │
│  │         (单一 chokepoint: policy → hook → execute)        │ │
│  └──────────────────────────┬────────────────────────────────┘ │
│                             │                                   │
│  ┌──────────┐ ┌─────────────┴──────────┐ ┌──────────┐         │
│  │  Policy  │ │      Approval          │ │  Budget  │         │
│  │  Engine  │ │      Service           │ │  Control │         │
│  └──────────┘ └────────────────────────┘ └──────────┘         │
└────────────────────────┬───────────────────────────────────────┘
                         │
┌────────────────────────▼───────────────────────────────────────┐
│                    crux-agent-runtime (运行层)                    │
├─────────────────────────────────────────────────────────────────┤
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐       │
│  │  Agent   │  │ Session  │  │ Context  │  │  Memory  │       │
│  │  Loop    │  │ Manager  │  │ Manager  │  │ Provider │       │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘       │
│  ┌──────────┐  ┌──────────┐                                    │
│  │AutoLearn │  │  Event   │                                    │
│  │          │  │  Stream  │                                    │
│  └──────────┘  └──────────┘                                    │
└────────────────────────┬───────────────────────────────────────┘
                         │
┌────────────────────────▼───────────────────────────────────────┐
│                        crux-ai (LLM 层)                         │
├─────────────────────────────────────────────────────────────────┤
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐       │
│  │  core    │  │   llm    │  │providers │  │  router  │       │
│  │ (类型)    │  │  (API)   │  │ (实现)    │  │ (路由)    │       │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘       │
└─────────────────────────────────────────────────────────────────┘
```

---

## 7. 实现计划

### Phase 1: Skill 系统 (2-3 天)
- [ ] `skills/registry.go` — 技能注册表
- [ ] `skills/interface.go` — Skill/SkillContext/SkillResult 接口
- [ ] `skills/terminal/` — 终端技能（参考 crux-harness）
- [ ] `skills/readfile/` — 文件读取技能
- [ ] `skills/writefile/` — 文件写入技能
- [ ] 集成到 agent.AgentLoopConfig

### Phase 2: Sandbox 系统 (2-3 天)
- [ ] `sandbox/interface.go` — Sandbox 接口
- [ ] `sandbox/process.go` — 进程级沙箱
- [ ] `sandbox/none.go` — 无限制沙箱
- [ ] Sandbox 注入到 SkillContext
- [ ] 第三方 Sandbox 适配器

### Phase 3: Harness 层分离 (3-5 天)
- [ ] 创建 `harness/` 包
- [ ] `harness/policy/` — 策略引擎
- [ ] `harness/dispatch/` — 工具调用 chokepoint
- [ ] `harness/approval/` — 审批工作流
- [ ] `harness/hooks/` — 事件扇出
- [ ] `harness/turn/` — Turn FSM

### Phase 4: 集成测试 (2-3 天)
- [ ] 端到端测试
- [ ] Sandbox 隔离测试
- [ ] 策略引擎测试

**总计: 9-14 天**

---

## 8. 参考项目

| 项目 | 参考点 |
|------|--------|
| [crux-harness](file:///mnt/workspace/crux/crux/crux-harness) | Skill 注册表、Dispatch chokepoint、Sandbox |
| [crux-deployment/sandbox](file:///mnt/workspace/crux/crux/crux-deployment/sandbox) | Process sandbox、权限检查 |
| [E2B](https://e2b.dev) | 云端沙箱 |
| [Modal](https://modal.com) | 容器化沙箱 |
| [Fly.io](https://fly.io) | 轻量级 VM |
