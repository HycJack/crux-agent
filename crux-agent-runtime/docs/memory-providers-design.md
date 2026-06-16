# 第三方记忆系统接入设计

> 状态: 设计方案
> 参考: [Mem0](https://github.com/mem0ai/mem0), [Zep](https://github.com/getzep/zep), [LangChain Memory](https://python.langchain.com/docs/modules/memory/)

---

## 1. 问题

当前 `memory` 包是简单的 JSON KV 存储，无法支持：
- **语义搜索** — "用户喜欢什么编程语言？" 需要向量相似度匹配
- **知识图谱** — 实体关系查询（用户 → 公司 → 项目）
- **外部记忆服务** — Mem0、Zep 等专业记忆系统
- **RAG 检索** — 从文档库中检索相关知识

## 2. 解决方案

抽象一个 `MemoryProvider` 接口，支持多种后端：

```
MemoryProvider (接口)
  ├── KVStore          # 当前的 JSON KV 存储
  ├── VectorStore      # 向量数据库（Qdrant, Milvus, Chroma）
  ├── KnowledgeGraph   # 知识图谱（Neo4j, Dgraph）
  ├── Mem0Provider     # Mem0 云服务
  ├── ZepProvider      # Zep 记忆服务
  └── RAGProvider      # RAG 检索系统
```

---

## 3. 接口设计

### 3.1 核心接口

```go
// MemoryProvider 是所有记忆后端的统一接口
// || 记忆提供者接口
type MemoryProvider interface {
    // Store 存储一条记忆
    Store(ctx context.Context, entry MemoryEntry) error

    // Search 搜索相关记忆
    // query: 自然语言查询
    // limit: 返回数量
    Search(ctx context.Context, query string, limit int) ([]MemoryEntry, error)

    // Get 按 key 精确获取
    Get(ctx context.Context, key string) (MemoryEntry, bool, error)

    // Delete 删除一条记忆
    Delete(ctx context.Context, key string) error

    // List 列出所有记忆（可选，某些后端可能不支持全量列出）
    List(ctx context.Context, filter MemoryFilter) ([]MemoryEntry, error)

    // FormatForPrompt 格式化为 system prompt 注入
    FormatForPrompt(ctx context.Context, query string, limit int) (string, error)

    // Close 关闭连接
    Close() error
}

// MemoryEntry 是一条记忆
type MemoryEntry struct {
    ID        string            `json:"id"`
    Key       string            `json:"key"`
    Value     string            `json:"value"`
    Category  string            `json:"category,omitempty"`
    Metadata  map[string]string `json:"metadata,omitempty"`
    Embedding []float32         `json:"embedding,omitempty"` // 向量（可选）
    CreatedAt time.Time         `json:"created_at"`
    UpdatedAt time.Time         `json:"updated_at"`
}

// MemoryFilter 用于过滤记忆
type MemoryFilter struct {
    Category string
    Keys     []string
    Since    *time.Time
    Limit    int
}
```

### 3.2 扩展接口

```go
// VectorStore 向量存储扩展
type VectorStore interface {
    MemoryProvider

    // StoreWithEmbedding 存储带向量的记忆
    StoreWithEmbedding(ctx context.Context, entry MemoryEntry, embedding []float32) error

    // SearchByVector 按向量相似度搜索
    SearchByVector(ctx context.Context, embedding []float32, limit int) ([]MemoryEntry, error)
}

// KnowledgeGraph 知识图谱扩展
type KnowledgeGraph interface {
    MemoryProvider

    // AddRelation 添加实体关系
    AddRelation(ctx context.Context, subject, predicate, object string) error

    // QueryRelation 查询关系
    QueryRelation(ctx context.Context, subject, predicate string) ([]string, error)

    // GetNeighbors 获取实体的邻居
    GetNeighbors(ctx context.Context, entity string, depth int) ([]Entity, error)
}

type Entity struct {
    Name       string
    Type       string
    Properties map[string]string
}
```

---

## 4. 实现方案

### 4.1 KVStore（当前实现，适配新接口）

```go
// KVStore 是当前的 JSON KV 存储，适配 MemoryProvider 接口
type KVStore struct {
    mu   sync.RWMutex
    path string
    data map[string]MemoryEntry
}

func NewKVStore(path string) (*KVStore, error) { ... }

func (s *KVStore) Store(ctx context.Context, entry MemoryEntry) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    if entry.ID == "" {
        entry.ID = generateID()
    }
    if entry.CreatedAt.IsZero() {
        entry.CreatedAt = time.Now()
    }
    entry.UpdatedAt = time.Now()
    s.data[entry.Key] = entry
    return s.save()
}

func (s *KVStore) Search(ctx context.Context, query string, limit int) ([]MemoryEntry, error) {
    // KV 存储不支持语义搜索，退化为关键词匹配
    s.mu.RLock()
    defer s.mu.RUnlock()
    
    var results []MemoryEntry
    queryLower := strings.ToLower(query)
    for _, entry := range s.data {
        if strings.Contains(strings.ToLower(entry.Key), queryLower) ||
           strings.Contains(strings.ToLower(entry.Value), queryLower) {
            results = append(results, entry)
        }
    }
    
    // 按更新时间排序
    sort.Slice(results, func(i, j int) bool {
        return results[i].UpdatedAt.After(results[j].UpdatedAt)
    })
    
    if limit > 0 && len(results) > limit {
        results = results[:limit]
    }
    return results, nil
}

func (s *KVStore) FormatForPrompt(ctx context.Context, query string, limit int) (string, error) {
    entries, err := s.Search(ctx, query, limit)
    if err != nil {
        return "", err
    }
    if len(entries) == 0 {
        return "", nil
    }
    
    var sb strings.Builder
    sb.WriteString("# Long-term Memory\n\n")
    for _, e := range entries {
        sb.WriteString("- ")
        sb.WriteString(e.Key)
       	sb.WriteString(": ")
       	sb.WriteString(e.Value)
       	sb.WriteString("\n")
    }
    return sb.String(), nil
}
```

### 4.2 VectorStore（向量数据库）

```go
// VectorStore 使用向量数据库（如 Qdrant, Milvus, Chroma）
type VectorStore struct {
    client    VectorClient // 向量数据库客户端
    embedder  Embedder     // 文本向量化
    namespace string       // 集合/命名空间
}

// VectorClient 向量数据库客户端接口
type VectorClient interface {
    Upsert(ctx context.Context, collection string, points []Point) error
    Search(ctx context.Context, collection string, vector []float32, limit int) ([]SearchResult, error)
    Delete(ctx context.Context, collection string, id string) error
}

// Embedder 文本向量化接口
type Embedder interface {
    Embed(ctx context.Context, text string) ([]float32, error)
    EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}

func NewVectorStore(client VectorClient, embedder Embedder, namespace string) *VectorStore {
    return &VectorStore{
        client:    client,
        embedder:  embedder,
        namespace: namespace,
    }
}

func (s *VectorStore) Store(ctx context.Context, entry MemoryEntry) error {
    // 1. 生成向量
    embedding, err := s.embedder.Embed(ctx, entry.Key + ": " + entry.Value)
    if err != nil {
        return fmt.Errorf("embed: %w", err)
    }
    entry.Embedding = embedding
    
    // 2. 存储到向量数据库
    point := Point{
        ID:        entry.ID,
        Vector:    embedding,
        Payload:   entryToPayload(entry),
    }
    return s.client.Upsert(ctx, s.namespace, []Point{point})
}

func (s *VectorStore) Search(ctx context.Context, query string, limit int) ([]MemoryEntry, error) {
    // 1. 查询向量化
    queryVector, err := s.embedder.Embed(ctx, query)
    if err != nil {
        return nil, fmt.Errorf("embed query: %w", err)
    }
    
    // 2. 向量搜索
    results, err := s.client.Search(ctx, s.namespace, queryVector, limit)
    if err != nil {
        return nil, fmt.Errorf("vector search: %w", err)
    }
    
    // 3. 转换为 MemoryEntry
    entries := make([]MemoryEntry, len(results))
    for i, r := range results {
        entries[i] = payloadToEntry(r.Payload)
        entries[i].Embedding = nil // 不返回向量
    }
    return entries, nil
}

func (s *VectorStore) FormatForPrompt(ctx context.Context, query string, limit int) (string, error) {
    entries, err := s.Search(ctx, query, limit)
    if err != nil {
        return "", err
    }
    return formatEntries(entries), nil
}
```

### 4.3 Mem0Provider（Mem0 云服务）

```go
// Mem0Provider 接入 Mem0 记忆服务
// https://docs.mem0.ai/
type Mem0Provider struct {
    client  *http.Client
    apiKey  string
    baseURL string
    userID  string
}

func NewMem0Provider(apiKey, userID string) *Mem0Provider {
    return &Mem0Provider{
        client:  &http.Client{Timeout: 30 * time.Second},
        apiKey:  apiKey,
        baseURL: "https://api.mem0.ai/v1",
        userID:  userID,
    }
}

func (p *Mem0Provider) Store(ctx context.Context, entry MemoryEntry) error {
    body := map[string]any{
        "messages": []map[string]string{
            {"role": "user", "content": entry.Key + ": " + entry.Value},
        },
        "user_id": p.userID,
    }
    
    data, _ := json.Marshal(body)
    req, _ := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/memories/", bytes.NewReader(data))
    req.Header.Set("Authorization", "Token "+p.apiKey)
    req.Header.Set("Content-Type", "application/json")
    
    resp, err := p.client.Do(req)
    if err != nil {
        return fmt.Errorf("mem0 store: %w", err)
    }
    defer resp.Body.Close()
    
    if resp.StatusCode != http.StatusOK {
        body, _ := io.ReadAll(resp.Body)
        return fmt.Errorf("mem0 store: %d %s", resp.StatusCode, body)
    }
    return nil
}

func (p *Mem0Provider) Search(ctx context.Context, query string, limit int) ([]MemoryEntry, error) {
    body := map[string]any{
        "query":   query,
        "user_id": p.userID,
        "limit":   limit,
    }
    
    data, _ := json.Marshal(body)
    req, _ := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/search/", bytes.NewReader(data))
    req.Header.Set("Authorization", "Token "+p.apiKey)
    req.Header.Set("Content-Type", "application/json")
    
    resp, err := p.client.Do(req)
    if err != nil {
        return nil, fmt.Errorf("mem0 search: %w", err)
    }
    defer resp.Body.Close()
    
    var result struct {
        Memories []struct {
            ID      string `json:"id"`
            Memory  string `json:"memory"`
            Score   float64 `json:"score"`
        } `json:"memories"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return nil, fmt.Errorf("mem0 decode: %w", err)
    }
    
    entries := make([]MemoryEntry, len(result.Memories))
    for i, m := range result.Memories {
        entries[i] = MemoryEntry{
            ID:    m.ID,
            Key:   "memory",
            Value: m.Memory,
        }
    }
    return entries, nil
}

