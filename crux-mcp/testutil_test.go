package mcp_test

import "os"

// writeFileImpl is the os.WriteFile wrapper used by tests. Kept in
// its own file so the test file imports stay minimal.
func writeFileImpl(path string, data []byte, mode uint32) error {
	return os.WriteFile(path, data, os.FileMode(mode))
}