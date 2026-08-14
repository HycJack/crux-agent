package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	stdruntime "runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hycjack/agent-engine/defaults"
	"github.com/hycjack/agent-engine/engine"
	pluginsession "github.com/hycjack/agent-engine/plugin"
	"github.com/hycjack/crux-ai/ai"
	core "github.com/hycjack/crux-ai/core"
	plugin "github.com/hycjack/crux-plugin"

	// Register built-in LLM providers (OpenAI, Anthropic, Google, etc.).
	_ "github.com/hycjack/crux-ai/providers"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"chat-app/logutil"
	"chat-app/skillutil"
	"chat-app/tools"
)

// App is the Wails application struct bound to the frontend.
type App struct {
	ctx context.Context

	mu         sync.RWMutex
	workingDir string
	cancelFn   context.CancelFunc
	agt        *engine.Agent

	// Cross-session long-term memory
	mem    *defaults.Memory
	memDir string

	// Persistent conversation session (JSONL), aligned with runtime session.
	// sess holds entry history for cross-restart conversation recovery.
	sess *defaults.JSONLSession

	// Skill loader
	skillLoader *skillutil.Loader

	// crux-plugin: subprocess JSON-RPC plugin manager + discovered tools
	pluginMgr *plugin.Manager
	pluginCtx context.Context
	pluginCnl context.CancelFunc

	// Auto-learn
	learner          *defaults.AutoLearner
	wfDir            string
	wfExtractor      *defaults.WorkflowExtractor
	autoLearnEnabled bool
}

// NewApp creates a new App instance.
func NewApp() *App {
	// Initialize skill loader
	sl := skillutil.NewLoader()

	return &App{
		skillLoader: sl,
	}
}

// startup is called by Wails when the app is ready.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// Restore working directory from persisted settings FIRST so that
	// all subsequent init (skills, memory, autolearn) uses the correct
	// working directory from the start.
	var s PersistedSettings
	if err := loadJSON("settings.json", &s); err == nil && s.WorkingDir != "" {
		_ = a.SetWorkingDir(s.WorkingDir)
	}
	// If no persisted working dir, default to the executable's current dir.
	if a.GetWorkingDir() == "" {
		cwd, _ := os.Getwd()
		if cwd != "" {
			a.mu.Lock()
			a.workingDir = cwd
			a.mu.Unlock()
		}
	}
	// Load skills using the resolved working directory.
	_ = a.skillLoader.LoadAll(a.GetWorkingDir())

	// Initialize logging to <appDataDir>/logs/<YYYY-MM-DD>.log
	if appDir, err := appDataDir(); err == nil {
		if err := logutil.Init(appDir); err != nil {
			fmt.Fprintf(os.Stderr, "[logutil] init failed: %v\n", err)
		} else {
			logutil.Infof("Crux Agent Chat started")
		}
	}

	// Initialize long-term memory
	if appDir, err := appDataDir(); err == nil {
		a.memDir = appDir
		memPath := filepath.Join(appDir, "memory.json")
		mem, err := defaults.NewMemory(memPath)
		if err != nil {
			logutil.Warnf("Failed to init memory: %v", err)
		} else {
			a.mem = mem
			logutil.Infof("Memory loaded (%d entries)", mem.Size())
		}
	}

	// Persistent conversation session (JSONL).
	if appDir, err := appDataDir(); err == nil {
		if sess, err := defaults.NewJSONLSession(filepath.Join(appDir, "session.jsonl")); err != nil {
			logutil.Warnf("Failed to init session: %v", err)
		} else {
			a.sess = sess
			logutil.Infof("Session loaded (%d entries)", len(sess.Entries()))
		}
	}

	logutil.Infof("App startup complete")

	// Initialize crux-plugin: discover + start subprocess tool plugins in
	// the conventional plugin directories. Missing dirs are skipped silently.
	a.initPlugins(ctx)
}

// AppDataDir returns the OS-conventional directory for app data.
// Re-exposed as a Wails-bound method so the frontend can check it.
func (a *App) AppDataDir() (string, error) {
	return appDataDir()
}

// -------------------- Working directory --------------------

// SetWorkingDir updates the working directory used for file/shell tools.
func (a *App) SetWorkingDir(dir string) error {
	if dir == "" {
		return nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("working directory is not accessible: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("working directory is not a directory: %s", abs)
	}
	a.mu.Lock()
	a.workingDir = abs
	a.mu.Unlock()

	// Reload skills when working directory changes
	a.mu.RLock()
	sl := a.skillLoader
	a.mu.RUnlock()
	if sl != nil {
		_ = sl.Reload(abs)
		count := sl.Count()
		if count > 0 {
			logutil.Infof("Reloaded %d skills from %s", count, abs)
		}
	}

	return nil
}

// GetWorkingDir returns the current working directory.
func (a *App) GetWorkingDir() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.workingDir
}

// PickWorkingDir opens a native directory picker and returns the selected path.
// Returns empty string if the user cancels.
func (a *App) PickWorkingDir() (string, error) {
	dir, err := wruntime.OpenDirectoryDialog(a.ctx, wruntime.OpenDialogOptions{
		Title:                "Choose working directory",
		CanCreateDirectories: true,
	})
	if err != nil {
		return "", err
	}
	if dir == "" {
		return "", nil
	}
	if err := a.SetWorkingDir(dir); err != nil {
		return "", err
	}
	return a.GetWorkingDir(), nil
}

// -------------------- Skill management --------------------

// GetSkills returns the list of loaded skill names.
func (a *App) GetSkills() []string {
	a.mu.RLock()
	sl := a.skillLoader
	a.mu.RUnlock()
	if sl == nil {
		return nil
	}
	return sl.List()
}

// SkillInfo is the rich per-skill metadata returned to the frontend so
// the Settings panel can badge bundled vs user-authored entries.
type SkillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"` // "user" or "bundled"
}

// ListSkills returns the full skill catalog. Frontends that only need
// flat names should keep using GetSkills.
func (a *App) ListSkills() []SkillInfo {
	a.mu.RLock()
	sl := a.skillLoader
	a.mu.RUnlock()
	if sl == nil {
		return nil
	}
	all := sl.All()
	out := make([]SkillInfo, 0, len(all))
	for _, s := range all {
		out = append(out, SkillInfo{
			Name:        s.Name,
			Description: s.Description,
			Source:      s.Source,
		})
	}
	return out
}

// GetSkillContent returns the full content of a skill by name.
func (a *App) GetSkillContent(name string) string {
	a.mu.RLock()
	sl := a.skillLoader
	a.mu.RUnlock()
	if sl == nil {
		return ""
	}
	skill, ok := sl.Get(name)
	if !ok {
		return ""
	}
	return skill.Content
}

// ReloadSkills rescans the skills directory.
func (a *App) ReloadSkills(dir string) error {
	a.mu.RLock()
	sl := a.skillLoader
	a.mu.RUnlock()
	if sl == nil {
		return nil
	}
	return sl.Reload(dir)
}

// -------------------- Memory management --------------------