func (p *Mem0Provider) FormatForPrompt(ctx context.Context, query string, limit int) (string, error) {
    entries, err := p.Search(ctx, query, limit)
    if err != nil {
        return "", err
    }
    return formatEntries(entries), nil
}

func (p *Mem0Provider) Close() error { return nil }
```

### 4.4 RAGProvider（RAG 检索）

```go
// RAGProvider 从文档库检索相关知识
type RAGProvider struct {
    retriever Retriever
    embedder  Embedder
}

// Retriever 文档检索接口
type Retriever interface {
    Retrieve(ctx context.Context, query string, limit int) ([]Document, error)
}

type Document struct {
    ID      string
    Content string
    Score   float64
    Source  string
}

func NewRAGProvider(retriever Retriever, embedder Embedder) *RAGProvider {
    return &RAGProvider{
        retriever: retriever,
        embedder:  embedder,
    }
}

func (p *RAGProvider) Search(ctx context.Context, query string, limit int) ([]MemoryEntry, error) {
    docs, err := p.retriever.Retrieve(ctx, query, limit)
    if err != nil {
        return nil, err
    }
    
    entries := make([]MemoryEntry, len(docs))
    for i, doc := range docs {
        entries[i] = MemoryEntry{
            ID:       doc.ID,
            Key:      "document",
            Value:    doc.Content,
            Metadata: map[string]string{"source": doc.Source},
        }
    }
    return entries, nil
}

