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