// GetMemories returns all long-term memory entries as key=value pairs.
func (a *App) GetMemories() map[string]string {
	a.mu.RLock()
	mem := a.mem
	a.mu.RUnlock()
	if mem == nil {
		return nil
	}
	out := make(map[string]string)
	for _, k := range mem.Keys() {
		if v, ok := mem.Get(k); ok {
			out[k] = v
		}
	}
	return out
}

// SetMemory stores a specific memory key-value pair.
func (a *App) SetMemory(key, value string) {
	a.mu.RLock()
	mem := a.mem
	a.mu.RUnlock()
	if mem != nil {
		mem.Set(key, value)
		_ = mem.Save()
		logutil.Infof("[memory] set %s=%s", key, value)
	}
}

// DeleteMemory deletes a specific memory key.
func (a *App) DeleteMemory(key string) {
	a.mu.RLock()
	mem := a.mem
	a.mu.RUnlock()
	if mem != nil {
		mem.Delete(key)
		_ = mem.Save()
		logutil.Infof("Memory deleted: %s", key)
	}
}

// ClearMemory clears all long-term memory.
func (a *App) ClearMemory() {
	a.mu.RLock()
	mem := a.mem
	a.mu.RUnlock()
	if mem != nil {
		for _, k := range mem.Keys() {
			mem.Delete(k)
		}
		_ = mem.Save()
		logutil.Infof("All memory cleared")
	}
}

// ─── Persistent session (aligned with runtime session) ─────────────────────

// sessionMessages returns the conversation history persisted in the session,
// or nil if no session is available.
func (a *App) sessionMessages() []core.Message {
	a.mu.RLock()
	sess := a.sess
	a.mu.RUnlock()
	if sess == nil {
		return nil
	}
	return sess.BuildContext().Messages
}

// persistTurnDelta appends the messages added to the agent since baseline to
// the persistent session. Each call only writes the delta, so repeated turns
// accumulate history without duplicates.
func (a *App) persistTurnDelta(msgs []core.Message, baseline int) {
	a.mu.RLock()
	sess := a.sess
	a.mu.RUnlock()
	if sess == nil {
		return
	}
	if baseline < 0 {
		baseline = 0
	}
	if baseline >= len(msgs) {
		return
	}
	entries := make([]pluginsession.SessionTreeEntry, 0, len(msgs)-baseline)
	for _, msg := range msgs[baseline:] {
		entries = append(entries, defaults.NewMessageEntry("", msg))
	}
	if err := sess.Append(entries...); err != nil {
		logutil.Warnf("Failed to persist session: %v", err)
	}
}

// ClearSession wipes the persisted conversation so a new conversation starts
// fresh. Returns the number of entries removed.
func (a *App) ClearSession() int {
	a.mu.RLock()
	sess := a.sess
	a.mu.RUnlock()
	if sess == nil {
		return 0
	}
	n := len(sess.Entries())
	if err := a.resetSessionFile(); err != nil {
		logutil.Warnf("Failed to clear session: %v", err)
	}
	return n
}

// resetSessionFile reopens an empty JSONL session file, discarding all entries.
func (a *App) resetSessionFile() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sess == nil {
		return nil
	}
	_ = a.sess.Close()
	appDir, err := appDataDir()
	if err != nil {
		return err
	}
	sess, err := defaults.NewJSONLSession(filepath.Join(appDir, "session.jsonl"))
	if err != nil {
		return err
	}
	a.sess = sess
	return nil
}

// SessionMessageCount returns how many messages are persisted in the session
// (for the frontend to know whether history recovery is available).
func (a *App) SessionMessageCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.sess == nil {
		return 0
	}
	return len(a.sess.Entries())
}

// GetToolList returns the list of currently available agent tool names.
func (a *App) GetToolList() []string {
	a.mu.RLock()
	agt := a.agt
	a.mu.RUnlock()
	if agt == nil {
		return nil
	}
	tools := agt.State().Tools
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name
	}
	return names
}

// GetCompactionStatus returns a summary of the compaction config.
func (a *App) GetCompactionStatus() string {
	a.mu.RLock()
	agt := a.agt
	a.mu.RUnlock()
	if agt == nil {
		return "No agent (send a message first)"
	}
	c := agt.Compaction()
	return fmt.Sprintf("MaxTokens: %d, ReserveTokens: %d, OverflowRetries: %d, Compactor: %T",
		c.MaxTokens, c.ReserveTokens, c.OverflowRetries, c.Compactor)
}

// -------------------- File tree & preview API --------------------

// FileNode represents a single entry in the file tree.
type FileNode struct {
	Name     string     `json:"name"`
	Path     string     `json:"path"`
	IsDir    bool       `json:"isDir"`
	Size     int64      `json:"size,omitempty"`
	Children []FileNode `json:"children,omitempty"`
}

// GetFileTree returns a recursive file tree rooted at the working directory.
// It skips common ignore files (node_modules, .git, etc.) for performance.
func (a *App) GetFileTree() (FileNode, error) {
	root := a.GetWorkingDir()
	if root == "" {
		return FileNode{}, fmt.Errorf("working directory not set")
	}

	rootInfo, err := os.Stat(root)
	if err != nil {
		return FileNode{}, fmt.Errorf("cannot access working directory: %w", err)
	}

	rootName := filepath.Base(root)
	if rootName == "" {
		rootName = root
	}

	return FileNode{
		Name:  rootName,
		Path:  root,
		IsDir: true,
		Size:  rootInfo.Size(),
	}, nil
}

// shouldSkipDir returns true for directories that should not be traversed.
func shouldSkipDir(name string) bool {
	switch name {
	case "node_modules", ".git", ".svn", ".hg", ".idea", ".vscode",
		"__pycache__", ".next", ".turbo", "dist", "build", ".cache",
		"target", "vendor", ".tox", ".eggs", ".mypy_cache", ".pytest_cache":
		return true
	}
	return false
}

// shouldSkipFile returns true for files that should be excluded from the tree.
func shouldSkipFile(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".pyc") || strings.HasSuffix(lower, ".pyo")
}

// maxTreeDepth defines how deep the file tree can go to avoid performance issues.
const maxTreeDepth = 6

// readDirRecursive recursively reads a directory up to a given depth.
func readDirRecursive(dir string, depth int) ([]FileNode, error) {
	if depth > maxTreeDepth {
		return nil, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	// Sort: directories first, then files, both alphabetical
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})

	nodes := make([]FileNode, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue // skip hidden files/dirs
		}
		if entry.IsDir() && shouldSkipDir(name) {
			continue
		}
		if !entry.IsDir() && shouldSkipFile(name) {
			continue
		}

		fullPath := filepath.Join(dir, name)
		info, err := entry.Info()
		if err != nil {
			continue
		}

		node := FileNode{
			Name:  name,
			Path:  fullPath,
			IsDir: entry.IsDir(),
			Size:  info.Size(),
		}

		if entry.IsDir() {
			children, err := readDirRecursive(fullPath, depth+1)
			if err == nil {
				node.Children = children
			}
		}

		nodes = append(nodes, node)
	}

	return nodes, nil
}