func (p *RAGProvider) FormatForPrompt(ctx context.Context, query string, limit int) (string, error) {
    docs, err := p.retriever.Retrieve(ctx, query, limit)
    if err != nil {
        return "", err
    }
    
    var sb strings.Builder
    sb.WriteString("# Relevant Knowledge\n\n")
    for _, doc := range docs {
        sb.WriteString("## Source: ")
        sb.WriteString(doc.Source)
        sb.WriteString("\n\n")
        sb.WriteString(doc.Content)
        sb.WriteString("\n\n---\n\n")
    }
    return sb.String(), nil
}
```

---

## 5. 使用示例

### 5.1 KV 存储（默认）

```go
mem, _ := memory.NewKVStore("./memory.json")
mem.Store(ctx, memory.MemoryEntry{
    Key:   "user.name",
    Value: "小明",
})
```

### 5.2 向量数据库

```go
// 使用 Qdrant
client := qdrant.NewClient("localhost:6333")
embedder := openai.NewEmbedder("text-embedding-3-small")

mem := memory.NewVectorStore(client, embedder, "agent-memory")

// 存储
mem.Store(ctx, memory.MemoryEntry{
    Key:   "user.preference",
    Value: "喜欢用 Python 编程",
})

// 语义搜索
results, _ := mem.Search(ctx, "用户喜欢什么编程语言？", 5)
// 结果: [{Key: "user.preference", Value: "喜欢用 Python 编程"}]
```

### 5.3 Mem0 云服务

```go
mem := memory.NewMem0Provider("your-api-key", "user-123")

