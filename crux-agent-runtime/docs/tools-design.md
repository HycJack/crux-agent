# Tools 内置工具设计

> 参考: [pi-ai-go/agent/tools](file:///mnt/workspace/pi-ai-go/agent/tools)
> 状态: 设计方案

---

## 1. 概述

内置工具是 Agent 可以直接调用的基础能力，无需额外配置。

### 工具清单

| 工具 | 说明 | 参考 |
|------|------|------|
| `read_file` | 读取文件内容 | pi-ai-go/tools/read.go |
| `write_file` | 写入文件 | pi-ai-go/tools/write.go |
| `edit_file` | 编辑文件（替换/补丁）| pi-ai-go/tools/edit.go |
| `bash` | 执行 shell 命令 | pi-ai-go/tools/bash.go |
| `glob` | 列出匹配文件 | pi-ai-go/tools/glob.go |
| `grep` | 搜索文件内容 | pi-ai-go/tools/grep.go |

---

## 2. 设计原则

1. **JSON Schema 声明** — 每个工具用 JSON Schema 描述参数
2. **统一返回格式** — `[]core.ContentBlock` 结果
3. **错误处理** — 错误返回 `IsError: true`，不 panic
4. **安全路径** — 文件操作通过 `safePath` 验证
5. **输出截断** — 防止上下文溢出

---

## 3. 接口设计

### 3.1 AgentTool 类型

```go
type AgentTool struct {
    Name        string
    Label       string
    Description string
    Parameters  json.RawMessage  // JSON Schema
    Execute     ExecuteFunc
}

type ExecuteFunc func(ctx context.Context, toolCallID string, params json.RawMessage, onUpdate func(json.RawMessage)) (AgentToolResult, error)

type AgentToolResult struct {
    Content   []core.ContentBlock
    Details   json.RawMessage
    IsError   bool
    Terminate bool
}
```

### 3.2 工具注册

```go
// 获取所有内置工具
tools := tools.All()

// 获取单个工具
readTool := tools.Read()
bashTool := tools.Bash()

// 注册到 Agent
config := agent.AgentLoopConfig{
    Tools: tools.All(),
    // ...
}
```

---

## 4. 工具实现

### 4.1 read_file

**参数**:
```json
{
    "filePath": "string (required)",
    "offset": "integer (optional)",
    "limit": "integer (optional)"
}
```

**返回**:
```
文件内容（可选截断）
```

**特性**:
- 支持行范围读取（offset + limit）
- 超过 200KB 自动截断
- 安全路径验证

### 4.2 write_file

**参数**:
```json
{
    "filePath": "string (required)",
    "content": "string (required)",
    "append": "boolean (optional)"
}
```

**返回**:
```
写入成功：文件路径 + 字节数
```

**特性**:
- 自动创建目录
- 支持追加模式
- 安全路径验证

### 4.3 edit_file

**参数**:
```json
{
    "filePath": "string (required)",
    "oldString": "string (required)",
    "newString": "string (required)",
    "replaceAll": "boolean (optional)"
}
```

**返回**:
```
替换成功：替换次数
```

**特性**:
- 字符串替换（非正则）
- 支持全部替换
- 原子操作（先备份）

### 4.4 bash

**参数**:
```json
{
    "command": "string (required)",
    "timeout": "integer (optional, ms)",
    "shell": "string (optional)"
}
```

**返回**:
```
stdout + stderr + exitCode
```

**特性**:
- 跨平台 shell 选择
- 超时控制（默认 30s）
- 输出截断（100KB）
- 支持自定义 shell

### 4.5 glob

**参数**:
```json
{
    "pattern": "string (required)",
    "path": "string (optional)"
}
```

**返回**:
```
匹配的文件列表
```

**特性**:
- 支持 glob 模式
- 递归匹配
- 结果限制（最多 1000 个）

### 4.6 grep

**参数**:
```json
{
    "pattern": "string (required)",
    "path": "string (optional)",
    "include": "string (optional)",
    "regex": "boolean (optional)"
}
```

**返回**:
```
匹配的行（文件名:行号:内容）
```

**特性**:
- 支持正则表达式
- 文件过滤（include）
- 结果限制

---

## 5. 安全设计

### 5.1 路径安全

```go
func resolveSafePath(path, workingDir string) (string, error) {
    // 1. 解析绝对路径
    // 2. 检查是否在允许的目录内
    // 3. 防止路径遍历攻击 (../../)
    // 4. 返回安全路径
}
```

### 5.2 命令安全

- 超时控制（默认 30s）
- 输出截断（100KB）
- 不支持后台进程
- 可选：命令白名单/黑名单

### 5.3 文件安全

- 路径验证
- 大小限制
- 备份机制（edit_file）

---

## 6. 使用示例

### 6.1 基本使用

```go
import "crux-agent-runtime/tools"

// 获取所有内置工具
allTools := tools.All()

// 注册到 Agent
config := agent.AgentLoopConfig{
    Model:    model,
    Tools:    allTools,
    StreamFn: streamFn,
}

// 运行 Agent
stream := agent.AgentLoop(ctx, messages, config)
```

### 6.2 自定义工具

```go
// 定义自定义工具
customTool := agent.AgentTool{
    Name:        "search_web",
    Description: "搜索网页",
    Parameters: mustSchema(`{
        "type": "object",
        "properties": {
            "query": {"type": "string"}
        },
        "required": ["query"]
    }`),
    Execute: func(ctx context.Context, id string, params json.RawMessage, onUpdate func(json.RawMessage)) (agent.AgentToolResult, error) {
        var args struct{ Query string }
        json.Unmarshal(params, &args)
        
        result := searchWeb(args.Query)
        return agent.AgentToolResult{
            Content: []core.ContentBlock{
                core.TextContent{Type: "text", Text: result},
            },
        }, nil
    },
}

// 合并内置和自定义工具
allTools := append(tools.All(), customTool)
```

### 6.3 带沙箱的工具

```go
// 使用沙箱限制工具能力
sandbox := sandbox.NewProcess(sandbox.ProcessConfig{
    ReadOnly:  []string{"/home/user/projects"},
    ReadWrite: []string{"/home/user/projects/output"},
    AllowCmds: []string{"ls", "cat", "grep", "python3"},
})

// 工具通过沙箱访问系统
config := agent.AgentLoopConfig{
    Tools:   tools.All(),
    Sandbox: sandbox,
}
```

---

## 7. 测试策略

### 单元测试

| 工具 | 测试内容 |
|------|----------|
| read_file | 正常读取、偏移/限制、大文件截断、路径安全 |
| write_file | 正常写入、追加模式、目录创建、路径安全 |
| edit_file | 字符串替换、全部替换、备份 |
| bash | 命令执行、超时、输出截断、shell 选择 |
| glob | 模式匹配、递归、结果限制 |
| grep | 文本搜索、正则、文件过滤 |

### 集成测试

| 测试 | 说明 |
|------|------|
| TestTools_All | 所有工具可获取 |
| TestTools_Execute | 工具执行流程 |
| TestTools_ErrorHandling | 错误处理 |
| TestTools_Sandbox | 沙箱集成 |

---

## 8. 实现计划

### Phase 1: 核心工具 (2-3 天)
- [ ] tools/tools.go — 公共函数和类型
- [ ] tools/read.go — 文件读取
- [ ] tools/write.go — 文件写入
- [ ] tools/bash.go — Shell 命令
- [ ] 单元测试

### Phase 2: 高级工具 (1-2 天)
- [ ] tools/edit.go — 文件编辑
- [ ] tools/glob.go — 文件搜索
- [ ] tools/grep.go — 内容搜索
- [ ] 安全路径验证

### Phase 3: 集成 (1 天)
- [ ] 与 Agent Loop 集成
- [ ] 与 Sandbox 集成
- [ ] 集成测试

**总计: 4-6 天**