// GetFileTreeExpanded returns a fully expanded file tree for the working directory.
func (a *App) GetFileTreeExpanded() (FileNode, error) {
	root := a.GetWorkingDir()
	if root == "" {
		return FileNode{}, fmt.Errorf("working directory not set")
	}

	rootInfo, err := os.Stat(root)
	if err != nil {
		return FileNode{}, fmt.Errorf("cannot access working directory: %w", err)
	}

	rootName := filepath.Base(root)
	if rootName == "" {
		rootName = root
	}

	children, err := readDirRecursive(root, 0)
	if err != nil {
		return FileNode{}, fmt.Errorf("read directory: %w", err)
	}

	return FileNode{
		Name:     rootName,
		Path:     root,
		IsDir:    true,
		Size:     rootInfo.Size(),
		Children: children,
	}, nil
}

// FileContent is the result of reading a file.
type FileContent struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Content  string `json:"content"`
	Size     int64  `json:"size"`
	IsBinary bool   `json:"isBinary"`
	Encoding string `json:"encoding,omitempty"` // "base64" for binary files
}

// maxPreviewSize is the maximum file size (in bytes) we'll attempt to preview.
const maxPreviewSize int64 = 50 * 1024 * 1024 // 50 MB

// textExtensions lists extensions that should be treated as text files.
var textExtensions = map[string]bool{
	".txt": true, ".md": true, ".markdown": true, ".html": true, ".htm": true,
	".css": true, ".scss": true, ".less": true, ".js": true, ".jsx": true,
	".ts": true, ".tsx": true, ".json": true, ".xml": true, ".yaml": true,
	".yml": true, ".toml": true, ".ini": true, ".cfg": true, ".conf": true,
	".go": true, ".py": true, ".rs": true, ".java": true, ".c": true,
	".cpp": true, ".h": true, ".hpp": true, ".cs": true, ".rb": true,
	".php": true, ".swift": true, ".kt": true, ".scala": true, ".sh": true,
	".bat": true, ".ps1": true, ".env": true, ".gitignore": true,
	".dockerfile": true, ".vue": true, ".svelte": true, ".astro": true,
	".sql": true, ".r": true, ".pl": true, ".lua": true, ".dart": true,
	".proto": true, ".gradle": true, ".mjs": true, ".cjs": true,
	".makefile": true, "makefile": true, "dockerfile": true,
}

var previewExtensions = map[string]bool{
	".pdf": true, ".docx": true, ".pptx": true, ".xlsx": true,
}

// isTextFile checks if the file extension is a known text format.
func isTextFile(ext string) bool {
	return textExtensions[strings.ToLower(ext)]
}

// isPreviewFile checks if the file can be previewed with special viewers.
func isPreviewFile(ext string) bool {
	return previewExtensions[strings.ToLower(ext)]
}

// ReadFileContent reads the content of a file from the working directory.
// It returns base64-encoded content for binary files and plain text for text files.
func (a *App) ReadFileContent(filePath string) (*FileContent, error) {
	wd := a.GetWorkingDir()
	if wd == "" {
		return nil, fmt.Errorf("working directory not set")
	}

	// Security: ensure the path is within the working directory
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}

	// Resolve symlinks to their real path before checking
	realPath := absPath
	if resolved, err := filepath.EvalSymlinks(absPath); err == nil {
		realPath = resolved
	}

	wdAbs, _ := filepath.Abs(wd)
	if !strings.HasPrefix(realPath, wdAbs) {
		return nil, fmt.Errorf("access denied: file is outside working directory")
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("cannot access file: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("path is a directory, not a file")
	}

	// Check file size
	if info.Size() > maxPreviewSize {
		return nil, fmt.Errorf("file too large to preview (%d MB)", info.Size()/(1024*1024))
	}

	f, err := os.Open(absPath)
	if err != nil {
		return nil, fmt.Errorf("cannot open file: %w", err)
	}
	defer f.Close()

	ext := strings.ToLower(filepath.Ext(absPath))

	// If it's a text file, read as text
	if isTextFile(ext) {
		data, err := io.ReadAll(f)
		if err != nil {
			return nil, fmt.Errorf("cannot read file: %w", err)
		}
		return &FileContent{
			Name:     info.Name(),
			Path:     absPath,
			Content:  string(data),
			Size:     info.Size(),
			IsBinary: false,
		}, nil
	}

	// Known binary preview formats (.pdf, .docx, .xlsx, .pptx) – treat as binary
	if isPreviewFile(ext) {
		readSize := info.Size()
		if readSize > 10*1024*1024 {
			readSize = 10 * 1024 * 1024
		}
		data := make([]byte, readSize)
		_, err := io.ReadFull(f, data)
		if err != nil && err != io.ErrUnexpectedEOF {
			return nil, fmt.Errorf("cannot read binary file: %w", err)
		}
		return &FileContent{
			Name:     info.Name(),
			Path:     absPath,
			Content:  base64.StdEncoding.EncodeToString(data),
			Size:     info.Size(),
			IsBinary: true,
			Encoding: "base64",
		}, nil
	}

	// Try reading a small portion to detect if it's text
	header := make([]byte, 512)
	n, _ := f.Read(header)
	header = header[:n]

	// Simple binary detection: check for null bytes
	isBinary := false
	for _, b := range header {
		if b == 0 {
			isBinary = true
			break
		}
	}

	if !isBinary {
		// Read the whole file as text
		f.Seek(0, 0)
		data, err := io.ReadAll(f)
		if err != nil {
			return nil, fmt.Errorf("cannot read file: %w", err)
		}
		return &FileContent{
			Name:     info.Name(),
			Path:     absPath,
			Content:  string(data),
			Size:     info.Size(),
			IsBinary: false,
		}, nil
	}

	// Binary file: read and base64 encode (limit to 10MB for binary preview)
	readSize := info.Size()
	if readSize > 10*1024*1024 {
		readSize = 10 * 1024 * 1024
	}
	f.Seek(0, 0)
	data := make([]byte, readSize)
	_, err = io.ReadFull(f, data)
	if err != nil && err != io.ErrUnexpectedEOF {
		return nil, fmt.Errorf("cannot read binary file: %w", err)
	}

	return &FileContent{
		Name:     info.Name(),
		Path:     absPath,
		Content:  base64.StdEncoding.EncodeToString(data),
		Size:     info.Size(),
		IsBinary: true,
		Encoding: "base64",
	}, nil
}

// ReadDir reads the immediate children of a directory.
func (a *App) ReadDir(dirPath string) ([]FileNode, error) {
	wd := a.GetWorkingDir()
	if wd == "" {
		return nil, fmt.Errorf("working directory not set")
	}

	// Security check
	absPath, err := filepath.Abs(dirPath)
	if err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}
	wdAbs, _ := filepath.Abs(wd)
	if !strings.HasPrefix(absPath, wdAbs) {
		return nil, fmt.Errorf("access denied: directory is outside working directory")
	}

	entries, err := os.ReadDir(absPath)
	if err != nil {
		return nil, fmt.Errorf("cannot read directory: %w", err)
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})

	nodes := make([]FileNode, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if entry.IsDir() && shouldSkipDir(name) {
			continue
		}

		fullPath := filepath.Join(absPath, name)
		info, _ := entry.Info()
		size := int64(0)
		if info != nil {
			size = info.Size()
		}

		nodes = append(nodes, FileNode{
			Name:  name,
			Path:  fullPath,
			IsDir: entry.IsDir(),
			Size:  size,
		})
	}

	return nodes, nil
}

