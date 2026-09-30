//go:build windows

package app

import "os/exec"

// postStart assigns the freshly launched serve process to a Job Object
// with KILL_ON_JOB_CLOSE so Stop() terminates the whole tree (the
// Windows equivalent of killing a process group).
func postStart(cmd *exec.Cmd) error { return assignToJob(cmd) }
