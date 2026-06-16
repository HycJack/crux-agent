package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAll(t *testing.T) {
	allTools := All()
	if len(allTools) != 5 {
		t.Errorf("expected 5 tools, got %d", len(allTools))
	}

	// Verify tool names
	names := make(map[string]bool)
	for _, tool := range allTools {
		names[tool.Name] = true
	}

	expected := []string{"read_file", "write_file", "bash", "glob", "grep"}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("missing tool: %s", name)
		}
	}
}

func TestRead_Basic(t *testing.T) {
	// Create temp file
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(tmpFile, []byte("hello world"), 0644)

	tool := Read()
	params, _ := json.Marshal(map[string]any{
		"filePath": tmpFile,
	})

	result, err := tool.Execute(context.Background(), "test", params, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %v", result.Content)
	}
}

func TestRead_WithOffsetLimit(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.txt")
	content := "line1\nline2\nline3\nline4\nline5"
	os.WriteFile(tmpFile, []byte(content), 0644)

	tool := Read()
	params, _ := json.Marshal(map[string]any{
		"filePath": tmpFile,
		"offset":   1,
		"limit":    2,
	})

	result, err := tool.Execute(context.Background(), "test", params, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %v", result.Content)
	}
}

func TestRead_NotFound(t *testing.T) {
	tool := Read()
	params, _ := json.Marshal(map[string]any{
		"filePath": "/nonexistent/file.txt",
	})

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
	params, _ := json.Marshal(map[string]any{
		"filePath": tmpFile,
		"content":  "hello world",
	})

	result, err := tool.Execute(context.Background(), "test", params, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %v", result.Content)
	}

	// Verify file was written
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
	params, _ := json.Marshal(map[string]any{
		"filePath": tmpFile,
		"content":  "line2\n",
		"append":   true,
	})

	result, err := tool.Execute(context.Background(), "test", params, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %v", result.Content)
	}

	data, _ := os.ReadFile(tmpFile)
	if string(data) != "line1\nline2\n" {
		t.Errorf("unexpected content: %q", string(data))
	}
}

func TestBash_Basic(t *testing.T) {
	tool := Bash()
	params, _ := json.Marshal(map[string]any{
		"command": "echo hello",
	})

	result, err := tool.Execute(context.Background(), "test", params, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %v", result.Content)
	}
}

func TestBash_Error(t *testing.T) {
	tool := Bash()
	params, _ := json.Marshal(map[string]any{
		"command": "exit 1",
	})

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
	params, _ := json.Marshal(map[string]any{
		"pattern": "*.txt",
		"path":    tmpDir,
	})

	result, err := tool.Execute(context.Background(), "test", params, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %v", result.Content)
	}
}

func TestGrep_Basic(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte("hello world\nfoo bar\nhello again"), 0644)

	tool := Grep()
	params, _ := json.Marshal(map[string]any{
		"pattern": "hello",
		"path":    tmpDir,
	})

	result, err := tool.Execute(context.Background(), "test", params, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %v", result.Content)
	}
}

func TestGrep_Regex(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "test.go"), []byte("func main() {}\nvar x = 1"), 0644)

	tool := Grep()
	params, _ := json.Marshal(map[string]any{
		"pattern": "^func ",
		"path":    tmpDir,
		"regex":   true,
		"include": "*.go",
	})

	result, err := tool.Execute(context.Background(), "test", params, nil)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %v", result.Content)
	}
}
