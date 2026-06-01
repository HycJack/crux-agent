//go:build !windows

package ui

import "os"

// enableVTOnHandle is a no-op on non-Windows platforms where ANSI escapes
// are supported natively by the terminal.
func enableVTOnHandle(_ *os.File) {}