// -------------------- Auto-learn management --------------------

// SetAutoLearnEnabled enables or disables LLM-based auto-learning.
// When disabled, only explicit markers ([remember:key=value]) are processed.
// When enabled, the agent will also attempt LLM extraction from natural
// language patterns.
func (a *App) SetAutoLearnEnabled(enabled bool) {
	a.mu.Lock()
	a.autoLearnEnabled = enabled
	a.mu.Unlock()
	logutil.Infof("[autolearn] auto-learn %v", map[bool]string{true: "enabled", false: "disabled"}[enabled])
}

// GetAutoLearnEnabled returns whether auto-learning is currently enabled.
func (a *App) GetAutoLearnEnabled() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.autoLearnEnabled
}

// -------------------- Model discovery --------------------

// ModelInfo is a minimal model descriptor for the frontend.
type ModelInfo struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Reasoning        bool              `json:"reasoning"`
	ThinkingLevelMap map[string]string `json:"thinkingLevelMap,omitempty"`
}

// modelListing is the response shape shared by /v1/models endpoints.
// Both OpenAI and Anthropic use the same `{data: [{id, ...}]}` envelope.
type modelListing struct {
	Data []struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
	} `json:"data"`
}

// GetModels lists models for a given provider, optionally using a custom base URL + API key.
func (a *App) GetModels(params map[string]interface{}) ([]ModelInfo, error) {
	provider := core.KnownProvider(stringParam(params, "provider"))
	switch provider {
	case core.ProviderAnthropic:
		return a.fetchProviderModels(provider, stringParam(params, "baseUrl"), stringParam(params, "apiKey"))
	case core.ProviderOpenAI:
		return a.fetchProviderModels(provider, stringParam(params, "baseUrl"), stringParam(params, "apiKey"))
	case core.ProviderOllama:
		// Local Ollama: skip /models fetch (the server advertises tags
		// via /api/tags, not /v1/models) and let the caller fall through
		// to the static cached list. This keeps things working when the
		// daemon is offline.
		return a.cachedModels(provider), nil
	}
	return nil, fmt.Errorf("unsupported provider: %s", provider)
}

// fetchProviderModels hits the provider's /models endpoint. The two
// providers share the same JSON shape, so they share this implementation
// — the only real differences are the default base URL and the auth
// header (handled by attachAuth).
func (a *App) fetchProviderModels(provider core.KnownProvider, baseURL, apiKey string) ([]ModelInfo, error) {
	url := baseURL
	if url == "" {
		url = defaultBaseURL(provider)
	}
	url += "/models"

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return a.cachedModels(provider), nil
	}
	a.attachAuth(req, provider, apiKey)
	if provider == core.ProviderAnthropic {
		req.Header.Set("anthropic-version", "2023-06-01")
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			_ = resp.Body.Close()
		}
		return a.cachedModels(provider), nil
	}
	defer resp.Body.Close()

	var listing modelListing
	if err := json.NewDecoder(resp.Body).Decode(&listing); err != nil {
		return a.cachedModels(provider), nil
	}

	out := make([]ModelInfo, 0, len(listing.Data))
	for _, m := range listing.Data {
		name := m.DisplayName
		if name == "" {
			name = m.ID
		}
		info := ModelInfo{ID: m.ID, Name: name}
		// Enrich with reasoning / thinking level map from static model data
		if cm, err := ai.GetModel(provider, m.ID); err == nil {
			info.Reasoning = cm.Reasoning
			if cm.ThinkingLevelMap != nil {
				info.ThinkingLevelMap = cm.ThinkingLevelMap
			}
		}
		out = append(out, info)
	}
	return out, nil
}

func defaultBaseURL(provider core.KnownProvider) string {
	switch provider {
	case core.ProviderAnthropic:
		return "https://api.anthropic.com/v1"
	case core.ProviderOllama:
		// Ollama exposes its OpenAI-compat surface at /v1 on port 11434.
		// Users running Ollama on a remote host or behind a proxy should
		// override this via the OLLAMA_BASE_URL env var or the UI's
		// Base URL field.
		return "http://localhost:11434/v1"
	default:
		return "https://api.openai.com/v1"
	}
}

// attachAuth sets Authorization / x-api-key from the user-supplied key.
// It does NOT fall back to env vars — model listing should reflect
// exactly what the user typed. Chat sends its own key separately.
func (a *App) attachAuth(req *http.Request, provider core.KnownProvider, apiKey string) {
	if apiKey == "" {
		return
	}
	if provider == core.ProviderAnthropic {
		req.Header.Set("x-api-key", apiKey)
	} else {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	req.Header.Set("Content-Type", "application/json")
}

// cachedModels returns the statically-known model list for a provider.
// Used as the offline fallback when /v1/models fails.
func (a *App) cachedModels(provider core.KnownProvider) []ModelInfo {
	models := ai.GetModels(provider)
	out := make([]ModelInfo, 0, len(models))
	for _, m := range models {
		info := ModelInfo{ID: m.ID, Name: m.ID}
		info.Reasoning = m.Reasoning
		if m.ThinkingLevelMap != nil {
			info.ThinkingLevelMap = m.ThinkingLevelMap
		}
		out = append(out, info)
	}
	return out
}

// stringParam is a small helper for the untyped params map that Wails
// passes from the frontend. Returns "" for missing or wrong-typed keys.
func stringParam(params map[string]interface{}, key string) string {
	if v, ok := params[key].(string); ok {
		return v
	}
	return ""
}

// parseConversationHistory converts a JSON array of {role, content}
// objects (sent from the frontend) into core.Message instances that
// the agent can process. Assistant messages with empty content are
// skipped (they represent in-progress streaming placeholders).
func parseConversationHistory(jsonStr string) []core.Message {
	type toolCallData struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	}
	type histMsg struct {
		Role       string         `json:"role"`
		Content    string         `json:"content"`
		ToolCalls  []toolCallData `json:"toolCalls,omitempty"`
		ToolCallID string         `json:"toolCallId,omitempty"`
		ToolName   string         `json:"toolName,omitempty"`
		IsError    bool           `json:"isError,omitempty"`
	}
	var history []histMsg
	if err := json.Unmarshal([]byte(jsonStr), &history); err != nil {
		return nil
	}
	msgs := make([]core.Message, 0, len(history))
	for _, h := range history {
		switch h.Role {
		case "user":
			if h.Content == "" {
				continue
			}
			msgs = append(msgs, core.UserMessage{
				Role:      core.MessageRoleUser,
				Content:   h.Content,
				Timestamp: time.Now(),
			})
		case "assistant":
			var content []core.ContentBlock
			if h.Content != "" {
				content = append(content, core.TextContent{Type: "text", Text: h.Content})
			}
			for _, tc := range h.ToolCalls {
				content = append(content, core.ToolCall{
					Type:      "tool_use",
					ID:        tc.ID,
					Name:      tc.Name,
					Arguments: json.RawMessage(tc.Arguments),
				})
			}
			if len(content) == 0 {
				continue // skip empty placeholder
			}
			msgs = append(msgs, core.AssistantMessage{
				Role:    core.MessageRoleAssistant,
				Content: content,
			})
		case "tool":
			if h.Content == "" {
				continue
			}
			msgs = append(msgs, core.ToolResultMessage{
				Role:       core.MessageRoleTool,
				ToolCallID: h.ToolCallID,
				ToolName:   h.ToolName,
				Content:    []core.ContentBlock{core.TextContent{Type: "text", Text: h.Content}},
				IsError:    h.IsError,
			})
		}
	}
	return msgs
}

