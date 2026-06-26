package core

import (
	"os"
	"strings"
	"sync"
)

// ProviderEnv is a per-request map of environment overrides.
//
// Values take precedence over process.env. Used by providers to inject
// regional endpoint placeholders, proxy variables, and per-request
// overrides without mutating the global process environment.
//
// Reference: pi-mono packages/ai/src/utils/provider-env.ts
type ProviderEnv = map[string]string

// sandboxEnvCache memoizes /proc/self/environ reads for Linux sandboxes
// where process.env is empty (Bun compiled-binary sandbox quirk).
var (
	sandboxEnvOnce  sync.Once
	sandboxEnvCache map[string]string
)

// loadSandboxEnv populates sandboxEnvCache from /proc/self/environ.
// On non-Linux platforms or when /proc/self/environ is unreadable, the
// cache stays nil and lookups fall back to process.env.
func loadSandboxEnv() {
	data, err := os.ReadFile("/proc/self/environ")
	if err != nil {
		return
	}
	cache := make(map[string]string)
	for _, entry := range strings.Split(string(data), "\x00") {
		idx := strings.IndexByte(entry, '=')
		if idx <= 0 {
			continue
		}
		cache[entry[:idx]] = entry[idx+1:]
	}
	if len(cache) > 0 {
		sandboxEnvCache = cache
	}
}

// GetProviderEnvValue resolves an env variable by precedence:
//  1. Caller-supplied env map (ProviderEnv).
//  2. process.env.
//  3. /proc/self/environ fallback (Linux sandbox fallback).
//
// Returns the empty string if the variable is not set.
func GetProviderEnvValue(name string, env ProviderEnv) string {
	if env != nil {
		if v, ok := env[name]; ok && v != "" {
			return v
		}
	}
	if v, ok := os.LookupEnv(name); ok {
		return v
	}
	sandboxEnvOnce.Do(loadSandboxEnv)
	if v, ok := sandboxEnvCache[name]; ok {
		return v
	}
	return ""
}

// HasProviderEnvValue returns true if the named env var resolves to a
// non-empty string under the same precedence as GetProviderEnvValue.
func HasProviderEnvValue(name string, env ProviderEnv) bool {
	return GetProviderEnvValue(name, env) != ""
}
