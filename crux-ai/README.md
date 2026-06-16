# crux-ai

[![Go Reference](https://pkg.go.dev/badge/github.com/hycjack/crux-ai.svg)](https://pkg.go.dev/github.com/hycjack/crux-ai)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.23+-blue.svg)](https://golang.org)

crux-ai 是一个 Go 语言的 LLM 客户端抽象库，基于 [pi-ai-go](https://github.com/) 迁移并吸收了优秀的架构设计。

## ✨ 特性

- 🔄 **统一接口** - 一套 API 访问 12+ LLM 提供商
- 🎯 **角色路由** - 根据任务类型自动选择最佳模型
- 🛡️ **类型安全** - `MessageRole` 编译时检查
- ⚡ **流式响应** - 完整的事件流支持
- 🔌 **易于扩展** - 简单的接口添加新提供商
- 🧠 **扩展思考** - 支持 DeepSeek-R1、Claude Thinking 等推理模型
- 🛠️ **内部工具** - 8 个专用工具包（SSE、JSON、验证等）
- 📦 **公共 API 层** - `llm/` 包提供简洁接口
- 🌐 **双语文档** - 中英文注释
- 🔁 **自动重试** - 智能退避策略

## 📦 支持的 Provider

| Provider | 包名 | 说明 |
|----------|------|------|
| OpenAI | `providers/openai` | GPT-4o, GPT-4.1, o3, o4-mini |
| Anthropic | `providers/anthropic` | Claude 3.5, Claude 4 系列 |
| Google | `providers/google` | Gemini 2.0, Gemini 2.5 |
| AWS Bedrock | `providers/bedrock` | AWS Bedrock 服务 |
| Mistral | `providers/mistral` | Mistral AI |
| DeepSeek | `providers/deepseek` | DeepSeek 系列 |
| Kimi | `providers/kimi` | 月之暗面 Kimi |
| GLM | `providers/glm` | 智谱 GLM |
| 小米 | `providers/xiaomi` | 小米 MiMo |
| OpenRouter | `providers/openrouter` | OpenRouter 聚合 |
| Faux | `providers/faux` | Mock Provider（测试）|

## 🚀 快速开始

### 安装

```bash
go get github.com/hycjack/crux-ai
```

### 基本用法

```go
package main

import (
    "context"
    "fmt"

    "github.com/hycjack/crux-ai/core"
    "github.com/hycjack/crux-ai/llm"

    // 导入需要的 Provider（触发自动注册）
    _ "github.com/hycjack/crux-ai/providers/openai"
)

func main() {
    ctx := context.Background()

    // 1. 获取模型
    model, err := llm.GetModel(core.ProviderOpenAI, "gpt-4o")
    if err != nil {
        panic(err)
    }

    // 2. 构建消息
    messages := []core.Message{
        core.UserMessage{
            Role:      core.MessageRoleUser,
            Content:   "你好！",
            Timestamp: time.Now(),
        },
    }

    // 3. 流式调用
    stream, err := llm.Stream(ctx, model, messages, core.StreamOptions{
        APIKey: "your-api-key",
    })
    if err != nil {
        panic(err)
    }

    // 4. 处理事件流
    result, err := stream.ForEach(ctx, func(evt core.AssistantMessageEvent) error {
        switch e := evt.(type) {
        case core.EventTextDelta:
            fmt.Print(e.Delta)
        case core.EventThinkingDelta:
            fmt.Printf("[thinking] %s", e.Delta)
        }
        return nil
    })
    if err != nil {
        panic(err)
    }

    fmt.Printf("\nTokens: %d in / %d out\n", result.Usage.Input, result.Usage.Output)
}
```

### 角色路由（推荐）

根据任务类型自动选择最佳模型：

```go
package main

import (
    "github.com/hycjack/crux-ai/core"
    "github.com/hycjack/crux-ai/router"
)

func main() {
    // 配置角色到模型的映射
    cfg := router.Config{
        Default: router.ModelSpec{
            ID:       "gpt-4o",
            Provider: core.ProviderOpenAI,
            API:      core.APIOpenAICompletions,
        },
        Smol: router.ModelSpec{
            ID:       "gpt-4o-mini",
            Provider: core.ProviderOpenAI,
            API:      core.APIOpenAICompletions,
        },
        Slow: router.ModelSpec{
            ID:       "claude-opus-4-5",
            Provider: core.ProviderAnthropic,
            API:      core.APIAnthropicMessages,
        },
        Plan: router.ModelSpec{
            ID:       "o3",
            Provider: core.ProviderOpenAI,
            API:      core.APIOpenAICompletions,
        },
    }

    // 创建路由器
    r, _ := router.New(cfg)

    // 根据任务类型选择模型
    messages := []core.Message{...}

    // 快速任务用小模型
    resp, _ := r.Stream(ctx, router.RoleSmol, messages, opts)

    // 复杂任务用大模型
    resp, _ = r.Stream(ctx, router.RoleSlow, messages, opts)

    // 推理任务用推理模型
    resp, _ = r.Stream(ctx, router.RolePlan, messages, opts)
}
```

### 工具调用

```go
// 定义工具
tools := []core.Tool{
    {
        Name:        "get_weather",
        Description: "获取指定城市的天气",
        Parameters: json.RawMessage(`{
            "type": "object",
            "properties": {
                "city": {"type": "string", "description": "城市名称"}
            },
            "required": ["city"]
        }`),
    },
}

// 构建请求
llmCtx := core.Context{
    SystemPrompt: "你是一个有帮助的助手",
    Messages:     messages,
    Tools:        tools,
}

// 调用（带工具）
result, err := provider.Stream(ctx, model, llmCtx, opts)
```

### 错误处理

```go
result, err := llm.Complete(ctx, model, messages, opts)
if err != nil {
    log.Printf("调用失败: %v", err)
    return
}

// 检查是否包含特定错误
// 重试逻辑由 retry.go 自动处理
```

## 📁 项目结构

```
crux-ai/
├── core/           # 核心类型（零依赖）
│   ├── types.go        # Message, Model, KnownAPI 等
│   ├── errors.go       # 错误类型
│   ├── events.go       # 事件流
│   ├── registry.go     # Provider 注册
│   ├── retry.go        # 重试机制
│   └── ...
├── llm/            # 公共 API 层
│   ├── api.go          # Stream, Complete
│   └── models.go       # 模型管理
├── providers/      # 12+ Provider 实现
│   ├── openai/
│   ├── anthropic/
│   └── ...
├── internal/       # 8 个内部工具包
│   ├── sse/
│   ├── jsonparse/
│   └── ...
├── router/         # 角色路由系统
├── testenv/        # 测试环境
├── cmd/            # CLI 工具
└── cruxai.go       # Facade 包
```

## 🏗️ 架构设计

### 分层架构

```
┌─────────────────────────────────┐
│  业务代码 (使用方)                 │
└────────────┬────────────────────┘
             │
┌────────────▼────────────────────┐
│  router/ (角色路由)               │
└────────────┬────────────────────┘
             │
┌────────────▼────────────────────┐
│  llm/ (公共 API)                  │
└────────────┬────────────────────┘
             │
┌────────────▼────────────────────┐
│  core/ (核心类型)                  │
│  - MessageRole (类型安全)         │
│  - Model, Context, StreamOptions  │
└────────────┬────────────────────┘
             │
┌────────────▼────────────────────┐
│  providers/ (实现)                │
│  - 12+ Provider                   │
│  - internal/ (工具支持)            │
└─────────────────────────────────┘
```

### 核心原则

1. **类型安全** - MessageRole 编译时检查
2. **并发安全** - Provider 必须是 goroutine-safe
3. **零循环依赖** - 单向依赖关系
4. **易于测试** - Mock Provider (Faux)
5. **易于扩展** - 简单接口添加新 Provider

## 📚 文档

- [AGENTS.md](AGENTS.md) - 项目结构详细说明
- [pkg.go.dev](https://pkg.go.dev/github.com/hycjack/crux-ai) - API 文档

## 🧪 测试

```bash
# 运行所有测试
go test ./...

# 运行特定包
go test ./core/
go test ./router/

# 查看覆盖率
go test ./... -cover

# 运行示例
XIAOMI_API_KEY=xxx go run ./cmd/xiaomi-agent
```

## 🤝 贡献

欢迎贡献！添加新 Provider 的步骤：

1. 在 `providers/` 下创建子包
2. 实现 `APIProvider` 接口
3. 添加单元测试
4. 在 `providers/register.go` 中注册

## 📝 版本

当前版本: `v0.0.1`

通过 `core.Version` 常量访问。

## 📄 许可证

MIT License - 详见 [LICENSE](LICENSE) 文件

## 🙏 致谢

- [pi-ai-go](https://github.com/) - 原始项目，提供核心架构
- [oh-my-pi](https://github.com/can1357/oh-my-pi) - 重试策略参考
- [crux/crux/crux-ai](file:///mnt/workspace/crux/crux/crux-ai) - 借鉴的优秀设计