// -------------------- Chat --------------------

// ChatParams carries per-request configuration from the frontend.
type ChatParams struct {
	Message       string
	Provider      string
	APIKey        string
	BaseURL       string
	Model         string
	ThinkingLevel string
	Messages      string // JSON-encoded conversation history, optional
}

func parseChatParams(params map[string]interface{}) (ChatParams, error) {
	p := ChatParams{
		Message:       stringParam(params, "message"),
		Provider:      stringParam(params, "provider"),
		APIKey:        stringParam(params, "apiKey"),
		BaseURL:       stringParam(params, "baseUrl"),
		Model:         stringParam(params, "model"),
		ThinkingLevel: stringParam(params, "thinkingLevel"),
		Messages:      stringParam(params, "messages"),
	}
	if p.Message == "" {
		return p, fmt.Errorf("message is required")
	}
	return p, nil
}

// StreamMessage runs an agent turn and streams events to the frontend.
func (a *App) StreamMessage(params map[string]interface{}) error {
	p, err := parseChatParams(params)
	if err != nil {
		wruntime.EventsEmit(a.ctx, "stream-error", err.Error())
		return err
	}

	cwd := a.GetWorkingDir()

	model, err := a.resolveModel(p)
	if err != nil {
		wruntime.EventsEmit(a.ctx, "stream-error", err.Error())
		return err
	}

	agt := a.getOrCreateAgent(model, cwd, p.APIKey, p.ThinkingLevel)

	// If conversation history is provided, reset and restore the full
	// conversation context so the LLM sees the complete message thread.
	if p.Messages != "" {
		agt.Reset()
		msgs := parseConversationHistory(p.Messages)
		if len(msgs) > 0 {
			agt.SetMessages(msgs)
		}
	}

	runCtx, cancel := context.WithCancel(a.ctx)
	a.mu.Lock()
	a.cancelFn = cancel
	a.mu.Unlock()

	// Capture the agent's message count before this turn so only the newly
	// added messages are persisted to the session (avoids duplicates).
	baseline := len(agt.Messages())

	go func() {
		defer func() {
			a.mu.Lock()
			a.cancelFn = nil
			a.mu.Unlock()
			// Always emit stream-done when the agent run finishes, regardless
			// of how it ended (context cancellation via CancelStream, error,
			// or natural completion).
			wruntime.EventsEmit(a.ctx, "stream-done", "")
		}()

		// If we have valid memory and autolearn enabled, attempt to extract facts
		// from the user input before the agent processes it.
		a.mu.RLock()
		learner := a.learner
		autoLearn := a.autoLearnEnabled
		a.mu.RUnlock()
		if learner != nil && autoLearn {
			if n := learner.ProcessUserInput(p.Message); n > 0 {
				logutil.Infof("[autolearn] extracted %d memories from user input", n)
			}
		}

		_, _ = agt.Run(runCtx, core.UserMessage{
			Role:      core.MessageRoleUser,
			Content:   p.Message,
			Timestamp: time.Now(),
		})

		// Persist this turn's new messages to the persistent session so the
		// conversation can be recovered after restart.
		a.persistTurnDelta(agt.Messages(), baseline)

		// Persist memory after each turn
		a.mu.RLock()
		mem := a.mem
		a.mu.RUnlock()
		if mem != nil {
			_ = mem.Save()
		}
	}()
	return nil
}

// ResetAgent clears the agent's message history. Call this when
// switching to a different conversation so old context does not
// leak into the new one.
func (a *App) ResetAgent() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.agt != nil {
		a.agt.Reset()
	}
}

// CancelStream cancels an in-progress StreamMessage call.
func (a *App) CancelStream() {
	a.mu.Lock()
	if a.cancelFn != nil {
		a.cancelFn()
	}
	a.mu.Unlock()
}

// resolveModel picks the model from the user-supplied list, or falls back
// to the first known model for the provider. The custom-model path lets
// users type any ID even if it isn't in the cached list.
func (a *App) resolveModel(p ChatParams) (core.Model, error) {
	provider := core.KnownProvider(p.Provider)
	switch provider {
	case core.ProviderAnthropic, core.ProviderOpenAI, core.ProviderOllama:
		// ok
	default:
		return core.Model{}, fmt.Errorf("unsupported provider: %s", p.Provider)
	}

	api := core.APIOpenAICompletions
	if provider == core.ProviderAnthropic {
		api = core.APIAnthropicMessages
	}

	if p.Model != "" {
		if m, err := ai.GetModel(provider, p.Model); err == nil {
			if p.BaseURL != "" {
				m.BaseURL = p.BaseURL
			}
			return m, nil
		}
		// Unknown ID: still allow it, with a conservative default window.
		return core.Model{
			ID:            p.Model,
			Provider:      provider,
			API:           api,
			BaseURL:       p.BaseURL,
			ContextWindow: 8192,
		}, nil
	}

	models := ai.GetModels(provider)
	if len(models) > 0 {
		m := models[0]
		if p.BaseURL != "" {
			m.BaseURL = p.BaseURL
		}
		return m, nil
	}
	return core.Model{}, fmt.Errorf("no model available for provider %s", p.Provider)
}

// buildSystemPromptWithMemory renders the system prompt including memory and skills.
func (a *App) buildSystemPrompt(cwd string) string {
	prompt := fmt.Sprintf(`You are Crux Agent, an AI coding assistant running inside the user's local workspace.

Working directory: %s

You have access to the following tools to inspect and modify files inside the working directory:
- read_file: read the contents of a file
- write_file: create or overwrite a file
- bash: run a shell command in the working directory (Windows: cmd.exe, Unix: sh)
- glob: list files matching a glob pattern
- grep: search for a regex across files`, cwd)

	// Append long-term memory
	a.mu.RLock()
	mem := a.mem
	a.mu.RUnlock()
	if mem != nil {
		if memContent := mem.FormatForPrompt(); memContent != "" {
			prompt += "\n\n" + memContent
		}
	}

	// Append available skills
	a.mu.RLock()
	sl := a.skillLoader
	a.mu.RUnlock()
	if sl != nil && sl.Count() > 0 {
		prompt += "\n\nAvailable skills (use skill_<name> tool to get instructions):\n"
		for _, name := range sl.List() {
			prompt += "- " + name + "\n"
		}
	}

	prompt += "\n\nWhen the user asks you to do something with files or code, prefer using the tools rather than guessing. After making changes, briefly summarize what you did."

	return prompt
}