// 自动提取记忆（Mem0 内置 LLM 提取）
mem.Store(ctx, memory.MemoryEntry{
    Key:   "conversation",
    Value: "用户: 我叫小明，来自杭州\n助手: 你好小明！",
})

// 搜索
results, _ := mem.Search(ctx, "用户叫什么名字？", 5)
```

### 5.4 RAG 检索

```go
// 使用向量数据库作为检索器
retriever := vector.NewRetriever(client, embedder, "documents")
mem := memory.NewRAGProvider(retriever, embedder)

// 检索相关文档
prompt, _ := mem.FormatForPrompt(ctx, "如何使用 Go 的 context？", 3)
// 返回相关文档内容，注入到 system prompt
```

### 5.5 组合使用

```go
// 同时使用 KV 存储和向量存储
kvMem, _ := memory.NewKVStore("./memory.json")
vecMem := memory.NewVectorStore(client, embedder, "agent-memory")

// AgentLoopConfig 中使用
config.SystemPrompt = basePrompt + "\n\n"

// 注入精确记忆（KV）
kvPrompt, _ := kvMem.FormatForPrompt(ctx, "", 0) // 全量
config.SystemPrompt += kvPrompt

// 注入相关记忆（向量搜索）
vecPrompt, _ := vecMem.FormatForPrompt(ctx, lastUserMessage, 5)
config.SystemPrompt += vecPrompt
```

---

## 6. AutoLearn 集成

```go
// AutoLearn 支持多种 MemoryProvider
type AutoLearner struct {
    settings Settings
    mem      memory.MemoryProvider  // 接口，不是具体类型
    // ...
}

// 使用向量存储
learner := autolearn.New(vecMem, settings)
learner.ProcessUserInput("我叫小明")
// 自动向量化并存储
```

---

## 7. 实现计划

### Phase 1: 接口抽象 (1 天)
- [ ] 定义 MemoryProvider 接口
- [ ] 适配现有 KVStore 实现
- [ ] 更新 AutoLearner 使用接口

### Phase 2: 向量存储 (2-3 天)
- [ ] VectorClient 接口
- [ ] Embedder 接口
- [ ] Qdrant 实现
- [ ] Chroma 实现

### Phase 3: 外部服务 (1-2 天)
- [ ] Mem0Provider
- [ ] ZepProvider
- [ ] HTTP 客户端封装

### Phase 4: RAG 集成 (1-2 天)
- [ ] Retriever 接口
- [ ] RAGProvider
- [ ] 文档分块策略

**总计: 5-8 天**

---

## 8. 参考项目

| 项目 | 说明 | 语言 |
|------|------|------|
| [Mem0](https://github.com/mem0ai/mem0) | AI 记忆层 | Python/Go |
| [Zep](https://github.com/getzep/zep) | 长期记忆服务 | Go |
| [LangChain Memory](https://python.langchain.com/docs/modules/memory/) | 记忆抽象 | Python |
| [Qdrant](https://github.com/qdrant/qdrant) | 向量数据库 | Rust |
| [Chroma](https://github.com/chroma-core/chroma) | 向量数据库 | Python |

---

## 9. 完整配置管理

### 9.1 YAML 配置

```yaml
# config.yaml
memory:
  # 默认 provider
  default: vector
  
  # Provider 配置
  providers:
    # KV 存储（本地文件）
    kv:
      type: kv
      path: ./data/memory.json
      
    # 向量数据库（Qdrant）
    vector:
      type: vector
      client:
        type: qdrant
        host: localhost
        port: 6333
        collection: agent-memory
      embedder:
        type: openai
        model: text-embedding-3-small
        api_key: ${OPENAI_API_KEY}
        
    # 向量数据库（Chroma）
    chroma:
      type: vector
      client:
        type: chroma
        host: localhost
        port: 8000
        collection: agent-memory
      embedder:
        type: openai
        model: text-embedding-3-small
        
    # Mem0 云服务
    mem0:
      type: mem0
      api_key: ${MEM0_API_KEY}
      user_id: user-123
      
    # Zep 服务
    zep:
      type: zep
      url: http://localhost:8000
      api_key: ${ZEP_API_KEY}
      
    # RAG（文档检索）
    rag:
      type: rag
      retriever:
        type: vector
        client:
          type: qdrant
          host: localhost
          port: 6333
          collection: documents
        embedder:
          type: openai
          model: text-embedding-3-small
          
  # 组合策略
  strategy:
    type: cascade  # cascade | round_robin | primary_fallback
    providers: [kv, vector]
    search_limit: 10
