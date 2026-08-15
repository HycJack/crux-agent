//go:build !windows

package tools

import "syscall"

// sysProcAttrHide is a no-op on non-Windows platforms. It always returns
// nil because only Windows uses SysProcAttr to hide the console window.
func sysProcAttrHide() *syscall.SysProcAttr {
	return nil
}

// decodeWindowsOutput is a no-op on non-Windows platforms.
func decodeWindowsOutput(b []byte) string {
	return string(b)
}