// -------------------- crux-plugin wiring --------------------

// initPlugins discovers and starts subprocess JSON-RPC plugins from the
// conventional plugin directories. Tool-capable plugins expose tools that
// are merged into the agent's tool set on each turn (see buildAllTools).
//
// Directories are resolved relative to the executable so the app works
// when the working directory is set to a project folder. Missing
// directories are skipped silently by Manager.Discover.
func (a *App) initPlugins(ctx context.Context) {
	pluginDirs := []string{
		filepath.Join(executableDir(), "plugins"),
	}
	if home, err := os.UserHomeDir(); err == nil {
		pluginDirs = append(pluginDirs, filepath.Join(home, ".crux", "plugins"))
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelWarn,
	}))
	mgr := plugin.NewManager(logger)

	if err := mgr.Discover(pluginDirs); err != nil {
		logutil.Warnf("[plugin] discover failed: %v", err)
	}

	pctx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	a.pluginMgr = mgr
	a.pluginCtx = pctx
	a.pluginCnl = cancel
	a.mu.Unlock()

	if err := mgr.StartAll(pctx); err != nil {
		logutil.Warnf("[plugin] start failed: %v", err)
	}

	if n := len(mgr.Plugins()); n > 0 {
		logutil.Infof("[plugin] %d plugin(s) discovered", n)
	} else {
		logutil.Infof("[plugin] no plugins discovered (looked in %v)", pluginDirs)
	}
}

// executableDir returns the directory of the running executable.
func executableDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

// adaptPluginTools converts a batch of crux-plugin ToolAdapters into
// agent-engine tools. Each adapter represents a tool that runs in a
// subprocess via JSON-RPC (e.g. `<pluginID>.<toolName>`).
func adaptPluginTools(adapters []plugin.ToolAdapter) []engine.AgentTool {
	out := make([]engine.AgentTool, 0, len(adapters))
	for _, a := range adapters {
		out = append(out, toEngineTool(adaptToEngineToolPlugin(a)))
	}
	return out
}

// adaptToEngineToolPlugin adapts a crux-plugin ToolAdapter into the
// agent-engine ToolPlugin contract (in-process wrapper around the
// subprocess Execute closure). See agent-engine/examples/crux-plugin-tool.
func adaptToEngineToolPlugin(a plugin.ToolAdapter) engineToolPlugin {
	params, _ := json.Marshal(a.Parameters)
	return &adapterToolPlugin{
		name:        a.Name,
		description: a.Description,
		parameters:  params,
		execute:     a.Execute,
	}
}

// toEngineTool converts a ToolPlugin into an engine.AgentTool.
func toEngineTool(tp engineToolPlugin) engine.AgentTool {
	return engine.AgentTool{
		Name:        tp.Name(),
		Description: tp.Description(),
		Parameters:  json.RawMessage(tp.Parameters()),
		Execute: func(ctx context.Context, id string, args json.RawMessage, onUpdate func(json.RawMessage)) (engine.AgentToolResult, error) {
			bridge := func(b []byte) { onUpdate(json.RawMessage(b)) }
			r, err := tp.Execute(ctx, id, args, bridge)
			if err != nil {
				return engine.AgentToolResult{
					Content: []core.ContentBlock{core.TextContent{Type: "text", Text: err.Error()}},
					IsError: true,
				}, nil
			}
			return engine.AgentToolResult{
				Content:   r.Content,
				Details:   r.Details,
				IsError:   r.IsError,
				Terminate: r.Terminate,
			}, nil
		},
	}
}

// engineToolPlugin is the minimal crux-plugin tool contract used by the
// in-process adapter. It mirrors agent-engine/plugin.ToolPlugin so we don't
// need to depend on that package here; only the pieces needed by the
// chat app's agent are kept.
type engineToolPlugin interface {
	Name() string
	Description() string
	Parameters() []byte
	Execute(ctx context.Context, toolCallID string, params []byte, onUpdate func([]byte)) (pluginToolResult, error)
}

// pluginToolResult is the in-process result of a plugin tool execution.
type pluginToolResult struct {
	Content   []core.ContentBlock
	Details   json.RawMessage
	IsError   bool
	Terminate bool
}

// adapterToolPlugin wraps a crux-plugin ToolAdapter into the engineToolPlugin
// contract. ToolAdapter.Execute returns a result string which becomes the
// TextContent returned to the LLM.
type adapterToolPlugin struct {
	name        string
	description string
	parameters  []byte
	execute     func(ctx context.Context, args json.RawMessage) (string, error)
}

func (t *adapterToolPlugin) Name() string        { return t.name }
func (t *adapterToolPlugin) Description() string { return t.description }
func (t *adapterToolPlugin) Parameters() []byte  { return t.parameters }

func (t *adapterToolPlugin) Execute(ctx context.Context, _ string, params []byte, _ func([]byte)) (pluginToolResult, error) {
	out, err := t.execute(ctx, params)
	if err != nil {
		return pluginToolResult{
			Content: []core.ContentBlock{core.TextContent{Type: "text", Text: err.Error()}},
			IsError: true,
		}, nil
	}
	return pluginToolResult{
		Content: []core.ContentBlock{core.TextContent{Type: "text", Text: out}},
	}, nil
}

// -------------------- Agent wiring --------------------

func (a *App) getOrCreateAgent(model core.Model, cwd, apiKey string, thinkingLevel ...string) *engine.Agent {
	if a.agt != nil {
		// Agent already exists — update its config for this turn.
		a.agt.SetModel(model)
		a.agt.SetTools(a.buildAllTools(cwd))
		a.agt.SetSystemPrompt(a.buildSystemPrompt(cwd))
		s := a.agt.State()
		s.SimpleStreamOptions.APIKey = apiKey
		if len(thinkingLevel) > 0 && thinkingLevel[0] != "" {
			s.SimpleStreamOptions.Reasoning = core.ThinkingLevel(thinkingLevel[0])
		}
		a.agt.SetSimpleStreamOptions(s.SimpleStreamOptions)
		return a.agt
	}

	toolsAll := a.buildAllTools(cwd)

	compaction := buildCompactionConfig(model, apiKey)

	// Restore previously-persisted conversation history from the session so
	// the conversation survives an app restart.
	restored := a.sessionMessages()

	agt := engine.New(engine.AgentOptions{
		InitialState: &engine.AgentState{
			Model:         model,
			SystemPrompt:  a.buildSystemPrompt(cwd),
			Tools:         toolsAll,
			Messages:      restored,
			ToolExecution: engine.ToolExecSequential,
			SimpleStreamOptions: core.SimpleStreamOptions{
				StreamOptions: core.StreamOptions{
					APIKey: apiKey,
				},
			},
			GetApiKey: func() string { return apiKey },
		},
		Compaction: compaction,
	})

	agt.Subscribe(func(evt engine.AgentEvent) {
		a.forwardAgentEvent(evt)
	})

	a.agt = agt

	// Initialize autolearn after agent is created
	a.initAutolearn(model, apiKey)

	return agt
}

