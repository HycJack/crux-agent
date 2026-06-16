# 模块设计：AutoLearn 自动学习

> 模块: crux-agent-runtime/autolearn
> 版本: v0.1.0 | 更新: 2026-06-17
> 状态: ✅ 已完成

---

## 1. 职责

从对话中自动提取可记忆的事实，写入 memory。

**核心能力**：
- 4 种触发源：显式标记、工具结果、自然语言、LLM 提取
- Key 白名单过滤（防止 LLM 幻觉）
- 增量去重（同 key 只更新一次）
- 可禁用（Settings.AutoLearn = false）

## 2. 架构

```
AutoLearner
  │
  ├── MemoryProvider (接口)  ← 注入
  │
  ├── 触发源
  │     ├── ExtractFromUserInput()     # 显式标记
  │     ├── ExtractFromToolResult()    # 工具结果
  │     ├── ExtractFromNaturalLanguage() # 自然语言
  │     └── LLMSimpleExtractor         # LLM 提取
  │
  └── apply(triggers) → MemoryProvider.Store()
```

## 3. 核心类型

### Settings

```go
type Settings struct {
    AutoLearn     bool    // 启用 LLM 提取（默认 false）
    ExtractEveryN int     // 每 N 轮触发（默认 5）
    MinConfidence float64 // 置信度阈值（默认 0.7）
}
```

### Trigger

```go
type Trigger struct {
    Source  TriggerSource  // "user", "tool", "extract"
    Key     string
    Value   string
    Context string
    Time    time.Time
}
```

### Extractor

```go
type Extractor interface {
    Extract(ctx context.Context, messages []core.Message) ([]Trigger, error)
}
```

## 4. 触发源对比

| 触发源 | 延迟 | 准确性 | 成本 | 使用场景 |
|--------|------|--------|------|----------|
| **显式标记** | 无 | 100% | 无 | [remember:key=val] |
| **工具结果** | 无 | 100% | 无 | REMEMBER:key=val |
| **自然语言** | 无 | ~80% | 无 | "我叫张三" |
| **LLM 提取** | 高 | ~90% | 有 | 每 N 轮触发 |

## 5. 自然语言提取规则

```go
"你叫小七"          → assistant.name=小七
"你的名字叫小七"    → assistant.name=小七
"你是小七"          → assistant.name=小七
"我叫张三"          → user.name=张三
"我的名字叫张三"    → user.name=张三
"我来自杭州"        → user.location=杭州
"请用中文回答"      → user.preferred_language=中文
```

## 6. LLM 提取 Key 白名单

```
user.*          — 用户信息
assistant.*     — AI 信息
task.*          — 当前任务
project.*       — 项目信息
fact.*          — 关键事实
decision.*      — 已做决策
constraint.*    — 约束条件
relation.*      — 关系
family.*        — 家庭
pet.*           — 宠物
health.*        — 健康
diet.*          — 饮食
date.*          — 日期
asset.*         — 资产
style.*         — 风格
tool.*          — 工具
goal.*          — 目标
pain.*          — 痛点
```

## 7. 提取流程

```
ProcessUserInput(text)
  │
  ├─ ExtractFromUserInput(text)
  │    └─ 正则匹配 [remember:key=val]
  │
  ├─ ExtractFromNaturalLanguage(text)
  │    └─ 正则匹配 "我叫xxx", "你叫xxx" 等
  │
  └─ apply(triggers)
       ├─ 过滤空 key/value
       ├─ MemoryProvider.Store(ctx, Entry{...})
       └─ 去重
```

## 8. LLM 提取流程

```
MaybeExtract(ctx, messages, extractor)
  │
  ├─ 检查 AutoLearn 是否启用
  ├─ 检查 counter % ExtractEveryN == 0
  │
  └─ extractor.Extract(ctx, messages)
       │
       ├─ 构造 prompt（含 key 白名单）
       ├─ 调用 LLM
       ├─ 解析输出（KEY=VALUE 格式）
       ├─ 白名单过滤
       └─ 去重
```

## 9. 集成点

### 与 Agent Loop

```go
config.ConvertToLlm = func(msgs []core.Message) []core.Message {
    for _, m := range msgs {
        if um, ok := m.(core.UserMessage); ok {
            learner.ProcessUserInput(fmt.Sprintf("%v", um.Content))
        }
    }
    return msgs
}

config.OnEvent = func(e agent.AgentEvent) {
    if ev, ok := e.(agent.EventToolExecEnd); ok {
        learner.ProcessToolResult(string(ev.Result))
    }
}
```

### 与 Memory

```go
// AutoLearn 使用 MemoryProvider 接口
mem, _ := memory.New("./memory.json")
learner := autolearn.New(mem, settings)

// 或使用向量存储
vecMem := memory.NewVectorStore(client, embedder, "memory")
learner := autolearn.New(vecMem, settings)
```

### 与 Session

```go
// AutoLearn 独立于 session
// Session 记录对话历史
// AutoLearn 提取长期记忆
```

## 10. 测试策略

| 测试类型 | 测试内容 |
|----------|----------|
| 自然语言提取 | 用户名、地点、语言偏好 |
| 显式标记 | [remember:key=val] |
| 工具结果 | REMEMBER:key=val |
| LLM 提取 | 白名单过滤、去重 |
| 集成 | ProcessUserInput + MemoryProvider |

## 11. 后续计划

- [ ] LLM 提取置信度过滤
- [ ] 记忆冲突检测
- [ ] 记忆衰减（长时间未提及权重降低）
- [ ] 记忆分类优化（自动推断 category）
