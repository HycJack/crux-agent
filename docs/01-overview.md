# Crux AI Agent Framework - 总体架构设计

## 一、项目概述

Crux 是一个基于 Go 的多层模块化 AI Agent 框架，用于构建 AI Agent 和 Agent 驱动的应用程序。项目被有意拆分为四个独立的 Go 模块，每个层都可以独立采用或跳过。

### 1.1 核心特性

- **供应商无关**：统一的流式 API，支持 OpenAI、Anthropic、Google、Bedrock、Mistral、Azure 等
- **推理感知**：原生支持 `ThinkingContent` 块和按模型的推理强度映射
- **多模态**：文本、图片、音频和工具调用共享统一的 `ContentBlock` 联合类型
- **工具循环**：支持中止的流式 `AgentLoop`，并发工具执行和结构化事件
- **Harness 管道**：Token 感知的上下文压缩、规则型审批网关、撤销/重做检查点、JSONL 会话持久化
- **REPL 编码 Agent**：跨平台终端助手，支持 Windows、macOS 和 Linux

### 1.2 模块列表

| 模块 | 路径 | 用途 |
|------|------|------|
| **crux-ai** | `crux-ai/` | 供应商无关的 AI 客户端 — 类型、流式传输和适配器 |
| **crux-agent-runtime** | `crux-agent-runtime/` | 可重用的 Agent 循环，包含事件流和工具执行框架 |
| **crux-agent-harness** | `crux-agent-harness/` | 可插拔的横切关注点：上下文压缩、审批网关、检查点、会话持久化等 |
| **crux-agent-tui** | `crux-agent-tui/` | TUI 终端界面 |

## 二、架构分层

### 2.1 依赖关系

依赖方向严格单向：

```
crux-agent-tui  →  crux-agent-runtime  →  crux-ai
crux-agent-harness  ───────────────────→  crux-ai
```

**关键设计原则**：
- `crux-agent-harness` 不了解 runtime
- runtime 不了解 harness
- 每层都可以独立替换或扩展

### 2.2 分层说明

```
┌─────────────────────────────────────────────────────────────┐
│              crux-agent-tui (TUI界面层)                      │
│                      ↓                                      │
├─────────────────────────────────────────────────────────────┤
│              crux-agent-runtime (Agent运行时)                │
│                      ↓                                      │
├─────────────────────────────────────────────────────────────┤
│              crux-agent-harness (Harness组件)               │
│                      ↓                                      │
├─────────────────────────────────────────────────────────────┤
│              crux-ai (AI客户端核心)                          │
└─────────────────────────────────────────────────────────────┘
```

## 三、核心设计模式

| 模式 | 应用场景 | 实现位置 |
|------|----------|----------|
| **服务定位器** | 动态注册/获取供应商 | `core/registry.go` |
| **策略模式** | 可插拔的压缩策略 | `context/compactor.go` |
| **观察者模式** | 事件订阅机制 | `agent/agent.go` |
| **模板方法** | Agent循环流程 | `agent/agent-loop.go` |
| **工厂模式** | Agent创建 | `agent/agent.go:New()` |
| **钩子模式** | 扩展点设计 | `AgentState` 中的回调函数 |

## 四、数据流

### 4.1 请求流程

```
用户输入 → TUI → Agent.Run() → AgentLoop → LLM Stream → 事件流 → TUI 渲染
                      ↓
                 工具执行
                      ↓
                 结果返回
```

### 4.2 事件流

```
EventAgentStart
  → EventTurnStart
    → EventMessageStart
      → EventMessageUpdate (多次)
      → EventMessageEnd
    → EventToolExecStart (可选)
      → EventToolExecUpdate (可选)
      → EventToolExecEnd
    → EventTurnEnd
  → [重复多轮]
  → stream.End(messages)
```

## 五、关键技术决策

### 5.1 为什么使用 Go？

- **并发模型**：goroutine + channel 适合流式处理和并发工具执行
- **类型安全**：编译期检查，减少运行时错误
- **跨平台**：单二进制部署，支持 Windows/macOS/Linux
- **性能**：高效的内存管理和 GC

### 5.2 为什么拆分为四个模块？

- **灵活采用**：用户可以只使用需要的层
- **独立演进**：每层可以独立升级而不影响其他层
- **清晰边界**：职责分离，易于理解和维护

### 5.3 为什么使用事件流？

- **实时反馈**：流式传输提供即时响应
- **可观测性**：事件记录便于调试和监控
- **可扩展**：通过订阅机制添加新功能

## 六、Tool 与 Skill 分层设计

### 6.1 概念区分

Crux 采用双层设计来分离执行逻辑和业务能力：

| 维度 | **Tool (工具)** | **Skill (技能)** |
|------|-----------------|------------------|
| **定位** | 核心执行层 | 业务编排层 |
| **位置** | `crux-agent-runtime` | `crux-agent-harness/skills` |
| **职责** | 执行具体操作 | 定义能力边界 |
| **形式** | 可执行函数 | 声明式描述 |
| **接口** | `AgentTool` | 文本描述（SKILL.md） |
| **加载方式** | 代码注册 | Markdown 文件加载 |

