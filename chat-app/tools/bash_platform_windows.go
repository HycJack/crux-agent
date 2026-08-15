//go:build windows

package tools

import (
	"syscall"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// sysProcAttrHide returns a *syscall.SysProcAttr that hides the console
// window on Windows. On other platforms it returns nil.
func sysProcAttrHide() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{HideWindow: true}
}

// decodeWindowsOutput decodes a byte slice from the Windows active code
// page (usually GBK) into UTF-8. On non-Windows it's a no-op.
func decodeWindowsOutput(b []byte) string {
	if len(b) == 0 {
		return string(b)
	}
	// Try GBK first (most common on Chinese Windows).
	if decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(b); err == nil {
		return string(decoded)
	}
	// Fallback: assume it's already UTF-8.
	return string(b)
}
