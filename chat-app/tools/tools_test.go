package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	core "github.com/hycjack/crux-ai/core"
)

// resultText extracts the concatenated text of all text content blocks.
func resultText(blocks []core.ContentBlock) string {
	var sb strings.Builder
	for _, b := range blocks {
		if tb, ok := b.(core.TextContent); ok {
			sb.WriteString(tb.Text)
		}
	}
	return sb.String()
}

func TestAll(t *testing.T) {
	allTools := All()
	if len(allTools) != 6 {
		t.Fatalf("expected 6 tools, got %d", len(allTools))
	}

	names := make(map[string]bool)
	for _, tool := range allTools {
		if tool.Name == "" {
			t.Error("tool with empty name")
		}
		if tool.Description == "" {
			t.Errorf("tool %s has empty description", tool.Name)
		}
		if tool.Parameters == nil {
			t.Errorf("tool %s has nil parameters", tool.Name)
		}
		if tool.Execute == nil {
			t.Errorf("tool %s has nil Execute", tool.Name)
		}
		names[tool.Name] = true
	}

	expected := []string{"read_file", "write_file", "bash", "glob", "grep", "web_fetch"}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("missing tool: %s", name)
		}
	}
}

func TestRead_Basic(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(tmpFile, []byte("hello world"), 0644)

	tool := Read()
	params, _ := json.Marshal(map[string]any{"filePath": tmpFile})
	result, err := tool.Execute(context.Background(), "test", params, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
	if len(result.Content) == 0 {
		t.Error("expected content")
	}
}

func TestRead_WithOffsetLimit(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	content := "line1\nline2\nline3\nline4\nline5"
	os.WriteFile(tmpFile, []byte(content), 0644)

	tool := Read()
	params, _ := json.Marshal(map[string]any{"filePath": tmpFile, "offset": 1, "limit": 2})
	result, err := tool.Execute(context.Background(), "test", params, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
}

func TestRead_NotFound(t *testing.T) {
	tool := Read()
	params, _ := json.Marshal(map[string]any{"filePath": "/nonexistent/file.txt"})
	result, err := tool.Execute(context.Background(), "test", params, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for nonexistent file")
	}
}

func TestWrite_Basic(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")

	tool := Write()
	params, _ := json.Marshal(map[string]any{"filePath": tmpFile, "content": "hello world"})
	result, err := tool.Execute(context.Background(), "test", params, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("expected 'hello world', got %q", string(data))
	}
}

func TestWrite_Append(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(tmpFile, []byte("line1\n"), 0644)

	tool := Write()
	params, _ := json.Marshal(map[string]any{"filePath": tmpFile, "content": "line2\n", "append": true})
	result, err := tool.Execute(context.Background(), "test", params, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
	data, _ := os.ReadFile(tmpFile)
	if string(data) != "line1\nline2\n" {
		t.Errorf("unexpected content: %q", string(data))
	}
}

func TestBash_Basic(t *testing.T) {
	tool := Bash()
	params, _ := json.Marshal(map[string]any{"command": "echo hello"})
	result, err := tool.Execute(context.Background(), "test", params, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
}

func TestBash_Error(t *testing.T) {
	tool := Bash()
	params, _ := json.Marshal(map[string]any{"command": "exit 1"})
	result, err := tool.Execute(context.Background(), "test", params, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for non-zero exit code")
	}
}

func TestGlob_Basic(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "test1.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "test2.txt"), []byte("b"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "test.go"), []byte("c"), 0644)

	tool := Glob()
	params, _ := json.Marshal(map[string]any{"pattern": "*.txt", "path": tmpDir})
	result, err := tool.Execute(context.Background(), "test", params, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
}

func TestGrep_Basic(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte("hello world\nfoo bar\nhello again"), 0644)

	tool := Grep()
	params, _ := json.Marshal(map[string]any{"pattern": "hello", "path": tmpDir})
	result, err := tool.Execute(context.Background(), "test", params, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
}

func TestGrep_Regex(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "test.go"), []byte("func main() {}\nvar x = 1"), 0644)

	tool := Grep()
	params, _ := json.Marshal(map[string]any{"pattern": "^func ", "path": tmpDir, "regex": true, "include": "*.go"})
	result, err := tool.Execute(context.Background(), "test", params, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
}

func TestWebFetch_Basic(t *testing.T) {
	html := `<html><head><style>body{color:red}</style><script>alert(1)</script></head>
<body><h1>Hello World</h1><p>Foo &amp; Bar</p></body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(html))
	}))
	defer srv.Close()

	tool := WebFetch()
	params, _ := json.Marshal(map[string]any{"url": srv.URL})
	result, err := tool.Execute(context.Background(), "test", params, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
	text := resultText(result.Content)
	if !strings.Contains(text, "Hello World") {
		t.Errorf("expected 'Hello World' in stripped text, got %q", text)
	}
	if strings.Contains(text, "alert(1)") {
		t.Errorf("script content should be stripped, got %q", text)
	}
	if strings.Contains(text, "&amp;") {
		t.Errorf("HTML entities should be decoded, got %q", text)
	}
}

func TestWebFetch_MaxLength(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<p>" + strings.Repeat("x", 500) + "</p>"))
	}))
	defer srv.Close()

	tool := WebFetch()
	params, _ := json.Marshal(map[string]any{"url": srv.URL, "max_length": 100})
	result, err := tool.Execute(context.Background(), "test", params, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}
	text := resultText(result.Content)
	if len(text) > 130 {
		t.Errorf("expected truncation near max_length, got length %d", len(text))
	}
	if !strings.Contains(text, "[...truncated]") {
		t.Errorf("expected truncation marker, got %q", text)
	}
}

func TestWebFetch_BadURL(t *testing.T) {
	tool := WebFetch()
	params, _ := json.Marshal(map[string]any{"url": "http://127.0.0.1:1/none"})
	result, err := tool.Execute(context.Background(), "test", params, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for unreachable URL")
	}
}

func TestWebFetch_MissingURL(t *testing.T) {
	tool := WebFetch()
	params, _ := json.Marshal(map[string]any{})
	result, err := tool.Execute(context.Background(), "test", params, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for missing url")
	}
}