### 6.2 架构层次

```
┌─────────────────────────────────────────────────────┐
│                    Skill Layer                      │
│     (业务能力定义: SKILL.md → 系统提示词)            │
│                    ↓                                │
├─────────────────────────────────────────────────────┤
│                    Tool Layer                       │
│     (执行逻辑: AgentTool → 工具调用执行)             │
│                    ↓                                │
├─────────────────────────────────────────────────────┤
│                  Agent Runtime                      │
│     (循环调度: AgentLoop → 事件流)                   │
└─────────────────────────────────────────────────────┘
```

### 6.3 工作流程

1. **Skill 加载阶段**：
   - 从 `SKILL.md` 文件读取技能定义
   - 将技能描述注入系统提示词
   - LLM 了解可用能力范围

2. **Tool 执行阶段**：
   - LLM 生成工具调用请求
   - AgentLoop 验证工具存在性
   - 执行工具并返回结果

3. **协作模式**：
   - Skill 定义"能做什么"（面向 LLM）
   - Tool 实现"怎么做"（面向系统）
   - 两者通过工具名称进行匹配

### 6.4 典型示例

**Skill 定义**（SKILL.md）：
```markdown
## 代码执行
- 名称: bash
- 描述: 在终端执行 shell 命令
- 参数:
  - command: 要执行的命令
```

**Tool 实现**（runtime）：
```go
type BashTool struct{}

func (t *BashTool) Name() string { return "bash" }
func (t *BashTool) Call(ctx context.Context, args map[string]any) (any, error) {
    // 执行命令逻辑
}
```

## 七、扩展点

### 7.1 添加新供应商

1. 实现 `core.APIProvider` 接口
2. 通过 `core.RegisterProvider()` 注册
3. 在 `core/env.go` 添加环境变量映射

### 7.2 添加新工具

在 `AgentLoopConfig.Tools` 中添加 `AgentTool` 定义

### 7.3 自定义压缩策略

实现 `context.Compactor` 接口并配置到 `Pipeline`

### 7.4 自定义审批规则

通过 `approval.Gate.AddRule()` 添加规则

## 八、项目结构

```
crux/
├── crux-ai/                       # 核心AI客户端
│   ├── core/                      # 类型、环境、注册表
│   ├── ai/                        # 流式入口点
│   ├── providers/<vendor>/        # 供应商适配器
│   ├── testenv/                   # 测试辅助工具
│   └── cmd/                       # CLI演示
│
├── crux-agent-runtime/            # Agent循环
│   ├── agent/                     # Agent、AgentLoop、事件类型
│   └── cmd/                       # 演示二进制
│
├── crux-agent-harness/            # 可选的Harness组件
│   ├── token/                     # Tiktoken计数
│   ├── context/                   # 预算 + 管道 + 压缩器
│   ├── approval/                  # 规则型网关
│   ├── checkpoint/                # 撤销/重做快照
│   ├── session/                   # JSONL会话树
│   ├── observe/                   # JSON行日志
│   ├── prompt/                    # 系统提示词构建器
│   └── skills/                    # SKILL.md加载器
│
└── crux-agent-tui/               # TUI终端界面
    ├── main.go                    # REPL循环
    ├── internal/app/              # 应用逻辑
    └── tui/                       # TUI渲染
```

## 九、测试策略

| 模块 | 测试命令 | 说明 |
|------|----------|------|
| crux-ai | `go test ./...` | 部分包有集成测试（`//go:build integration`） |
| crux-agent-runtime | `go test ./...` | 单元测试 |
| crux-agent-harness | `go test ./...` | 单元测试 |
| crux-agent-tui | `go build ./...` | 无测试（设计如此） |

**测试工具**：
- `crux-ai/providers/faux`：模拟供应商，用于集成测试而不消耗真实 token

## 十、配置管理

### 10.1 环境变量

| 变量 | 用途 |
|------|------|
| `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` / `DEEPSEEK_API_KEY` | 供应商 API 密钥 |
| `AI_PROVIDER` | 强制指定供应商 |
| `AI_MODEL` | 覆盖模型 ID |
| `AI_BASE_URL` | 覆盖 API 基础 URL |
| `AI_MAX_TOKENS` | 最大输出 token 数 |
| `AI_TEMPERATURE` | 采样温度 |

### 10.2 模型配置

模型通过 `crux-ai/ai/models_generated.go` 中的 `GeneratedModels()` 函数注册，包含：
- 模型 ID 和名称
- API 协议类型
- 供应商标识
- 支持的输入模态
- 定价信息
- 上下文窗口大小
- 兼容性标志（`Compat`）

## 十一、性能考虑

- **Token 计数**：使用 Tiktoken 进行准确的 token 估算
- **上下文压缩**：自动管理上下文窗口，避免超限
- **并发工具执行**：并行执行独立工具调用
- **流式传输**：减少延迟，提供实时反馈
- **连接池**：HTTP 客户端复用连接

## 十二、安全考虑

- **API 密钥管理**：通过环境变量或动态回调解析
- **审批网关**：规则型工具执行审批
- **错误处理**：优雅的错误传播和恢复
- **资源限制**：超时和取消支持