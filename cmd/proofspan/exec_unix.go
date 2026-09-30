//go:build !windows

package main

import (
	"os"
	"syscall"
)

// execFrontEnd replaces the current process (true exec: the TUI/GUI
// becomes this PID, signals and exit codes propagate naturally).
func execFrontEnd(path string, argv []string, env []string) error {
	return syscall.Exec(path, argv, env)
}

// ensure os is used on all platforms (Windows file imports it too)
var _ = os.Exit
