# crux-ai 代码审查报告

## 项目概述
crux-ai 是一个 Go 语言的多 LLM 提供商统一抽象层，提供统一的接口来访问多个 AI 服务提供商。项目结构清晰，设计合理，处于实验阶段（v0.0.1）。

## 优点

### 1. 项目结构清晰
- **模块分离良好**：core/、ai/、providers/、internal/ 各司其职
- **依赖方向明确**：core/ 零依赖，ai/ 依赖 core/，providers/ 依赖 core/ 和 internal/
- **Facade 模式**：cruxai.go 提供统一的入口点

### 2. 错误处理优秀
- **双层错误模型**：哨兵错误（ErrOverflow、ErrAuth 等）+ 类型化错误（*OverflowError、*AuthError 等）
- **支持 errors.Is 和 errors.As**：便于错误分类和处理
- **详细的错误信息**：包含提供商、HTTP 状态码、重试提示等上下文

### 3. 设计模式合理
- **EventStream[T,R]**：使用泛型实现的事件流，支持流式响应
- **自动重试机制**：指数退避、支持 RateLimitError 中的 RetryAfter 提示
- **并发安全**：所有 provider 实现都支持并发调用

### 4. 文档完善
- **README.md**：详细的使用说明和架构图
- **AGENTS.md**：完整的项目结构、模块边界、命名约定
- **代码注释**：中英文结合，解释"为什么"而非"是什么"

### 5. 测试覆盖良好
- 表驱动测试（table-driven tests）
- 子测试（t.Run）
- 边界条件测试
- 错误场景测试

## 潜在问题

### 1. 死代码（Dead Code）
AGENTS.md 中已列出，主要包括：
- `core/compaction.go` 中的压缩相关类型
- `core/timeouts.go` 中的流超时中间件
- `core/headers.go` 中的 ProviderHeaders 相关函数
- `providers/openai/` 中的 `NewCompletions()` 和 `CompletionsProvider`

**建议**：定期清理死代码，或将其移至 internal/ 包。

### 2. 代码重复
每个原生 provider 都重复以下样板代码：
```go
client := core.NewTimeoutClient(opts.TimeoutMs)
apiKey := core.ResolveAPIKey(model.Provider, opts.APIKey)
// ... 设置 headers、创建请求
```

**建议**：抽取 `core.NewProviderRequest()` 统一负责 timeout/auth/header 合并。

### 3. 硬编码超时
`providers/compat/compat.go:203` 硬编码 5 分钟超时：
```go
core.WrapHTTPTimeout(model.Provider, 5*time.Minute, err)
```

**建议**：使用 `opts.TimeoutMs`（已有）。

### 4. 未使用的 Compat 字段
`core.Compat` 结构体中的字段（如 `SupportsStore`、`SupportsReasoningEffort`）已定义但未被任何 provider 读取。

**建议**：要么删除这些字段，要么在 provider 中实现相应逻辑。

### 5. SSE 处理重复
Anthropic、Bedrock、Mistral 各自维护独立的 SSE 处理循环。

**建议**：将通用部分抽到 `internal/sse`，并添加 per-provider 钩子。

## 架构改进建议

### 1. 统一请求构建
创建 `core.NewProviderRequest()` 函数，统一处理：
- HTTP 客户端创建
- API Key 解析
- Header 合并
- 错误包装

### 2. 增强错误分类
考虑添加更细粒度的错误类型：
- `*QuotaExhaustedError`（配额耗尽，不可重试）
- `*ModelNotFoundError`（模型不存在）
- `*InvalidRequestError`（请求格式错误）

### 3. 配置验证
在 `compat.Router.Register()` 中添加配置验证：
- 检查必需字段（Provider、DefaultBaseURL）
- 验证 URL 格式
- 检查重复注册

### 4. 监控和指标
添加可选的监控接口：
- 请求计数
- 延迟统计
- 错误率
- Token 使用量

## 代码质量评估

### 优点
- **类型安全**：使用 Go 泛型和接口确保类型安全
- **并发安全**：正确的 mutex 使用和 channel 设计
- **错误处理**：全面的错误分类和处理
- **可测试性**：良好的依赖注入和 mock 支持

### 改进点
- **代码重复**：provider 间的样板代码可减少
- **死代码**：定期清理未使用的代码
- **配置验证**：添加运行时配置检查
- **文档更新**：确保 README 与代码同步

## 总结

crux-ai 是一个设计良好、结构清晰的项目。它成功实现了多 LLM 提供商的统一抽象，具有以下特点：

1. **架构合理**：模块分离清晰，依赖方向明确
2. **错误处理优秀**：双层错误模型，支持丰富的上下文
3. **并发安全**：正确的并发设计
4. **文档完善**：详细的文档和注释

主要改进方向：
1. 清理死代码
2. 减少代码重复
3. 增强配置验证
4. 考虑添加监控接口

项目处于实验阶段，建议在 v0.1.0 发布前解决上述问题，特别是硬编码超时和未使用字段的问题。

## 优先级建议

### 高优先级
1. 修复硬编码的 5 分钟超时
2. 清理 `providers/openai/` 中的死代码
3. 添加配置验证

### 中优先级
1. 抽取 provider 样板代码
2. 增强错误分类
3. 更新文档

### 低优先级
1. 实现 Compat 字段逻辑
2. 添加监控接口
3. 优化 SSE 处理

---

**审查时间**：2026年6月27日  
**审查版本**：v0.0.1  
**审查结论**：项目质量良好，建议在正式发布前解决上述问题。