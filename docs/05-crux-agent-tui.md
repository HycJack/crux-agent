# crux-agent-tui 模块设计

## 一、模块概述

`crux-agent-tui` 是 Crux 框架的终端用户界面模块，提供一个交互式的 REPL（Read-Eval-Print Loop）编码 Agent。它基于所有下层模块构建，提供跨平台的终端体验。

## 二、核心组件

### 2.1 目录结构

```
crux-agent-tui/
├── main.go                    # 入口点，REPL循环
├── internal/
│   └── app/
│       └── app.go             # 应用逻辑
├── tui/
│   └── tui.go                 # TUI渲染
├── .gitignore
├── README.md
└── go.mod
```

### 2.2 模块职责

| 组件 | 职责 |
|------|------|
| `main.go` | 程序入口，REPL循环，命令解析 |
| `internal/app/app.go` | 应用核心逻辑，状态管理 |
| `tui/tui.go` | 终端界面渲染，用户输入处理 |

## 三、REPL 循环

### 3.1 主循环

```go
func main() {
    // 初始化应用
    app := app.New()
    
    // 初始化 TUI
    tui := tui.New()
    
    // 主循环
    for {
        // 显示提示
        tui.PrintPrompt()
        
        // 读取用户输入
        input := tui.ReadInput()
        
        // 解析命令
        cmd, args := parseCommand(input)
        
        // 执行命令
        switch cmd {
        case "/help":
            tui.PrintHelp()
        case "/clear":
            tui.Clear()
        case "/tools":
            tui.PrintTools(app.GetTools())
        case "/paste":
            app.PasteImage(args[0])
        case "/clearimg":
            app.ClearImages()
        case "/quit":
            return
        default:
            // 发送消息给 Agent
            response := app.SendMessage(input)
            tui.PrintResponse(response)
        }
    }
}
```

### 3.2 命令解析

```go
func parseCommand(input string) (string, []string) {
    if !strings.HasPrefix(input, "/") {
        return "", []string{input}
    }
    
    parts := strings.Fields(input)
    if len(parts) == 0 {
        return "", nil
    }
    
    cmd := parts[0]
    args := parts[1:]
    
    return cmd, args
}
```

### 3.3 支持的命令

| 命令 | 功能 | 参数 |
|------|------|------|
| `/help` | 显示帮助信息 | 无 |
| `/clear` | 清空终端 | 无 |
| `/tools` | 列出可用工具 | 无 |
| `/paste` | 粘贴图片 | 图片路径 |
| `/clearimg` | 清除已粘贴的图片 | 无 |
| `/quit` | 退出程序 | 无 |

## 四、应用逻辑

### 4.1 App 结构体

```go
type App struct {
    agent     *agent.Agent
    tools     []agent.AgentTool
    images    []core.ImageContent
    config    Config
    observer  *observe.Observer
    checkpoint *checkpoint.Checkpoint
}
```

### 4.2 初始化

```go
func New() *App {
    // 加载配置
    config := LoadConfig()
    
    // 创建 Agent
    agent := agent.New(agent.AgentOptions{
        InitialState: &agent.AgentState{
            Model:        config.Model,
            SystemPrompt: buildSystemPrompt(config),
            Tools:        loadTools(),
        },
    })
    
    // 初始化组件
    observer := observe.New(os.Stdout)
    checkpoint := checkpoint.New()
    
    // 订阅事件
    agent.Subscribe(func(evt agent.AgentEvent) {
        observer.LogEvent(evt)
        renderEvent(evt)
    })
    
    return &App{
        agent:      agent,
        config:     config,
        observer:   observer,
        checkpoint: checkpoint,
    }
}
```

### 4.3 系统提示词构建

```go
func buildSystemPrompt(config Config) string {
    builder := prompt.NewBuilder()
    
    builder.AddSection("Role", "你是一个有帮助的编码助手。")
    
    builder.AddSection("Environment", fmt.Sprintf(`
- 工作目录: %s
- 操作系统: %s
- 架构: %s
- 时间: %s`,
        config.WorkingDir,
        runtime.GOOS,
        runtime.GOARCH,
        time.Now().Format(time.RFC3339),
    ))
    
    builder.AddSection("Instructions", `
- 使用提供的工具完成任务
- 对于文件操作，始终使用完整路径
- 如果遇到问题，尝试调试并提供解决方案
- 保持回答简洁明了
`)
    
    return builder.Build()
}
```

### 4.4 发送消息

