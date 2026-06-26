// Package testenv provides test utilities for loading .env files.
package testenv

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var loadOnce sync.Once

// LoadEnv loads environment variables from .env files in the current directory
// and parent directories. The first existing file found is used.
func LoadEnv() {
	loadOnce.Do(loadEnvInternal)
}

func loadEnvInternal() {
	dir, err := os.Getwd()
	if err != nil {
		return
	}

	// Search up to 5 levels up
	for i := 0; i < 5; i++ {
		// Prefer .env.test if it exists
		testPath := filepath.Join(dir, ".env.test")
		if _, err := os.Stat(testPath); err == nil {
			loadEnvFile(testPath)
		}
		// Then also load .env (but .env values won't override existing)
		envPath := filepath.Join(dir, ".env")
		if _, err := os.Stat(envPath); err == nil {
			loadEnvFile(envPath)
			return
		}
		if _, err := os.Stat(testPath); err == nil {
			return
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
}

// loadEnvFile reads the .env file and sets environment variables.
func loadEnvFile(path string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse KEY=VALUE
		idx := strings.Index(line, "=")
		if idx < 0 {
			continue
		}

		key := strings.TrimSpace(line[:idx])
		value := strings.TrimSpace(line[idx+1:])

		// Remove surrounding quotes
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}

		// Only set if not already set (so test overrides still work)
		if os.Getenv(key) == "" {
			os.Setenv(key, value)
		}
	}
}

// GetEnv returns the environment variable value, falling back to a default.
func GetEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
