# 模块设计：Session Branch 会话分支

> 模块: crux-agent-runtime/session
> 版本: v0.2.0 | 更新: 2026-06-17
> 状态: ⏳ 待实现
> 参考: [crux-harness/branch](file:///mnt/workspace/crux/crux/crux-harness/branch)

---

## 1. 问题

当前 Session 是线性的，无法支持：
- **会话分支** — 用户在某个消息处"分叉"，探索不同方向
- **分支摘要** — 分支时生成摘要，保留被放弃路径的上下文
- **分支切换** — 用户在不同分支间来回切换
- **分支可视化** — 展示会话的树状结构

### 典型场景

```
用户: 如何优化这个函数？
助手: 方案A: 使用缓存
用户: 试试方案A
助手: 实现如下...
     ┌─────────────────────────────────────┐
     │ 这时用户想探索另一个方向              │
     └─────────────────────────────────────┘
用户: 等等，试试方案B: 并行化
     └── 分支: 从"如何优化这个函数？"处创建新分支
         旧分支摘要: "用户探索了方案A（缓存），实现了..."
```

---

## 2. 解决方案

### 核心概念

```
Session (会话)
  │
  ├── Branch (分支)
  │     ├── main (主分支)
  │     │     └── messages: [m1, m2, m3, m4, m5]
  │     │
  │     └── fork-1 (分支1, 从 m3 处分叉)
  │           ├── summary: "分支摘要..."
  │           └── messages: [m1, m2, m3, m6, m7]
  │
  └── CurrentBranch (当前活跃分支)
```

### 分支模型

```go
// Branch 代表一个会话分支
type Branch struct {
    ID        string            `json:"id"`
    Name      string            `json:"name"`      // "main", "fork-1", etc.
    ParentID  string            `json:"parent_id"` // 分叉点的消息 ID
    Summary   string            `json:"summary"`   // 分支摘要
    Messages  []SessionTreeEntry `json:"messages"`
    CreatedAt time.Time         `json:"created_at"`
    Metadata  map[string]string `json:"metadata,omitempty"`
}

// BranchConfig 分支配置
type BranchConfig struct {
    MaxBranches     int           // 最大分支数（默认 10）
    AutoSummary     bool          // 自动摘要（默认 true）
    SummaryFunc     SummaryFunc   // 自定义摘要函数
    TruncateLength  int           // 截断摘要长度（默认 500）
}
```

---

## 3. 核心接口

### SummaryFunc（摘要函数）

```go
// SummaryFunc 生成分支摘要
// sourceTitle: 源会话标题
// messages: 被放弃的消息
// 返回: 摘要文本
type SummaryFunc func(ctx context.Context, sourceTitle string, messages []SessionTreeEntry) (string, error)
```

### 扩展 Session

```go
// Session 扩展
type Session struct {
    // ... 现有字段
    
    branches       map[string]*Branch  // 分支映射
    currentBranch  string              // 当前分支 ID
    branchConfig   BranchConfig        // 分支配置
}

// Fork 从当前消息处分叉，创建新分支
func (s *Session) Fork(ctx context.Context, name string) (*Branch, error)

// SwitchTo 切换到指定分支
func (s *Session) SwitchTo(branchID string) error

// Branches 返回所有分支
func (s *Session) Branches() []*Branch

// CurrentBranch 返回当前分支
func (s *Session) CurrentBranch() *Branch

// DeleteBranch 删除分支（不能删除当前分支）
func (s *Session) DeleteBranch(branchID string) error

// MergeBranch 将分支合并回主分支
func (s *Session) MergeBranch(ctx context.Context, branchID string) error
```

---

## 4. 分支流程

### 4.1 创建分支（Fork）

```
Session.Fork("探索方案B")
  │
  ├─ 1. 找到分叉点（当前消息）
  │
  ├─ 2. 生成分支摘要
  │     │
  │     ├─ SummaryFunc != nil?
  │     │   └─ Yes → SummaryFunc(ctx, title, messages)
  │     │   └─ No  → truncateSummary(messages)
  │     │
  │     └─ 摘要格式：
  │         ## Branch summary
  │         - [user] 如何优化这个函数？
  │         - [assistant] 方案A: 使用缓存...
  │         - [user] 试试方案A
  │
  ├─ 3. 创建新 Branch
  │     Branch{
  │         ID: "fork-1",
  │         Name: "探索方案B",
  │         ParentID: "msg-3",
  │         Summary: summary,
  │         Messages: forkedMessages,
  │     }
  │
  ├─ 4. 在新分支注入摘要消息
  │     [system] ## Branch summary\n\n{summary}
  │
  └─ 5. 切换到新分支
```

### 4.2 切换分支（SwitchTo）

```
Session.SwitchTo("fork-1")
  │
  ├─ 1. 验证分支存在
  ├─ 2. 保存当前分支状态
  └─ 3. 切换 currentBranch
```

### 4.3 合并分支（MergeBranch）

```
Session.MergeBranch("fork-1")
  │
  ├─ 1. 验证分支存在
  ├─ 2. 找到分叉点（共同祖先）
  ├─ 3. 合并消息（去重、排序）
  ├─ 4. 更新主分支
  └─ 5. 删除源分支
```

---

## 5. 存储设计

### SQLite Schema 扩展

```sql
-- 分支表
CREATE TABLE branches (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    name TEXT NOT NULL,
    parent_id TEXT,
    summary TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    metadata TEXT,
    FOREIGN KEY (session_id) REFERENCES sessions(id)
);

-- 消息表增加 branch_id
ALTER TABLE session_entries ADD COLUMN branch_id TEXT DEFAULT 'main';

-- 索引
CREATE INDEX idx_branches_session ON branches(session_id);
CREATE INDEX idx_entries_branch ON session_entries(branch_id);
```

### 存储接口扩展

```go
type SessionStorage interface {
    // ... 现有方法
    
    // 分支操作
    SaveBranch(branch *Branch) error
    LoadBranch(branchID string) (*Branch, error)
    ListBranches(sessionID string) ([]*Branch, error)
    DeleteBranch(branchID string) error
    
    // 分支消息
    AppendToBranch(branchID string, entries []SessionTreeEntry) error
    LoadBranchEntries(branchID string) ([]SessionTreeEntry, error)
}
```

---

## 6. 摘要策略

### 6.1 截断摘要（默认）

```go
func truncateSummary(msgs []SessionTreeEntry) string {
    var sb strings.Builder
    sb.WriteString("## Branch summary\n\n")
    
    count := 0
    for _, m := range msgs {
        if m.Type != EntryUserMessage && m.Type != EntryAssistantMessage {
            continue
        }
        
        content := string(m.MessageData)
        if len(content) > 100 {
            content = content[:100] + "..."
        }
        
        fmt.Fprintf(&sb, "- [%s] %s\n", m.Type, content)
        count++
        if count >= 5 {
            sb.WriteString("- ... (more messages omitted)\n")
            break
        }
    }
    return sb.String()
}
```

### 6.2 LLM 摘要

```go
func llmSummary(ctx context.Context, completeFn LLMCompleteFn, title string, msgs []SessionTreeEntry) (string, error) {
    prompt := buildSummaryPrompt(title, msgs)
    return completeFn(ctx, prompt)
}

func buildSummaryPrompt(title string, msgs []SessionTreeEntry) string {
    return fmt.Sprintf(`You MUST create a structured summary of a conversation branch.
This branch was just left (the user forked to a new path).

Source session: %q
Messages: %d

Conversation:
%s

Use EXACT format:
## Goal
[What was the user trying to accomplish?]

## Constraints
- [Constraints mentioned]

## Progress
### Done
- [x] [Completed tasks]

### In Progress
- [ ] [Work started]

## Key Decisions
- **[Decision]**: [Rationale]

## Next Steps
1. [What should happen next]`,
        title, len(msgs), formatMessages(msgs))
}
```

---

## 7. 集成点

### 7.1 与 Agent Loop

```go
// Agent 循环中支持分支
config.OnEvent = func(e agent.AgentEvent) {
    switch ev := e.(type) {
    case agent.EventMessageEnd:
        // 自动保存到当前分支
        sess.AppendToCurrentBranch(session.NewAssistantMessageEntry(ev.Message))
    }
}

// 用户请求分支
if userWantsToFork {
    branch, _ := sess.Fork(ctx, "探索新方向")
    // 切换到新分支后继续对话
}
```

### 7.2 与 Context Manager

```go
// 切换分支时重建上下文
sess.SwitchTo(branchID)
ctxMgr.LoadFromSession(sess)
```

### 7.3 与 Memory

```go
// 每个分支可以有独立的记忆
branchMemory := memory.New(fmt.Sprintf("./memory-%s.json", branch.ID))
```

---

## 8. 使用示例

### 8.1 基本分支

```go
// 创建 session
sess, _ := session.NewSession(storage)

// 正常对话
sess.Append(session.NewUserMessageEntry("如何优化这个函数？"))
sess.Append(session.NewAssistantMessageEntry(response))

// 创建分支
branch, _ := sess.Fork(ctx, "探索方案B")
fmt.Printf("创建分支: %s\n", branch.Name)
fmt.Printf("摘要: %s\n", branch.Summary)

// 在新分支继续对话
sess.Append(session.NewUserMessageEntry("试试并行化"))
```

### 8.2 分支切换

```go
// 列出所有分支
branches := sess.Branches()
for _, b := range branches {
    fmt.Printf("分支: %s (消息数: %d)\n", b.Name, len(b.Messages))
}

// 切换到主分支
sess.SwitchTo("main")

// 切换到分支
sess.SwitchTo("fork-1")
```

### 8.3 分支合并

```go
// 合并分支回主分支
err := sess.MergeBranch(ctx, "fork-1")
if err != nil {
    fmt.Printf("合并失败: %v\n", err)
}
```

### 8.4 自定义摘要

```go
// 使用 LLM 生成摘要
summaryFunc := func(ctx context.Context, title string, msgs []session.SessionTreeEntry) (string, error) {
    prompt := buildPrompt(title, msgs)
    return llm.Complete(ctx, model, []core.Message{
        {Role: core.MessageRoleUser, Content: prompt},
    })
}

config := session.BranchConfig{
    MaxBranches: 10,
    AutoSummary: true,
    SummaryFunc: summaryFunc,
}

sess, _ := session.NewSessionWithBranchConfig(storage, config)
```

---

## 9. 测试计划

### 单元测试

| 测试 | 说明 |
|------|------|
| TestSession_Fork | 创建分支 |
| TestSession_Fork_WithSummary | 带摘要的分支 |
| TestSession_SwitchTo | 切换分支 |
| TestSession_SwitchTo_Invalid | 无效分支 ID |
| TestSession_Branches | 列出分支 |
| TestSession_DeleteBranch | 删除分支 |
| TestSession_DeleteBranch_Current | 不能删除当前分支 |
| TestSession_MergeBranch | 合并分支 |
| TestTruncateSummary | 截断摘要 |
| TestLLMSummary | LLM 摘要 |

### 集成测试

| 测试 | 说明 |
|------|------|
| TestSession_BranchWithAgent | Agent 循环中分支 |
| TestSession_BranchWithContext | 分支后上下文重建 |
| TestSession_BranchPersistence | 分支持久化 |

---

## 10. 实现计划

### Phase 1: 核心分支 (2-3 天)
- [ ] Branch 类型定义
- [ ] Session.Fork() 实现
- [ ] Session.SwitchTo() 实现
- [ ] Session.Branches() 实现
- [ ] 截断摘要
- [ ] 单元测试

### Phase 2: 持久化 (1-2 天)
- [ ] SQLite 分支表
- [ ] 扩展 SessionStorage 接口
- [ ] 分支消息持久化
- [ ] 集成测试

### Phase 3: LLM 摘要 (1 天)
- [ ] SummaryFunc 接口
- [ ] LLM 摘要实现
- [ ] 摘要模板

### Phase 4: 分支操作 (1-2 天)
- [ ] Session.DeleteBranch()
- [ ] Session.MergeBranch()
- [ ] 冲突处理

**总计: 5-8 天**

---

## 11. 参考项目

| 项目 | 参考点 |
|------|--------|
| [crux-harness/branch](file:///mnt/workspace/crux/crux/crux-harness/branch) | Summarizer、truncation fallback |
| [pi-mono branch-summarization.ts](https://github.com/) | 分支摘要格式 |
| [oh-my-pi branch-summary.md](https://github.com/) | 四段式摘要模板 |
