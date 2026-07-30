//go:build !windows

package main

// Off Windows the process already has whatever terminal launched it.
func attachParentConsole() {}
