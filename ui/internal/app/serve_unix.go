//go:build !windows

package app

import (
	"os/exec"
	"syscall"
)

// sysProcAttrForGroup puts the serve process in a new process group so
// Stop() can terminate the whole tree.
func sysProcAttrForGroup() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup sends SIGTERM then SIGKILL to the process group.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