// initAutolearn initializes the auto-learning system if memory is available.
func (a *App) initAutolearn(model core.Model, apiKey string) {
	a.mu.RLock()
	if a.learner != nil {
		a.mu.RUnlock()
		return // Already initialized
	}
	mem := a.mem
	a.mu.RUnlock()

	if mem == nil || apiKey == "" {
		return
	}

	// Create synchronous summarizer for autolearn
	signalSummarize := newSyncSummarizer(model, apiKey, 20*time.Second, "")
	wfSummarize := newSyncSummarizer(model, apiKey, 60*time.Second, "")

	wfDir := filepath.Join(a.workingDir, "skills", "auto-extracted")
	_ = os.MkdirAll(wfDir, 0755)

	learner := defaults.NewAutoLearner(mem, 5) // extract every 5 turns (matches runtime DefaultSettings.ExtractEveryN)
	learner.SetSignalExtractor(&defaults.LLMSignalExtractor{SummarizeFunc: signalSummarize})

	wfExtractor := &defaults.WorkflowExtractor{SummarizeFunc: wfSummarize}

	a.mu.Lock()
	a.learner = learner
	a.wfDir = wfDir
	a.wfExtractor = wfExtractor
	a.mu.Unlock()

	logutil.Infof("[autolearn] initialized with model %s", model.ID)
}

// buildAllTools builds the full tool set including built-in tools,
// crux-plugin subprocess tools, skill tools, and memory tools.
func (a *App) buildAllTools(cwd string) []engine.AgentTool {
	var allTools []engine.AgentTool

	// Built-in tools (chat-app/tools), already engine.AgentTool, wrapped
	// with the working dir so relative paths and shell commands resolve
	// inside cwd instead of the process's working dir.
	for _, t := range tools.All() {
		allTools = append(allTools, wrapWithWorkingDir(t, cwd))
	}

	// crux-plugin subprocess tools (ToolAdapter → engine.AgentTool).
	a.mu.RLock()
	mgr := a.pluginMgr
	a.mu.RUnlock()
	if mgr != nil {
		if adapters, err := mgr.RegisterPluginTools(a.ctx); err == nil {
			allTools = append(allTools, adaptPluginTools(adapters)...)
		} else {
			logutil.Warnf("[plugin] register tools failed: %v", err)
		}
	}

	// Skill tools
	a.mu.RLock()
	sl := a.skillLoader
	a.mu.RUnlock()
	if sl != nil && sl.Count() > 0 {
		skillTools := sl.AsAgentTools()
		allTools = append(allTools, skillTools...)
	}

	// Memory tool (if memory is available)
	a.mu.RLock()
	mem := a.mem
	a.mu.RUnlock()
	if mem != nil {
		allTools = append(allTools, a.rememberTool(mem))
		allTools = append(allTools, a.recallTool(mem))
	}

	return allTools
}

// rememberTool returns a tool that stores a key=value pair into long-term memory.
func (a *App) rememberTool(mem *defaults.Memory) engine.AgentTool {
	return engine.AgentTool{
		Name:        "remember",
		Description: "Store a key-value pair into long-term memory. Use this when the user asks you to remember something or when you learn a fact about the user (their name, preferences, project details) that will be useful in future conversations.",
		Parameters:  mustRawMessage(`{"type":"object","properties":{"key":{"type":"string","description":"Memory key, e.g. user.name or project.tech_stack"},"value":{"type":"string","description":"Value to store"}},"required":["key","value"]}`),
		Execute: func(_ context.Context, _ string, params json.RawMessage, _ func(json.RawMessage)) (engine.AgentToolResult, error) {
			var args struct {
				Key   string `json:"key"`
				Value string `json:"value"`
			}
			if err := json.Unmarshal(params, &args); err != nil {
				return engine.AgentToolResult{
					Content: []core.ContentBlock{core.TextContent{Type: "text", Text: "Error: invalid arguments - " + err.Error()}},
					IsError: true,
				}, nil
			}
			if args.Key == "" {
				return engine.AgentToolResult{
					Content: []core.ContentBlock{core.TextContent{Type: "text", Text: "Error: key is required"}},
					IsError: true,
				}, nil
			}
			mem.Set(args.Key, args.Value)
			_ = mem.Save()
			logutil.Infof("[memory] set %s=%s", args.Key, args.Value)
			return engine.AgentToolResult{
				Content: []core.ContentBlock{core.TextContent{Type: "text", Text: fmt.Sprintf("Remembered: %s = %s", args.Key, args.Value)}},
			}, nil
		},
	}
}

// recallTool returns a tool that retrieves a value from long-term memory.
func (a *App) recallTool(mem *defaults.Memory) engine.AgentTool {
	return engine.AgentTool{
		Name:        "recall",
		Description: "Retrieve a value from long-term memory by key. Use this when you need to recall information about the user or project that was previously stored.",
		Parameters:  mustRawMessage(`{"type":"object","properties":{"key":{"type":"string","description":"Memory key to look up, e.g. user.name or project.tech_stack"}},"required":["key"]}`),
		Execute: func(_ context.Context, _ string, params json.RawMessage, _ func(json.RawMessage)) (engine.AgentToolResult, error) {
			var args struct {
				Key string `json:"key"`
			}
			if err := json.Unmarshal(params, &args); err != nil {
				return engine.AgentToolResult{
					Content: []core.ContentBlock{core.TextContent{Type: "text", Text: "Error: invalid arguments - " + err.Error()}},
					IsError: true,
				}, nil
			}
			if args.Key == "" {
				return engine.AgentToolResult{
					Content: []core.ContentBlock{core.TextContent{Type: "text", Text: "Error: key is required"}},
					IsError: true,
				}, nil
			}
			if value, ok := mem.Get(args.Key); ok {
				return engine.AgentToolResult{
					Content: []core.ContentBlock{core.TextContent{Type: "text", Text: fmt.Sprintf("Memory: %s = %s", args.Key, value)}},
				}, nil
			}
			return engine.AgentToolResult{
				Content: []core.ContentBlock{core.TextContent{Type: "text", Text: fmt.Sprintf("No memory found for key: %s", args.Key)}},
			}, nil
		},
	}
}

// mustRawMessage returns a json.RawMessage for the given literal JSON.
func mustRawMessage(s string) json.RawMessage {
	if !json.Valid([]byte(s)) {
		panic(fmt.Sprintf("app: invalid JSON: %s", s))
	}
	return json.RawMessage(s)
}

// wrapWithWorkingDir rewrites file/shell tools so relative paths and shell
// commands resolve inside cwd instead of the process's working dir.
func wrapWithWorkingDir(t engine.AgentTool, cwd string) engine.AgentTool {
	if cwd == "" {
		return t
	}
	inner := t.Execute
	t.Execute = func(ctx context.Context, toolCallID string, params json.RawMessage, onUpdate func(json.RawMessage)) (engine.AgentToolResult, error) {
		if rewritten, ok := rewriteToolParams(t.Name, params, cwd); ok {
			params = rewritten
		}
		return inner(ctx, toolCallID, params, onUpdate)
	}
	return t
}

