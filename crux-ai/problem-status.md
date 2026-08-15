# crux-ai 问题解决状态

## 主要问题解决状态

### 1. 死代码（Dead Code）
**状态**：❌ 未解决

- `core/compaction.go` 中的压缩相关类型仍然存在
- `providers/openai/completions.go` 中的 `NewCompletions()` 和 `CompletionsProvider` 仍然存在
- 其他死代码（`core/timeouts.go`、`core/headers.go` 等）仍然存在

**证据**：
- `NewCompletions()` 仍然在 `providers/openai/completions.go` 中定义
- 但确实没有被使用（register.go 中使用的是 `openai.NewCompat()`）

### 2. 硬编码超时
**状态**：❌ 未解决

**位置**：`providers/compat/compat.go:203`
```go
d := time.Duration(opts.TimeoutMs) * time.Millisecond
if d <= 0 {
    d = 5 * time.Minute // matches SSEClient default
}
```

**建议**：使用 `opts.TimeoutMs` 或从配置中读取。

### 3. 代码重复
**状态**：❌ 未解决

**问题**：多个 provider 文件中重复使用 `core.NewTimeoutClient(opts.TimeoutMs)`：
- `providers/anthropic/anthropic.go:278`
- `providers/bedrock/bedrock.go:331`
- `providers/google/google.go:166`
- `providers/google/vertex.go:119`
- `providers/mistral/mistral.go:278`
- `providers/openai/completions.go:148`
- `providers/openai/responses.go:206`

**建议**：抽取 `core.NewProviderRequest()` 统一处理。

### 4. 未使用的 Compat 字段
**状态**：❌ 未解决

**问题**：`core.Compat` 结构体中的字段已定义但未被任何 provider 读取：
- `SupportsStore`
- `SupportsDeveloperRole`
- `SupportsReasoningEffort`
- `MaxTokensField`
- `RequiresToolResultName`
- `RequiresThinkingAsText`
- `ThinkingFormat`
- `CacheControlFormat`

**建议**：要么删除这些字段，要么在 provider 中实现相应逻辑。

### 5. 配置验证
**状态**：❌ 未解决

**问题**：`compat.Router.Register()` 没有配置验证：
- 不检查必需字段（Provider、DefaultBaseURL）
- 不验证 URL 格式
- 不检查重复注册

**建议**：添加配置验证逻辑。

## 已解决的问题

### 1. 项目结构
**状态**：✅ 已解决

项目结构清晰，模块分离良好：
- `core/`：零依赖
- `ai/`：依赖 `core/`
- `providers/`：依赖 `core/` 和 `internal/`
- `internal/`：不导出

### 2. 错误处理
**状态**：✅ 已解决

错误处理体系完善：
- 双层错误模型（哨兵+类型化）
- 支持 `errors.Is` 和 `errors.As`
- 详细的错误信息

### 3. 文档
**状态**：✅ 已解决

文档完善：
- `README.md`：详细的使用说明
- `AGENTS.md`：完整的项目结构说明
- 代码注释：中英文结合

## 总结

**主要问题都没有被解决**。项目仍然存在：
1. 死代码
2. 硬编码超时
3. 代码重复
4. 未使用的字段
5. 缺少配置验证

**建议**：在正式发布前解决这些问题，特别是硬编码超时和代码重复问题。

## 优先级建议

### 高优先级
1. 修复硬编码的 5 分钟超时
2. 添加配置验证
3. 清理死代码

### 中优先级
1. 抽取 provider 样板代码
2. 实现或删除 Compat 字段
3. 增强错误分类

### 低优先级
1. 添加监控接口
2. 优化 SSE 处理
3. 更新文档