```go
func (app *App) SendMessage(text string) error {
    // 创建用户消息
    msg := core.UserMessage{
        Role:      "user",
        Content:   text,
        Timestamp: time.Now(),
    }
    
    // 添加图片（如果有）
    if len(app.images) > 0 {
        content := []core.ContentBlock{core.TextContent{Type: "text", Text: text}}
        for _, img := range app.images {
            content = append(content, img)
        }
        msg.Content = content
        app.images = nil // 清空图片
    }
    
    // 保存检查点
    app.checkpoint.Save(app.agent.Messages(), app.agent.State().Model, app.agent.State().SystemPrompt)
    
    // 运行 Agent
    _, err := app.agent.Run(context.Background(), msg)
    return err
}
```

### 4.5 图片处理

```go
func (app *App) PasteImage(path string) error {
    // 读取图片文件
    data, err := os.ReadFile(path)
    if err != nil {
        return err
    }
    
    // 检查文件大小（最大 8 MiB）
    if len(data) > 8 * 1024 * 1024 {
        return fmt.Errorf("image too large (max 8 MiB)")
    }
    
    // 检测 MIME 类型
    mimeType := http.DetectContentType(data)
    
    // 创建图片内容
    img := core.ImageContent{
        Type:     "image",
        Data:     base64.StdEncoding.EncodeToString(data),
        MimeType: mimeType,
    }
    
    app.images = append(app.images, img)
    return nil
}

func (app *App) ClearImages() {
    app.images = nil
}
```

## 五、TUI 渲染

### 5.1 TUI 结构体

```go
type TUI struct {
    reader  *bufio.Reader
    writer  io.Writer
    width   int
    height  int
}

func New() *TUI {
    return &TUI{
        reader: bufio.NewReader(os.Stdin),
        writer: os.Stdout,
    }
}
```

### 5.2 提示输出

```go
func (t *TUI) PrintPrompt() {
    fmt.Fprint(t.writer, "\n👤 You: ")
}

func (t *TUI) PrintResponse(response core.AssistantMessage) {
    fmt.Fprint(t.writer, "\n🤖 Assistant: ")
    
    for _, block := range response.Content {
        switch b := block.(type) {
        case core.TextContent:
            fmt.Fprint(t.writer, b.Text)
        case core.ThinkingContent:
            fmt.Fprint(t.writer, fmt.Sprintf("\n[思考] %s", b.Thinking))
        case core.ToolCall:
            fmt.Fprint(t.writer, fmt.Sprintf("\n[工具调用] %s(%s)", b.Name, b.Arguments))
        }
    }
}
```

### 5.3 输入读取

```go
func (t *TUI) ReadInput() string {
    input, _ := t.reader.ReadString('\n')
    return strings.TrimSpace(input)
}
```

### 5.4 帮助信息

```go
func (t *TUI) PrintHelp() {
    help := `
可用命令:
/help       - 显示此帮助信息
/clear      - 清空终端
/tools      - 列出可用工具
/paste <path> - 粘贴图片（支持 jpg, jpeg, png, gif, webp）
/clearimg   - 清除已粘贴的图片
/quit       - 退出程序

直接输入文本即可发送消息给助手。
`
    fmt.Fprint(t.writer, help)
}
```

### 5.5 工具列表

```go
func (t *TUI) PrintTools(tools []agent.AgentTool) {
    fmt.Fprintln(t.writer, "\n可用工具:")
    for _, tool := range tools {
        fmt.Fprintf(t.writer, "  • %s: %s\n", tool.Name, tool.Description)
    }
}
```

## 六、事件渲染

### 6.1 事件处理器

```go
func renderEvent(evt agent.AgentEvent) {
    switch e := evt.(type) {
    case agent.EventMessageUpdate:
        // 渲染增量更新
        renderMessageUpdate(e)
    case agent.EventToolExecStart:
        fmt.Printf("\n[工具执行开始] %s\n", e.ToolName)
    case agent.EventToolExecUpdate:
        fmt.Printf("[工具输出] %s\n", string(e.PartialResult))
    case agent.EventToolExecEnd:
        if e.IsError {
            fmt.Printf("[工具执行错误] %s: %s\n", e.ToolName, string(e.Result))
        } else {
            fmt.Printf("[工具执行完成] %s\n", e.ToolName)
        }
    }
}

func renderMessageUpdate(evt agent.EventMessageUpdate) {
    // 获取增量内容
    delta := extractDelta(evt.AssistantEvent)
    if delta != "" {
        fmt.Print(delta)
    }
}

func extractDelta(evt core.AssistantMessageEvent) string {
    switch e := evt.(type) {
    case core.EventTextDelta:
        return e.Delta
    case core.EventThinkingDelta:
        return fmt.Sprintf("\r[思考] %s", e.Delta)
    default:
        return ""
    }
}
```

## 七、配置管理

### 7.1 Config 结构体