```

### 9.2 配置加载

```go
type MemoryConfig struct {
    Default   string                     `yaml:"default"`
    Providers map[string]ProviderConfig  `yaml:"providers"`
    Strategy  StrategyConfig             `yaml:"strategy"`
}

type ProviderConfig struct {
    Type     string         `yaml:"type"`
    Path     string         `yaml:"path,omitempty"`
    URL      string         `yaml:"url,omitempty"`
    APIKey   string         `yaml:"api_key,omitempty"`
    UserID   string         `yaml:"user_id,omitempty"`
    Client   *ClientConfig  `yaml:"client,omitempty"`
    Embedder *EmbedderConfig `yaml:"embedder,omitempty"`
}

// LoadFromYAML 从 YAML 文件加载配置
func LoadFromYAML(path string) (*MemoryConfig, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, err
    }
    var cfg MemoryConfig
    if err := yaml.Unmarshal(data, &cfg); err != nil {
        return nil, err
    }
    return &cfg, nil
}

// NewFromConfig 根据配置创建 MemoryProvider
func NewFromConfig(cfg ProviderConfig) (MemoryProvider, error) {
    switch cfg.Type {
    case "kv":
        return NewKVStore(cfg.Path)
    case "vector":
        client, err := newVectorClient(cfg.Client)
        if err != nil {
            return nil, err
        }
        embedder, err := newEmbedder(cfg.Embedder)
        if err != nil {
            return nil, err
        }
        return NewVectorStore(client, embedder, cfg.Client.Collection), nil
    case "mem0":
        return NewMem0Provider(cfg.APIKey, cfg.UserID), nil
    case "zep":
        return NewZepProvider(cfg.URL, cfg.APIKey), nil
    default:
        return nil, fmt.Errorf("unknown provider type: %s", cfg.Type)
    }
}
```

---

## 10. 错误处理和重试

### 10.1 错误类型

```go
// MemoryError 记忆系统错误
type MemoryError struct {
    Code    ErrorCode
    Message string
    Err     error
    Provider string
}

type ErrorCode string

const (
    ErrCodeConnection  ErrorCode = "CONNECTION_ERROR"
    ErrCodeTimeout     ErrorCode = "TIMEOUT"
    ErrCodeRateLimit   ErrorCode = "RATE_LIMIT"
    ErrCodeAuth        ErrorCode = "AUTH_ERROR"
    ErrCodeNotFound    ErrorCode = "NOT_FOUND"
    ErrCodeInternal    ErrorCode = "INTERNAL_ERROR"
)

func (e *MemoryError) Error() string {
    return fmt.Sprintf("[%s] %s: %s", e.Provider, e.Code, e.Message)
}

func (e *MemoryError) Unwrap() error { return e.Err }

// IsRetryable 判断是否可重试
func (e *MemoryError) IsRetryable() bool {
    switch e.Code {
    case ErrCodeConnection, ErrCodeTimeout, ErrCodeRateLimit:
        return true
    default:
        return false
    }
}
```

### 10.2 重试包装器

```go
// RetryableProvider 包装 MemoryProvider，添加重试逻辑
type RetryableProvider struct {
    inner      MemoryProvider
    maxRetries int
    baseDelay  time.Duration
    maxDelay   time.Duration
}

func NewRetryableProvider(inner MemoryProvider, maxRetries int) *RetryableProvider {
    return &RetryableProvider{
        inner:      inner,
        maxRetries: maxRetries,
        baseDelay:  1 * time.Second,
        maxDelay:   30 * time.Second,
    }
}

func (p *RetryableProvider) Store(ctx context.Context, entry Entry) error {
    return p.retry(ctx, func() error {
        return p.inner.Store(ctx, entry)
    })
}

func (p *RetryableProvider) Search(ctx context.Context, query string, limit int) ([]Entry, error) {
    var result []Entry
    err := p.retry(ctx, func() error {
        var err error
        result, err = p.inner.Search(ctx, query, limit)
        return err
    })
    return result, err
}

