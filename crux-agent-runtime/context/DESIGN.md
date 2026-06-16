# 模块设计：Context 上下文窗口管理

> 模块: crux-agent-runtime/context
> 版本: v0.1.0 | 更新: 2026-06-17
> 状态: ✅ 已完成

---

## 1. 职责

Token 计数、上下文窗口压缩、自动触发压缩。

**关键属性**：
- 基于字符的 token 粗略估算（~4 chars/token）
- 多种压缩策略（滑动窗口、LLM 摘要、链式）
- 软限制触发压缩，硬限制强制截断
- 与 session 集成，自动重建上下文

## 2. 设计原则

1. **策略模式** — Compactor 接口，可替换压缩策略
2. **链式组合** — ChainedCompactor 按顺序尝试多个策略
3. **增量统计** — TokenStats 只计算新增消息
4. **自动触发** — Manager 在 AddMessage 时自动检查是否需要压缩

## 3. 核心类型

```go
// Token 计数函数
type TokenCounter func(systemPrompt string, messages []core.Message, tools []core.Tool) int

// 压缩策略接口
type Compactor interface {
    Compact(ctx context.Context, msgs []core.Message) ([]core.Message, bool, error)
    Name() string
}

// 上下文窗口配置
type ContextWindowConfig struct {
    MaxTokens     int  // 最大 token 数
    ReserveTokens int  // 预留给响应的 token
    MinMessages   int  // 压缩后最少保留的消息数
    TokenCounter  TokenCounter
}
```

## 4. 压缩策略对比

| 策略 | LLM 调用 | 信息保留 | 性能 | 使用场景 |
|------|----------|----------|------|----------|
| SlideWindow | ❌ | 低（丢弃旧消息）| 最快 | 高吞吐、低延迟 |
| LLMSummarize | ✅ | 高（生成摘要）| 慢 | 长对话、重要上下文 |
| ChainedCompactor | 可选 | 灵活 | 中等 | 组合策略 |
| ContextWindowCompactor | 可选 | 自动 | 中等 | 自动管理 |

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

**注意**：这是粗略估算。精确计数需要使用 tiktoken 等库。

## 6. 压缩流程

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

## 7. Manager 统计

```go
type Stats struct {
    TotalTokens     int
    MessageCount    int
    Compactions     int
    MaxTokens       int
    AvailableTokens int
    UsagePercent    float64
}

mgr.GetStats()
// {
//   TotalTokens: 50000,
//   MessageCount: 100,
//   Compactions: 3,
//   MaxTokens: 128000,
//   AvailableTokens: 123904,
//   UsagePercent: 40.3
// }
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
// Memory 格式化为 prompt 后，也计入 token
memPrompt := mem.FormatForPrompt()
ctxMgr.SetSystemPrompt(basePrompt + "\n\n" + memPrompt)
```

## 9. 后续计划

- [ ] 支持 tiktoken 精确计数
- [ ] 按消息类型加权（工具结果 token 更多）
- [ ] 压缩历史记录（记录每次压缩的摘要）
