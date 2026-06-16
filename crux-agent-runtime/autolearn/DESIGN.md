# 模块设计：AutoLearn 自动学习

> 模块: crux-agent-runtime/autolearn
> 版本: v0.1.0 | 更新: 2026-06-17
> 状态: ✅ 已完成

---

## 1. 职责

从对话中自动提取可记忆的事实，写入 memory。

**关键属性**：
- 4 种触发源：显式标记、工具结果、自然语言、LLM 提取
- Key 白名单过滤（防止 LLM 幻觉）
- 增量去重（同 key 只更新一次）
- 可禁用（Settings.AutoLearn = false）

## 2. 设计原则

1. **多源触发** — 支持显式标记、工具输出、自然语言、LLM 提取
2. **异步执行** — LLM 提取不阻塞主对话
3. **白名单过滤** — LLM 输出必须在允许的 key 前缀内
4. **可禁用** — Settings.AutoLearn = false 关闭 LLM 提取

## 3. 触发源对比

| 触发源 | 延迟 | 准确性 | 成本 | 使用场景 |
|--------|------|--------|------|----------|
| 显式标记 | 无 | 100% | 无 | 用户主动 [remember:key=val] |
| 工具结果 | 无 | 100% | 无 | 工具输出 REMEMBER:key=val |
| 自然语言 | 无 | ~80% | 无 | "我叫张三" → user.name=张三 |
| LLM 提取 | 高 | ~90% | 有 | 每 N 轮异步提取 |

## 4. 自然语言提取规则

```go
// 支持的模式
"你叫小七"          → assistant.name=小七
"你的名字叫小七"    → assistant.name=小七
"你是小七"          → assistant.name=小七
"我叫张三"          → user.name=张三
"我的名字叫张三"    → user.name=张三
"我来自杭州"        → user.location=杭州
"请用中文回答"      → user.preferred_language=中文
```

## 5. LLM 提取 Key 白名单

```
A. user.*          — 用户信息
B. assistant.*     — AI 信息
C. task.*          — 当前任务
D. project.*       — 项目信息
E. fact.*          — 关键事实
F. decision.*      — 已做决策
G. constraint.*    — 约束条件
H. relation.*      — 关系
I. family.*        — 家庭
J. pet.*           — 宠物
K. health.*        — 健康
L. diet.*          — 饮食
M. date.*          — 日期
N. asset.*         — 资产
O. style.*         — 风格
P. tool.*          — 工具
Q. goal.*          — 目标
R. pain.*          — 痛点
```

## 6. 提取流程

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
       ├─ mem.SetWithCategory(key, value, source)
       └─ mem.Save()
```

## 7. LLM 提取流程

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

## 8. 集成点

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
mem, _ := memory.New("./memory.json")
learner := autolearn.New(mem, settings)
// learner.ProcessUserInput() 自动调用 mem.Set()
```

### 与 Session

```go
// AutoLearn 独立于 session
// session 记录对话历史，autolearn 提取长期记忆
```

## 9. 后续计划

- [ ] LLM 提取的置信度过滤
- [ ] 记忆冲突检测（同 key 不同 value）
- [ ] 记忆衰减（长时间未提及的记忆权重降低）
- [ ] 记忆分类优化（自动推断 category）