func (p *RetryableProvider) retry(ctx context.Context, fn func() error) error {
    var lastErr error
    for attempt := 0; attempt <= p.maxRetries; attempt++ {
        if attempt > 0 {
            delay := p.baseDelay * time.Duration(1<<uint(attempt-1))
            if delay > p.maxDelay {
                delay = p.maxDelay
            }
            select {
            case <-time.After(delay):
            case <-ctx.Done():
                return ctx.Err()
            }
        }
        
        err := fn()
        if err == nil {
            return nil
        }
        
        lastErr = err
        
        // 检查是否可重试
        var memErr *MemoryError
        if errors.As(err, &memErr) && !memErr.IsRetryable() {
            return err
        }
    }
    return fmt.Errorf("max retries exceeded: %w", lastErr)
}
```

---

## 11. 多 Provider 组合策略

### 11.1 CascadeProvider（级联查询）

```go
// CascadeProvider 依次查询多个 provider，返回第一个成功的结果
type CascadeProvider struct {
    providers []MemoryProvider
    logger    *slog.Logger
}

func NewCascadeProvider(providers ...MemoryProvider) *CascadeProvider {
    return &CascadeProvider{providers: providers}
}

func (p *CascadeProvider) Search(ctx context.Context, query string, limit int) ([]Entry, error) {
    var lastErr error
    for _, provider := range p.providers {
        entries, err := provider.Search(ctx, query, limit)
        if err == nil {
            return entries, nil
        }
        lastErr = err
        p.logger.Warn("provider search failed, trying next", 
            "provider", providerName(provider), "error", err)
    }
    return nil, fmt.Errorf("all providers failed: %w", lastErr)
}

func (p *CascadeProvider) Store(ctx context.Context, entry Entry) error {
    // 写入所有 provider
    var errs []error
    for _, provider := range p.providers {
        if err := provider.Store(ctx, entry); err != nil {
            errs = append(errs, err)
        }
    }
    if len(errs) == len(p.providers) {
        return fmt.Errorf("all providers failed to store: %v", errs)
    }
    return nil
}
```

### 11.2 PrimaryFallbackProvider（主备切换）

```go
// PrimaryFallbackProvider 主 provider 失败时切换到备 provider
type PrimaryFallbackProvider struct {
    primary   MemoryProvider
    fallback  MemoryProvider
    healthy   atomic.Bool
    logger    *slog.Logger
}

func (p *PrimaryFallbackProvider) Search(ctx context.Context, query string, limit int) ([]Entry, error) {
    if p.healthy.Load() {
        entries, err := p.primary.Search(ctx, query, limit)
        if err == nil {
            return entries, nil
        }
        p.logger.Warn("primary provider failed, switching to fallback", "error", err)
        p.healthy.Store(false)
    }
    return p.fallback.Search(ctx, query, limit)
}

// HealthCheck 定期检查主 provider 健康状态
func (p *PrimaryFallbackProvider) HealthCheck(ctx context.Context, interval time.Duration) {
    ticker := time.NewTicker(interval)
    defer ticker.Stop()
    for {
        select {
        case <-ticker.C:
            _, err := p.primary.Search(ctx, "health", 1)
            if err == nil {
                p.healthy.Store(true)
            }
        case <-ctx.Done():
            return
        }
    }
}
```

### 11.3 RoundRobinProvider（轮询）

```go
// RoundRobinProvider 轮询多个 provider
type RoundRobinProvider struct {
    providers []MemoryProvider
    counter   atomic.Uint64
}

func (p *RoundRobinProvider) Search(ctx context.Context, query string, limit int) ([]Entry, error) {
    idx := p.counter.Add(1) % uint64(len(p.providers))
    return p.providers[idx].Search(ctx, query, limit)
}
```

---

## 12. 测试用 Mock Provider

```go
// MockProvider 用于测试的 mock 实现
type MockProvider struct {
    StoreFunc   func(ctx context.Context, entry Entry) error
    SearchFunc  func(ctx context.Context, query string, limit int) ([]Entry, error)
    GetFunc     func(ctx context.Context, key string) (Entry, bool, error)
    DeleteFunc  func(ctx context.Context, key string) error
    entries     map[string]Entry
}

func NewMockProvider() *MockProvider {
    return &MockProvider{
        entries: make(map[string]Entry),
    }
}

func (m *MockProvider) Store(ctx context.Context, entry Entry) error {
    if m.StoreFunc != nil {
        return m.StoreFunc(ctx, entry)
    }
    m.entries[entry.Key] = entry
    return nil
}

