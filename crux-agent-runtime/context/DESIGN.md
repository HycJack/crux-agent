# 模块设计：Context 上下文窗口管理

> 模块: crux-agent-runtime/context
> 版本: v0.1.0 | 更新: 2026-06-17
> 状态: ✅ 已完成

---

## 1. 职责

Token 计数、上下文窗口压缩、自动触发压缩。

**核心能力**：
- 多种 Token 计数策略
- 4 种压缩策略
- 自动触发压缩
- 上下文管理器（自动管理）

## 2. 架构

```
Manager (上下文管理器)
  │
  ├── TokenCounter (接口)
  │     └── DefaultTokenCounter  # ~4 chars/token
  │
  ├── Compactor (接口)
  │     ├── SlideWindow          # 滑动窗口
  │     ├── LLMSummarize         # LLM 摘要
  │     ├── ChainedCompactor     # 链式组合
  │     └── ContextWindowCompactor # Token 感知
  │
  └── ContextWindowConfig       # 配置
```

## 3. 核心类型

### TokenCounter

```go
type TokenCounter func(systemPrompt string, messages []core.Message, tools []core.Tool) int
```

### ContextWindowConfig

```go
type ContextWindowConfig struct {
    MaxTokens     int          // 最大 token 数（默认 128000）
    ReserveTokens int          // 预留给响应（默认 4096）
    MinMessages   int          // 压缩后最少消息数（默认 4）
    TokenCounter  TokenCounter // 自定义计数器
}
```

### Compactor 接口

```go
type Compactor interface {
    Compact(ctx context.Context, msgs []core.Message) ([]core.Message, bool, error)
    Name() string
}
```

## 4. 压缩策略对比

| 策略 | LLM 调用 | 信息保留 | 性能 | 使用场景 |
|------|----------|----------|------|----------|
| **SlideWindow** | ❌ | 低 | 最快 | 高吞吐、低延迟 |
| **LLMSummarize** | ✅ | 高 | 慢 | 长对话、重要上下文 |
| **ChainedCompactor** | 可选 | 灵活 | 中等 | 组合策略 |
| **ContextWindowCompactor** | 可选 | 自动 | 中等 | 自动管理 |

### SlideWindow

```go
compactor := NewSlideWindow(50)  // 保留最后 50 条消息
```

**逻辑**：
```
[msg1, msg2, ..., msg100] → [msg51, msg52, ..., msg100]
```

### LLMSummarize

```go
compactor := NewLLMSummarize()
compactor.KeepLast = 10      // 保留最后 10 条
compactor.MinTrigger = 30    // 至少 30 条才触发
compactor.Summarize = func(ctx context.Context, dropped []core.Message) (string, error) {
    return llm.CompleteSimple(ctx, model, dropped)
}
```

**逻辑**：
```
[msg1, ..., msg100] 
  → 摘要: "用户询问天气，助手回复..."
  → [摘要, msg91, msg92, ..., msg100]
```

### ChainedCompactor

```go
compactor := &ChainedCompactor{
    Compactors: []Compactor{
        NewLLMSummarize(),   // 先尝试 LLM 摘要
        NewSlideWindow(50),  // 失败则滑动窗口
    },
}
```

**逻辑**：
```
策略1.Compact() → 成功？返回
策略2.Compact() → 成功？返回
原始消息（无变化）
```

### ContextWindowCompactor

```go
config := ContextWindowConfig{
    MaxTokens:     1000,
    ReserveTokens: 200,
    MinMessages:   2,
}
compactor := NewContextWindowCompactor(config, NewSlideWindow(5))
```

**逻辑**：
```
Token > MaxTokens - ReserveTokens?
  │
  ├─ Yes → 触发内部 compactor
  └─ No  → 返回原消息
```

## 5. Token 估算策略

```go
func estimateStringTokens(s string) int {
    cjkCount := 0
    otherCount := 0
    for _, r := range s {
        if r >= 0x4E00 && r <= 0x9FFF {
            cjkCount++  // CJK 字符
        } else {
            otherCount++ // ASCII 字符
        }
    }
    // CJK: ~1.5 字符/token, ASCII: ~4 字符/token
    return cjkCount*2/3 + otherCount/4
}
```

**精度说明**：
- 粗略估算，误差 ±20%
- 精确计数需要 tiktoken 等库
- CJK 字符按 1.5 字符/token 计算
- ASCII 字符按 4 字符/token 计算

## 6. Manager（上下文管理器）

```go
type Manager struct {
    config    ContextWindowConfig
    compactor Compactor
    counter   TokenCounter
    messages  []core.Message
    totalTokens int
    compactions int
}
```

### 核心功能

```go
// 创建管理器
mgr := NewManager(config)
mgr.SetCompactor(NewSlideWindow(50))
mgr.LoadFromSession(session)

// 添加消息（自动触发压缩）
mgr.AddMessage(msg)

// 获取当前上下文
ctx := mgr.GetContext()
msgs := mgr.GetMessages()

// 监控
stats := mgr.GetStats()
// {TotalTokens, MessageCount, Compactions, MaxTokens, UsagePercent}

mgr.IsNearLimit(0.8)  // 是否接近限制
```

### Stats

```go
type Stats struct {
    TotalTokens     int
    MessageCount    int
    Compactions     int
    MaxTokens       int
    AvailableTokens int
    UsagePercent    float64
}
```

## 7. 压缩流程

```
Manager.AddMessage(msg)
  │
  ├─ 更新 token 计数
  │
  └─ CompactIfNeeded()
       │
       ├─ NeedsCompaction? (token > softLimit)
       │    └─ No → 返回
       │
       └─ Yes → Compactor.Compact()
            │
            ├─ SlideWindow: 保留最后 N 条
            ├─ LLMSummarize: 生成摘要 + 保留最后 N 条
            └─ ChainedCompactor: 按顺序尝试
```

## 8. 集成点

### 与 Agent Loop

```go
config.TransformContext = func(msgs []core.Message) []core.Message {
    for _, m := range msgs {
        ctxMgr.AddMessage(m)
    }
    return ctxMgr.GetMessages()
}
```

### 与 Session

```go
ctxMgr.LoadFromSession(sess)
// 从 session 重建上下文，自动计算 token
```

### 与 Memory

```go
memPrompt := mem.FormatForPrompt(ctx, "", 0)
ctxMgr.SetSystemPrompt(basePrompt + "\n\n" + memPrompt)
```

## 9. 测试策略

| 测试类型 | 测试内容 |
|----------|----------|
| Token 计数 | 中英文混合、空消息、超长消息 |
| 滑动窗口 | 边界条件、空消息、单条消息 |
| LLM 摘要 | 触发条件、摘要生成、错误处理 |
| 链式压缩 | 策略顺序、失败回退 |
| Manager | 添加消息、自动压缩、统计 |

## 10. 后续计划

- [ ] tiktoken 精确计数
- [ ] 按消息类型加权
- [ ] 压缩历史记录
- [ ] 异步压缩
