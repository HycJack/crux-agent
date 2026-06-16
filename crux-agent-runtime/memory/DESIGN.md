# 模块设计：Memory 长期记忆

> 模块: crux-agent-runtime/memory
> 版本: v0.1.0 | 更新: 2026-06-17
> 状态: ✅ 已完成

---

## 1. 职责

跨会话的 KV 存储，持久化用户偏好、关键事实、任务进度。

**关键属性**：
- 简单 KV 接口（Get/Set/Delete/Has/Keys）
- JSON 文件持久化（原子写入）
- 分类支持（user, task, preference 等）
- 格式化为 system prompt（FormatForPrompt）

## 2. 设计原则

1. **简单优先** — 不用数据库，JSON 文件足够
2. **原子写入** — temp 文件 + rename，避免数据损坏
3. **分类管理** — 按 category 分组，便于查询
4. **变化检测** — Hash() 用于判断是否需要重建 prompt

## 3. 核心类型

```go
type Memory struct {
    path string
    data map[string]Entry
}

type Entry struct {
    Value     string    `json:"value"`
    CreatedAt time.Time `json:"createdAt"`
    UpdatedAt time.Time `json:"updatedAt"`
    Category  string    `json:"category,omitempty"`
}
```

## 4. 存储格式

```json
{
  "user.name": {
    "value": "小明",
    "createdAt": "2024-01-01T00:00:00Z",
    "updatedAt": "2024-01-01T00:00:00Z",
    "category": "user"
  },
  "task.current": {
    "value": "开发 demo",
    "createdAt": "2024-01-01T00:00:00Z",
    "updatedAt": "2024-01-01T00:00:00Z",
    "category": "task"
  }
}
```

## 5. FormatForPrompt 输出

```
# Long-term Memory

- task.current: 开发 demo
- user.name: 小明
- user.preferred_language: 中文
```

按分类分组，每组内按 key 排序。

## 6. 集成点

### 与 Agent Loop

```go
// 注入到 system prompt
config.SystemPrompt = basePrompt + "\n\n" + mem.FormatForPrompt()
```

### 与 AutoLearn

```go
learner := autolearn.New(mem, settings)
learner.ProcessUserInput("我叫小明")
// 自动调用 mem.Set("user.name", "小明")
```

### 与 Session

```go
// Memory 独立于 session
// session 管理对话历史，memory 管理长期事实
```

## 7. 后续计划

- [ ] 支持加密存储（敏感信息）
- [ ] 条目过期策略（TTL）
- [ ] 导出/导入功能
- [ ] SQLite 存储后端（大量条目时）
