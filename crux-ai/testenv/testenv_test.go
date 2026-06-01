package testenv

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestLoadEnvFromFile 测试从文件加载
func TestLoadEnvFromFile(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")
	content := `# Test env
KEY1=value1
KEY2="value with spaces"
KEY3='single quoted'
KEY4=unquoted value
`
	if err := os.WriteFile(envPath, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Change to temp dir
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(tmpDir)

	// Reset sync.Once for test
	loadOnce = sync.Once{}
	LoadEnv()

	if os.Getenv("KEY1") != "value1" {
		t.Errorf("KEY1 = %q, want %q", os.Getenv("KEY1"), "value1")
	}
	if os.Getenv("KEY2") != "value with spaces" {
		t.Errorf("KEY2 = %q, want %q", os.Getenv("KEY2"), "value with spaces")
	}
	if os.Getenv("KEY3") != "single quoted" {
		t.Errorf("KEY3 = %q, want %q", os.Getenv("KEY3"), "single quoted")
	}
	if os.Getenv("KEY4") != "unquoted value" {
		t.Errorf("KEY4 = %q, want %q", os.Getenv("KEY4"), "unquoted value")
	}
}

// TestLoadEnvFileNotFound 测试文件不存在
func TestLoadEnvFileNotFound(t *testing.T) {
	tmpDir := t.TempDir()

	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(tmpDir)

	loadOnce = sync.Once{}
	// Should not panic
	LoadEnv()
}

// TestGetEnvWithDefault 测试默认值
func TestGetEnvWithDefault(t *testing.T) {
	os.Unsetenv("UNSET_VAR_FOR_TEST")
	got := GetEnv("UNSET_VAR_FOR_TEST", "default")
	if got != "default" {
		t.Errorf("Expected 'default', got: %s", got)
	}
}

// TestGetEnvWithValue 测试已设置值
func TestGetEnvWithValue(t *testing.T) {
	os.Setenv("SET_VAR_FOR_TEST", "real-value")
	got := GetEnv("SET_VAR_FOR_TEST", "default")
	if got != "real-value" {
		t.Errorf("Expected 'real-value', got: %s", got)
	}
	os.Unsetenv("SET_VAR_FOR_TEST")
}