func (m *MockProvider) Search(ctx context.Context, query string, limit int) ([]Entry, error) {
    if m.SearchFunc != nil {
        return m.SearchFunc(ctx, query, limit)
    }
    var results []Entry
    for _, e := range m.entries {
        results = append(results, e)
        if limit > 0 && len(results) >= limit {
            break
        }
    }
    return results, nil
}

func (m *MockProvider) Get(ctx context.Context, key string) (Entry, bool, error) {
    if m.GetFunc != nil {
        return m.GetFunc(ctx, key)
    }
    entry, ok := m.entries[key]
    return entry, ok, nil
}

func (m *MockProvider) Delete(ctx context.Context, key string) error {
    if m.DeleteFunc != nil {
        return m.DeleteFunc(ctx, key)
    }
    delete(m.entries, key)
    return nil
}

func (m *MockProvider) FormatForPrompt(ctx context.Context, query string, limit int) (string, error) {
    entries, _ := m.Search(ctx, query, limit)
    return formatEntries(entries), nil
}

func (m *MockProvider) Close() error { return nil }

// WithError 设置下次调用返回错误
func (m *MockProvider) WithError(err error) *MockProvider {
    m.SearchFunc = func(ctx context.Context, query string, limit int) ([]Entry, error) {
        return nil, err
    }
    return m
}
```

---

## 13. 迁移指南

### 13.1 从旧 Memory 迁移

```go
// 旧代码
mem, _ := memory.New("./memory.json")
mem.Set("user.name", "小明")

// 新代码（兼容旧 API）
mem, _ := memory.New("./memory.json")
mem.Set("user.name", "小明")  // 仍然可用

// 新代码（使用 MemoryProvider 接口）
var provider memory.MemoryProvider = memory.NewVectorStoreAdapter(mem)
provider.Store(ctx, memory.Entry{
    Key:   "user.name",
    Value: "小明",
})
```

### 13.2 从 KV 迁移到 Vector

```go
// 1. 读取旧数据
oldMem, _ := memory.New("./memory.json")
entries, _ := oldMem.ListByCategory("")

// 2. 写入新 provider
newMem := memory.NewVectorStore(client, embedder, "agent-memory")
for _, e := range entries {
    newMem.Store(ctx, memory.Entry{
        Key:      e.Key,
        Value:    e.Value,
        Category: e.Category,
    })
}

// 3. 切换配置
// config.yaml: memory.default = vector
```

### 13.3 零停机迁移

```go
// 使用 PrimaryFallbackProvider 实现零停机迁移
oldProvider := memory.NewKVStore("./old-memory.json")
newProvider := memory.NewVectorStore(client, embedder, "new-memory")

// 先从旧 provider 复制数据到新 provider
migrateData(ctx, oldProvider, newProvider)

// 使用 PrimaryFallbackProvider，新 provider 为主
provider := memory.NewPrimaryFallbackProvider(newProvider, oldProvider)

// 验证新 provider 正常后，移除旧 provider
```

---

## 14. 监控和可观测性

### 14.1 指标收集

```go
// MetricsProvider 包装 MemoryProvider，收集指标
type MetricsProvider struct {
    inner     MemoryProvider
    storeOps  *prometheus.CounterVec
    searchOps *prometheus.CounterVec
    latency   *prometheus.HistogramVec
}

func NewMetricsProvider(inner MemoryProvider, reg prometheus.Registerer) *MetricsProvider {
    return &MetricsProvider{
        inner: inner,
        storeOps: promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
            Name: "memory_store_operations_total",
            Help: "Total number of store operations",
        }, []string{"status"}),
        searchOps: promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
            Name: "memory_search_operations_total",
            Help: "Total number of search operations",
        }, []string{"status"}),
        latency: promauto.With(reg).NewHistogramVec(prometheus.HistogramOpts{
            Name:    "memory_operation_duration_seconds",
            Help:    "Duration of memory operations",
            Buckets: prometheus.ExponentialBuckets(0.001, 2, 10),
        }, []string{"operation"}),
    }
}

func (p *MetricsProvider) Store(ctx context.Context, entry Entry) error {
    start := time.Now()
    err := p.inner.Store(ctx, entry)
    p.latency.WithLabelValues("store").Observe(time.Since(start).Seconds())
    if err != nil {
        p.storeOps.WithLabelValues("error").Inc()
    } else {
        p.storeOps.WithLabelValues("success").Inc()
    }
    return err
}
```

### 14.2 健康检查

```go
// HealthChecker 定期检查 provider 健康状态
type HealthChecker struct {
    provider MemoryProvider
    interval time.Duration
    timeout  time.Duration
    status   atomic.Bool
}

