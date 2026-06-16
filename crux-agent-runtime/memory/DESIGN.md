# 模块设计：Memory 长期记忆

> 模块: crux-agent-runtime/memory
> 版本: v0.2.0 | 更新: 2026-06-17
> 状态: ✅ 已完成（支持第三方 provider）

---

## 1. 职责

跨会话的持久化记忆系统，支持多种后端存储。

**核心能力**：
- MemoryProvider 统一接口
- KV 存储（本地 JSON 文件）
- 向量存储（语义搜索）
- 级联查询、主备切换
- 自动重试、Mock 测试

## 2. 架构

```
MemoryProvider (接口)
  │
  ├── KVStore                 # 本地 JSON 存储
  ├── VectorStore             # 向量数据库（Qdrant, Chroma）
  ├── Mem0Provider            # Mem0 云服务（待实现）
  ├── ZepProvider             # Zep 服务（待实现）
  │
  ├── RetryableProvider       # 重试包装器
  ├── CascadeProvider         # 级联查询
  ├── PrimaryFallbackProvider # 主备切换
  └── MockProvider            # 测试用 Mock
```

## 3. 核心接口

```go
type MemoryProvider interface {
    Store(ctx context.Context, entry Entry) error
    Search(ctx context.Context, query string, limit int) ([]Entry, error)
    Get(ctx context.Context, key string) (Entry, bool, error)
    Delete(ctx context.Context, key string) error
    List(ctx context.Context, filter Filter) ([]Entry, error)
    FormatForPrompt(ctx context.Context, query string, limit int) (string, error)
    Close() error
}
```

## 4. Entry 结构

```go
type Entry struct {
    ID        string            `json:"id,omitempty"`
    Key       string            `json:"key"`
    Value     string            `json:"value"`
    Category  string            `json:"category,omitempty"`
    Metadata  map[string]string `json:"metadata,omitempty"`
    Embedding []float32         `json:"embedding,omitempty"`
    CreatedAt time.Time         `json:"created_at"`
    UpdatedAt time.Time         `json:"updated_at"`
}
```

## 5. Provider 对比

| Provider | 存储 | 搜索 | 性能 | 使用场景 |
|----------|------|------|------|----------|
| **KVStore** | JSON 文件 | 关键词匹配 | 快 | 开发、小规模 |
| **VectorStore** | 向量数据库 | 语义搜索 | 中 | 生产环境 |
| **Mem0Provider** | 云服务 | LLM 提取 | 慢 | 第三方服务 |
| **RetryableProvider** | 包装器 | 委托 | - | 添加重试 |
| **CascadeProvider** | 多个 | 依次尝试 | 慢 | 高可用 |
| **PrimaryFallbackProvider** | 两个 | 主备切换 | 快 | 容灾 |
| **MockProvider** | 内存 | 可配置 | 最快 | 测试 |

## 6. 向量存储架构

```
VectorStore
  │
  ├── VectorClient (接口)
  │     ├── InMemoryVectorClient  # 测试用
  │     ├── QdrantClient         # Qdrant
  │     └── ChromaClient         # Chroma
  │
  └── Embedder (接口)
        ├── SimpleEmbedder       # 测试用（hash）
        └── OpenAIEmbedder       # OpenAI embeddings
```

### VectorClient 接口

```go
type VectorClient interface {
    Upsert(ctx context.Context, collection string, points []Point) error
    Search(ctx context.Context, collection string, vector []float32, limit int) ([]SearchResult, error)
    Delete(ctx context.Context, collection string, id string) error
    Close() error
}
```

### Embedder 接口

```go
type Embedder interface {
    Embed(ctx context.Context, text string) ([]float32, error)
    EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}
```

## 7. 错误处理

```go
type MemoryError struct {
    Code     ErrorCode  // CONNECTION_ERROR, TIMEOUT, RATE_LIMIT, AUTH_ERROR, NOT_FOUND, INTERNAL_ERROR
    Message  string
    Err      error
    Provider string
}

func (e *MemoryError) IsRetryable() bool {
    // CONNECTION_ERROR, TIMEOUT, RATE_LIMIT → true
    // AUTH_ERROR, NOT_FOUND, INTERNAL_ERROR → false
}
```

## 8. 重试策略

```go
RetryableProvider {
    maxRetries: 3
    baseDelay:  1s
    maxDelay:   30s
    // 指数退避: 1s → 2s → 4s
}
```

## 9. 组合策略

### CascadeProvider（级联）

```
Search(query)
  │
  ├─ provider1.Search() → 成功？返回
  ├─ provider2.Search() → 成功？返回
  └─ provider3.Search() → 返回结果或错误
```

### PrimaryFallbackProvider（主备）

```
Search(query)
  │
  ├─ primary.Search() → 成功？返回
  └─ fallback.Search() → 返回结果
```

## 10. 测试策略

| 测试类型 | 使用的 Provider |
|----------|-----------------|
| 单元测试 | MockProvider |
| 集成测试 | InMemoryVectorClient + SimpleEmbedder |
| 端到端 | 真实向量数据库 |

### Mock 使用示例

```go
mock := memory.NewMockProvider()
mock.WithSearchError(fmt.Errorf("test error"))

// 或自定义行为
mock.SearchFunc = func(ctx context.Context, query string, limit int) ([]Entry, error) {
    return []Entry{{Key: "test", Value: "value"}}, nil
}
```

## 11. 配置管理

```yaml
memory:
  default: vector
  providers:
    kv:
      type: kv
      path: ./data/memory.json
    vector:
      type: vector
      client:
        type: qdrant
        host: localhost
        port: 6333
      embedder:
        type: openai
        model: text-embedding-3-small
  strategy:
    type: cascade
    providers: [kv, vector]
```

## 12. 迁移路径

```
v0.1.0 (KVStore)
  │
  ├─→ v0.2.0 (MemoryProvider 接口)
  │     └─ 兼容旧 API
  │
  └─→ v0.3.0 (VectorStore)
        └─ 语义搜索
```

## 13. 集成点

### 与 AutoLearn

```go
learner := autolearn.New(memoryProvider, settings)
learner.ProcessUserInput("我叫小明")
// → memoryProvider.Store(ctx, Entry{Key: "user.name", Value: "小明"})
```

### 与 Agent Loop

```go
config.SystemPrompt = basePrompt + "\n\n" + mem.FormatForPrompt(ctx, "", 0)
```

### 与 Session

```go
// Memory 独立于 Session
// Session 管理对话历史，Memory 管理长期事实
```

## 14. 后续计划

- [ ] Mem0Provider 完整实现
- [ ] ZepProvider 实现
- [ ] RAGProvider 实现
- [ ] 连接池
- [ ] 缓存层
- [ ] Prometheus 指标
