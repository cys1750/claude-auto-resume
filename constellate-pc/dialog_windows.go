//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

// The Windows build is linked with -H=windowsgui so double-clicking the exe does
// not flash a console window. That leaves no stderr for the user to read, so
// fatal errors go to a message box instead.
func showFatalDialog(msg string) {
	user32 := syscall.NewLazyDLL("user32.dll")
	messageBox := user32.NewProc("MessageBoxW")

	text, err := syscall.UTF16PtrFromString(msg)
	if err != nil {
		return
	}
	title, err := syscall.UTF16PtrFromString("Constellate")
	if err != nil {
		return
	}
	const mbIconError = 0x00000010
	messageBox.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), mbIconError)
}