// rewriteToolParams rewrites a tool's parameters so they target cwd.
// Returns (rewritten, true) on success, (nil, false) if the tool's args
// aren't recognized or no rewrite is needed.
func rewriteToolParams(name string, params json.RawMessage, cwd string) (json.RawMessage, bool) {
	switch name {
	case "bash":
		var args struct {
			Command string `json:"command"`
			Shell   string `json:"shell"`
		}
		if err := json.Unmarshal(params, &args); err != nil || args.Command == "" {
			return nil, false
		}
		args.Command = injectCwd(args.Command, cwd, args.Shell)
		return jsonMarshal(args)
	case "read_file", "write_file":
		var args struct {
			FilePath string `json:"filePath"`
		}
		if err := json.Unmarshal(params, &args); err != nil || args.FilePath == "" || filepath.IsAbs(args.FilePath) {
			return nil, false
		}
		args.FilePath = filepath.Join(cwd, args.FilePath)
		return jsonMarshal(args)
	case "glob", "grep":
		var args struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(params, &args); err != nil {
			return nil, false
		}
		if args.Path == "" {
			args.Path = cwd
		} else if !filepath.IsAbs(args.Path) {
			args.Path = filepath.Join(cwd, args.Path)
		} else {
			return nil, false
		}
		return jsonMarshal(args)
	}
	return nil, false
}

func jsonMarshal(v interface{}) (json.RawMessage, bool) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	return b, true
}

// injectCwd prefixes the shell command with a "cd into cwd" step so
// relative paths resolve there. Uses platform-native shell semantics.
//
// The separator and the cd syntax differ per shell:
//   - PowerShell (default on Windows): '&' is a reserved call operator, so it
//     cannot be used as a command separator on PowerShell 5.1+. We use ';' and
//     Set-Location instead.
//   - cmd.exe:                       uses cd /d and '&' as the separator.
//   - bash/sh:                       uses cd and '&&' as the separator.
func injectCwd(cmd, cwd, shellOverride string) string {
	shell := strings.ToLower(strings.TrimSpace(shellOverride))
	if shell == "" {
		shell = strings.ToLower(strings.TrimSpace(os.Getenv("CRUX_SHELL")))
	}

	switch stdruntime.GOOS {
	case "windows":
		switch shell {
		case "cmd", "cmd.exe":
			return fmt.Sprintf(`cd /d "%s" & %s`, cwd, cmd)
		case "bash", "sh":
			return fmt.Sprintf(`cd "%s" && %s`, cwd, cmd)
		default:
			// Default Windows shell is PowerShell (pwsh or powershell.exe).
			// Use ; as the separator and Set-Location for the cd step.
			return fmt.Sprintf(`Set-Location -LiteralPath "%s"; %s`, cwd, cmd)
		}
	default:
		return fmt.Sprintf(`cd "%s" && %s`, cwd, cmd)
	}
}

// buildCompactionConfig wires up automatic context-window compaction.
// Strategy:
//  1. Pre-call: when estimated tokens exceed MaxTokens (60k), run chained
//     compactor: LLM summarize (lossy) → slide window (cheap).
//  2. Overflow retry: after a context-overflow error, force-compact and retry
//     up to OverflowRetries times.
func buildCompactionConfig(model core.Model, apiKey string) engine.CompactionConfig {
	summarizer := defaults.NewLLMSummarize()
	summarizer.KeepLast = 8
	summarizer.MinTrigger = 20
	summarizer.Summarize = buildSummarizeFunc(model, apiKey)

	return engine.CompactionConfig{
		// agent-engine uses a plain function signature for compaction;
		// defaults.NewChainedCompactorFunc wraps the LLM-summarize + slide
		// window strategies to satisfy it.
		Compactor: defaults.NewChainedCompactorFunc(summarizer, defaults.NewSlideWindow(40)),
		MaxTokens:       60000,
		ReserveTokens:   4096,
		OverflowRetries: 2,
		OnCompact: func(prevTokens, newTokens, prevMsgs, newMsgs int) {
			logutil.Debugf("[compaction] %d tokens, %d msgs → %d tokens, %d msgs",
				prevTokens, prevMsgs, newTokens, newMsgs)
		},
	}
}

// forwardAgentEvent converts agent events into Wails runtime events so
// the React frontend can render streaming output.
func (a *App) forwardAgentEvent(evt engine.AgentEvent) {
	switch e := evt.(type) {
	case engine.EventAgentStart:
		wruntime.EventsEmit(a.ctx, "stream-agent-start", "")
		logutil.Debugf("[agent] started")
	case engine.EventAgentEnd:
		wruntime.EventsEmit(a.ctx, "stream-done", "")
		logutil.Debugf("[agent] ended")
	case engine.EventTurnStart:
		logutil.Debugf("[agent] turn start")
	case engine.EventTurnEnd:
		logutil.Debugf("[agent] turn end")
	case engine.EventMessageUpdate:
		a.forwardAssistantEvent(e.AssistantEvent)
	case engine.EventToolExecStart:
		data, _ := json.Marshal(map[string]string{"id": e.ToolCallID, "name": e.ToolName})
		wruntime.EventsEmit(a.ctx, "stream-tool-exec-start", string(data))
		logutil.Debugf("[tool] executing %s (%s)", e.ToolName, string(e.ToolCallID))
	case engine.EventToolExecEnd:
		text := string(e.Result)
		event := "stream-tool-exec-end"
		if e.IsError {
			event = "stream-tool-exec-error"
			logutil.Warnf("[tool] %s error: %s", e.ToolName, text[:min(len(text), 500)])
		} else {
			logutil.Debugf("[tool] %s completed (%d bytes)", e.ToolName, len(text))
		}
		wruntime.EventsEmit(a.ctx, event, text)
	}
}

// forwardAssistantEvent emits the right Wails event for each assistant
// event variant (text / thinking delta, tool call lifecycle).
func (a *App) forwardAssistantEvent(evt core.AssistantMessageEvent) {
	switch ae := evt.(type) {
	case core.EventTextDelta:
		wruntime.EventsEmit(a.ctx, "stream-text-delta", ae.Delta)
	case core.EventThinkingDelta:
		wruntime.EventsEmit(a.ctx, "stream-thinking-delta", ae.Delta)
	case core.EventToolCallStart:
		data, _ := json.Marshal(map[string]string{"id": ae.ID, "name": ae.Name})
		wruntime.EventsEmit(a.ctx, "stream-tool-call-start", string(data))
		logutil.Debugf("[toolcall] started %s (%s)", ae.Name, ae.ID)
	case core.EventToolCallDelta:
		wruntime.EventsEmit(a.ctx, "stream-tool-call-delta", ae.ArgumentsDelta)
	case core.EventToolCallEnd:
		wruntime.EventsEmit(a.ctx, "stream-tool-call-end", string(ae.Arguments))
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