func (h *HealthChecker) Start(ctx context.Context) {
    ticker := time.NewTicker(h.interval)
    defer ticker.Stop()
    
    for {
        select {
        case <-ticker.C:
            checkCtx, cancel := context.WithTimeout(ctx, h.timeout)
            _, err := h.provider.Search(checkCtx, "health", 1)
            h.status.Store(err == nil)
            cancel()
        case <-ctx.Done():
            return
        }
    }
}

func (h *HealthChecker) IsHealthy() bool {
    return h.status.Load()
}
```

---

## 15. 性能优化

### 15.1 连接池

```go
// PooledClient 带连接池的向量客户端
type PooledClient struct {
    pool *sync.Pool
    factory func() VectorClient
}

func NewPooledClient(factory func() VectorClient, size int) *PooledClient {
    return &PooledClient{
        factory: factory,
        pool: &sync.Pool{
            New: func() interface{} {
                return factory()
            },
        },
    }
}

func (c *PooledClient) Search(ctx context.Context, collection string, vector []float32, limit int) ([]SearchResult, error) {
    client := c.pool.Get().(VectorClient)
    defer c.pool.Put(client)
    return client.Search(ctx, collection, vector, limit)
}
```

### 15.2 批量操作

```go
// BatchStore 批量存储
func BatchStore(ctx context.Context, provider MemoryProvider, entries []Entry, batchSize int) error {
    for i := 0; i < len(entries); i += batchSize {
        end := i + batchSize
        if end > len(entries) {
            end = len(entries)
        }
        batch := entries[i:end]
        
        // 并行存储
        var wg sync.WaitGroup
        errCh := make(chan error, len(batch))
        for _, entry := range batch {
            wg.Add(1)
            go func(e Entry) {
                defer wg.Done()
                if err := provider.Store(ctx, e); err != nil {
                    errCh <- err
                }
            }(entry)
        }
        wg.Wait()
        close(errCh)
        
        for err := range errCh {
            return err
        }
    }
    return nil
}
```

### 15.3 缓存层

```go
// CachedProvider 带 LRU 缓存的 provider
type CachedProvider struct {
    inner   MemoryProvider
    cache   *lru.Cache[string, Entry]
    ttl     time.Duration
}

func NewCachedProvider(inner MemoryProvider, cacheSize int, ttl time.Duration) *CachedProvider {
    cache, _ := lru.New[string, Entry](cacheSize)
    return &CachedProvider{
        inner: inner,
        cache: cache,
        ttl:   ttl,
    }
}

func (p *CachedProvider) Get(ctx context.Context, key string) (Entry, bool, error) {
    // 先查缓存
    if entry, ok := p.cache.Get(key); ok {
        if time.Since(entry.UpdatedAt) < p.ttl {
            return entry, true, nil
        }
        p.cache.Remove(key)
    }
    
    // 查 provider
    entry, found, err := p.inner.Get(ctx, key)
    if err != nil {
        return Entry{}, false, err
    }
    if found {
        p.cache.Add(key, entry)
    }
    return entry, found, nil
}
```

---

## 16. 完整使用示例

### 16.1 生产环境配置

```go
func setupMemory(cfg *MemoryConfig) (memory.MemoryProvider, error) {
    // 1. 创建主 provider（向量数据库）
    mainProvider, err := memory.NewFromConfig(cfg.Providers["vector"])
    if err != nil {
        return nil, err
    }
    
    // 2. 添加重试
    retryableProvider := memory.NewRetryableProvider(mainProvider, 3)
    
    // 3. 添加缓存
    cachedProvider := memory.NewCachedProvider(retryableProvider, 1000, 5*time.Minute)
    
    // 4. 添加指标
    metricsProvider := memory.NewMetricsProvider(cachedProvider, prometheus.DefaultRegisterer)
    
    // 5. 启动健康检查
    healthChecker := memory.NewHealthChecker(metricsProvider, 30*time.Second, 5*time.Second)
    go healthChecker.Start(context.Background())
    
    return metricsProvider, nil
}
```

### 16.2 开发环境配置

```go
func setupMemoryDev() memory.MemoryProvider {
    // 简单的内存存储
    return memory.NewKVStore("./dev-memory.json")
}
```

### 16.3 测试配置

```go
func setupMemoryTest() memory.MemoryProvider {
    // Mock provider
    return memory.NewMockProvider()
}
