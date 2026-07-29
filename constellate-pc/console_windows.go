//go:build windows

package main

import (
	"os"
	"syscall"
)

// The executable is linked with -H=windowsgui so double-clicking it does not
// flash up a console window. The cost is that a GUI binary starts with no
// console at all, so anything printed when it is launched *from* a terminal goes
// nowhere — which would silently swallow the addresses -lan needs to report.
//
// AttachConsole joins the console of the process that launched us, when there is
// one, and the standard handles are rebound onto it. Double-clicked, there is no
// parent console, the call fails, and nothing changes.
func attachParentConsole() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	attachConsole := kernel32.NewProc("AttachConsole")

	const attachParentProcess = ^uintptr(0) // (DWORD)-1
	if ret, _, _ := attachConsole.Call(attachParentProcess); ret == 0 {
		return
	}
	// Go's os.Stdout was bound to the (invalid) handle we started with, so open
	// the console's own streams and point the standard files at them.
	if out, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout = out
		os.Stderr = out
	}
	if in, err := os.OpenFile("CONIN$", os.O_RDONLY, 0); err == nil {
		os.Stdin = in
	}
}