```go
type Config struct {
    Model        core.Model
    WorkingDir   string
    MaxTokens    int
    Temperature  float64
    Shell        string
}
```

### 7.2 配置加载

```go
func LoadConfig() Config {
    // 从环境变量加载
    env := os.Environ()
    
    config := Config{
        WorkingDir:  getEnv("CRUX_WORKING_DIR", "."),
        MaxTokens:   getEnvInt("AI_MAX_TOKENS", 4096),
        Temperature: getEnvFloat("AI_TEMPERATURE", 0.7),
        Shell:       getEnv("CRUX_SHELL", defaultShell()),
    }
    
    // 解析模型
    config.Model = resolveModel()
    
    return config
}

func resolveModel() core.Model {
    // 优先使用环境变量指定的模型
    provider := os.Getenv("AI_PROVIDER")
    modelID := os.Getenv("AI_MODEL")
    
    if provider != "" && modelID != "" {
        model, err := ai.GetModel(core.KnownProvider(provider), modelID)
        if err == nil {
            return model
        }
    }
    
    // 自动检测
    return autoDetectModel()
}

func autoDetectModel() core.Model {
    // 按优先级检测环境变量
    providers := []core.KnownProvider{
        core.ProviderAnthropic,
        core.ProviderOpenAI,
        core.ProviderDeepSeek,
        core.ProviderMistral,
        core.ProviderGoogle,
    }
    
    for _, p := range providers {
        if core.GetEnvAPIKey(p) != "" {
            models := ai.GetModels(p)
            if len(models) > 0 {
                return models[0]
            }
        }
    }
    
    // 默认返回 Claude Sonnet
    return core.Model{
        ID:      "claude-sonnet-4-20250514",
        API:     core.APIAnthropicMessages,
        Provider: core.ProviderAnthropic,
    }
}
```

### 7.3 默认 Shell

```go
func defaultShell() string {
    switch runtime.GOOS {
    case "windows":
        return "pwsh"
    case "darwin", "linux":
        return "bash"
    default:
        return "bash"
    }
}
```

## 八、工具加载

### 8.1 内置工具

```go
func loadTools() []agent.AgentTool {
    return []agent.AgentTool{
        {
            Name:        "bash",
            Description: "Execute shell commands",
            Parameters:  json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}}}`),
            Execute:     executeBash,
        },
        {
            Name:        "read_file",
            Description: "Read a file",
            Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
            Execute:     executeReadFile,
        },
        {
            Name:        "write_file",
            Description: "Write to a file",
            Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}}}`),
            Execute:     executeWriteFile,
        },
        {
            Name:        "list_files",
            Description: "List files in directory",
            Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
            Execute:     executeListFiles,
        },
    }
}
```

### 8.2 Shell 执行

```go
func executeBash(ctx context.Context, toolCallID string, params json.RawMessage, 
    onUpdate func(json.RawMessage)) (agent.AgentToolResult, error) {
    
    var args struct {
        Command string `json:"command"`
    }
    json.Unmarshal(params, &args)
    
    // 选择 shell
    shell := os.Getenv("CRUX_SHELL")
    if shell == "" {
        shell = defaultShell()
    }
    
    // 构建命令
    var cmd *exec.Cmd
    if shell == "pwsh" || shell == "powershell" {
        cmd = exec.CommandContext(ctx, shell, "-Command", args.Command)
    } else {
        cmd = exec.CommandContext(ctx, shell, "-c", args.Command)
    }
    
    // 设置工作目录
    cmd.Dir = os.Getenv("CRUX_WORKING_DIR")
    
    // 执行命令
    output, err := cmd.CombinedOutput()
    
    if err != nil {
        return agent.AgentToolResult{
            Content: []core.ContentBlock{core.TextContent{Type: "text", Text: string(output)}},
            IsError: true,
        }, nil
    }
    
    return agent.AgentToolResult{
        Content: []core.ContentBlock{core.TextContent{Type: "text", Text: string(output)}},
        IsError: false,
    }, nil
}
```

### 8.3 文件操作

