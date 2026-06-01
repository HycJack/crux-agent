//go:build windows

package ui

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32                        = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleMode              = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode              = kernel32.NewProc("SetConsoleMode")
	enableVirtualTerminalProcessing = uint32(0x0004)
)

// enableVTOnHandle enables ENABLE_VIRTUAL_TERMINAL_PROCESSING on the given
// console handle. If the handle is not a console (e.g. redirected to a file)
// the call is a no-op.
func enableVTOnHandle(f *os.File) {
	handle := f.Fd()
	var mode uint32
	r, _, _ := procGetConsoleMode.Call(handle, uintptr(unsafe.Pointer(&mode)))
	if r == 0 {
		return
	}
	procSetConsoleMode.Call(handle, uintptr(mode|enableVirtualTerminalProcessing))
}
