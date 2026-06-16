# 模块设计：Skills 技能系统

> 模块: crux-agent-runtime/skills
> 版本: v0.1.0 | 更新: 2026-06-17
> 状态: ⏳ 待实现

---

## 1. 职责

管理 Agent 可用的工具/技能，支持动态注册、沙箱隔离、热加载。

## 2. 核心接口

```go
type Skill interface {
    Name() string
    Description() string
    ToolSchemas() []core.ToolSchema
    Handle(ctx SkillContext, call core.ToolCall) SkillResult
    Init(config map[string]any) error
    Close() error
}

type SkillContext struct {
    Context  context.Context
    Sandbox  Sandbox
    Memory   MemoryProvider
    Session  SessionProvider
    Logger   *slog.Logger
}
```

## 3. 技能分类

| 类别 | 技能 | 说明 |
|------|------|------|
| **文件** | readfile, writefile, listfiles | 文件操作 |
| **终端** | terminal | Shell 命令执行 |
| **记忆** | memory | 长期记忆（LLM 可调用）|
| **网络** | websearch | 网页搜索 |
| **协作** | delegate | 跨 Agent 委托 |
| **维护** | compaction | 上下文压缩 |

## 4. 注册流程

```
registry.Register(skill)
  │
  ├─ 验证 skill.Name() 唯一性
  ├─ 收集 skill.ToolSchemas()
  └─ 存储到 byName map
```

## 5. 执行流程

```
registry.Execute(ctx, call)
  │
  ├─ 查找 byName[call.Function.Name]
  ├─ 构建 SkillContext
  └─ skill.Handle(ctx, call)
```

## 6. YAML 声明式技能

```yaml
name: my_skill
description: 自定义技能
tools:
  - name: my_tool
    description: 执行操作
    parameters: { ... }
executor:
  type: command
  command: "python3 skill.py"
```