```go
func executeReadFile(ctx context.Context, toolCallID string, params json.RawMessage, 
    onUpdate func(json.RawMessage)) (agent.AgentToolResult, error) {
    
    var args struct {
        Path string `json:"path"`
    }
    json.Unmarshal(params, &args)
    
    content, err := os.ReadFile(args.Path)
    if err != nil {
        return agent.AgentToolResult{
            Content: []core.ContentBlock{core.TextContent{Type: "text", Text: err.Error()}},
            IsError: true,
        }, nil
    }
    
    return agent.AgentToolResult{
        Content: []core.ContentBlock{core.TextContent{Type: "text", Text: string(content)}},
        IsError: false,
    }, nil
}

func executeWriteFile(ctx context.Context, toolCallID string, params json.RawMessage, 
    onUpdate func(json.RawMessage)) (agent.AgentToolResult, error) {
    
    var args struct {
        Path    string `json:"path"`
        Content string `json:"content"`
    }
    json.Unmarshal(params, &args)
    
    err := os.WriteFile(args.Path, []byte(args.Content), 0644)
    if err != nil {
        return agent.AgentToolResult{
            Content: []core.ContentBlock{core.TextContent{Type: "text", Text: err.Error()}},
            IsError: true,
        }, nil
    }
    
    return agent.AgentToolResult{
        Content: []core.ContentBlock{core.TextContent{Type: "text", Text: "File written successfully"}},
        IsError: false,
    }, nil
}

func executeListFiles(ctx context.Context, toolCallID string, params json.RawMessage, 
    onUpdate func(json.RawMessage)) (agent.AgentToolResult, error) {
    
    var args struct {
        Path string `json:"path"`
    }
    json.Unmarshal(params, &args)
    
    if args.Path == "" {
        args.Path = "."
    }
    
    files, err := os.ReadDir(args.Path)
    if err != nil {
        return agent.AgentToolResult{
            Content: []core.ContentBlock{core.TextContent{Type: "text", Text: err.Error()}},
            IsError: true,
        }, nil
    }
    
    var output []string
    for _, file := range files {
        info, _ := file.Info()
        output = append(output, fmt.Sprintf("%s\t%s\t%s", 
            info.Mode().String(),
            info.ModTime().Format("2006-01-02 15:04"),
            file.Name(),
        ))
    }
    
    return agent.AgentToolResult{
        Content: []core.ContentBlock{core.TextContent{Type: "text", Text: strings.Join(output, "\n")}},
        IsError: false,
    }, nil
}
```

## 九、跨平台支持

### 9.1 Windows 终端处理

```go
func init() {
    // 在 Windows 上启用虚拟终端处理
    if runtime.GOOS == "windows" {
        enableVirtualTerminalProcessing()
    }
}

func enableVirtualTerminalProcessing() {
    // 获取标准输出句柄
    stdout := syscall.Stdout
    var mode uint32
    
    // 获取当前模式
    kernel32 := syscall.NewLazyDLL("kernel32.dll")
    getConsoleMode := kernel32.NewProc("GetConsoleMode")
    setConsoleMode := kernel32.NewProc("SetConsoleMode")
    
    getConsoleMode.Call(uintptr(stdout), uintptr(unsafe.Pointer(&mode)))
    
    // 启用虚拟终端处理
    mode |= 0x0004 // ENABLE_VIRTUAL_TERMINAL_PROCESSING
    setConsoleMode.Call(uintptr(stdout), uintptr(mode))
}
```

### 9.2 信号处理

```go
func setupSignalHandler() {
    sigChan := make(chan os.Signal, 1)
    signal.Notify(sigChan, os.Interrupt)
    
    go func() {
        count := 0
        for range sigChan {
            count++
            if count >= 2 {
                fmt.Println("\n强制退出")
                os.Exit(0)
            }
            fmt.Println("\n按 Ctrl+C 再次退出，或继续输入")
        }
    }()
}
```

## 十、错误处理

### 10.1 错误显示

```go
func handleError(err error) {
    if err != nil {
        fmt.Printf("\n❌ 错误: %v\n", err)
    }
}
```

### 10.2 优雅退出

```go
func cleanup() {
    // 清理资源
    fmt.Println("\n再见！")
}

func main() {
    defer cleanup()
    setupSignalHandler()
    
    // ... 主循环
}
```

## 十一、扩展点

### 11.1 添加新命令

```go
// 在 parseCommand 中添加新命令
switch cmd {
case "/newcmd":
    handleNewCommand(args)
}
```

### 11.2 添加新工具

```go
// 在 loadTools 中添加新工具
{
    Name:        "my_tool",
    Description: "My custom tool",
    Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
    Execute:     executeMyTool,
},
```

### 11.3 自定义渲染

```go
// 在 renderEvent 中添加新事件处理
case agent.EventCustom:
    renderCustomEvent(e)
```

## 十二、总结

`crux-agent-tui` 提供了一个完整的终端界面，包括：

1. **REPL 循环**：交互式命令行界面
2. **命令系统**：支持常用命令（/help, /clear, /tools, /paste, /quit）
3. **图片支持**：粘贴图片进行多模态交互
4. **工具集成**：内置 shell、文件操作工具
5. **跨平台**：支持 Windows、macOS、Linux
6. **优雅退出**：支持 Ctrl+C 中断

它是一个完整的 Agent 应用示例，可以作为构建更复杂应用的起点。