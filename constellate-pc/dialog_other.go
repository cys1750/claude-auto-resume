//go:build !windows

package main

import (
	"fmt"
	"os"
)

// Off Windows the launcher runs from a terminal, so stderr is the visible place.
func showFatalDialog(msg string) {
	fmt.Fprintln(os.Stderr, "Constellate: "+msg)
}
