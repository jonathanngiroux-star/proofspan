//go:build !windows

package app

import "os/exec"

// postStart: unix needs nothing after Start — the process group was set
// via SysProcAttr before launch.
func postStart(cmd *exec.Cmd) error { return nil }
