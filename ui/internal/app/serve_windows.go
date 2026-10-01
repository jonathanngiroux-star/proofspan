//go:build windows

package app

import (
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"
)

// On Windows there are no POSIX process groups or SIGKILL; the correct
// way to kill a spawned tree is a Job Object with
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE — closing the handle terminates
// every process in the job.

type windowsJob struct {
	handle syscall.Handle
}

// sysProcAttrForGroup: no group semantics needed on Windows; the job is
// assigned right after Start (see assignToJob).
func sysProcAttrForGroup() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{}
}

var jobRegistry = map[*exec.Cmd]*windowsJob{}

// assignToJob puts the freshly started process into a kill-on-close job.
func assignToJob(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return fmt.Errorf("no process to assign")
	}
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	createJob := kernel32.NewProc("CreateJobObjectW")
	setInfo := kernel32.NewProc("SetInformationJobObject")
	assign := kernel32.NewProc("AssignProcessToJobObject")

	const JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE = 0x2000
	const JobObjectExtendedLimitInformation = 9

	type ioCounters struct {
		ReadOperationCount, WriteOperationCount, OtherOperationCount uint64
		ReadTransferCount, WriteTransferCount, OtherTransferCount    uint64
	}
	type basicLimit struct {
		PerProcessUserTimeLimit int64
		PerJobUserTimeLimit     int64
		LimitFlags              uint32
		MinimumWorkingSetSize   uintptr
		MaximumWorkingSetSize   uintptr
		ActiveProcessLimit      uint32
		Affinity                uintptr
		PriorityClass           uint32
		SchedulingClass         uint32
	}
	type extendedLimit struct {
		BasicLimitInformation basicLimit
		IoInfo                ioCounters
		ProcessMemoryLimit    uintptr
		JobMemoryLimit        uintptr
		PeakProcessMemoryUsed uintptr
		PeakJobMemoryUsed     uintptr
	}

	h, _, err := createJob.Call(0, 0, 0)
	if h == 0 {
		return fmt.Errorf("CreateJobObject: %v", err)
	}
	var info extendedLimit
	info.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	r, _, err := setInfo.Call(h, JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info))
	if r == 0 {
		return fmt.Errorf("SetInformationJobObject: %v", err)
	}
	// PROCESS_SET_QUOTA | PROCESS_TERMINATE
	const procRights = 0x0100 | 0x0001
	ph, _, _ := kernel32.NewProc("OpenProcess").Call(procRights, 0, uintptr(cmd.Process.Pid))
	if ph == 0 {
		return fmt.Errorf("OpenProcess(%d): %v", cmd.Process.Pid, err)
	}
	r, _, err = assign.Call(h, ph, 0)
	if r == 0 {
		return fmt.Errorf("AssignProcessToJobObject: %v", err)
	}
	jobRegistry[cmd] = &windowsJob{handle: syscall.Handle(h)}
	return nil
}

// killProcessGroup closes the job handle; KILL_ON_JOB_CLOSE takes the
// whole tree down. Falls back to a direct kill if the job is missing.
func killProcessGroup(cmd *exec.Cmd) {
	job, ok := jobRegistry[cmd]
	if !ok {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		return
	}
	syscall.CloseHandle(job.handle)
	delete(jobRegistry, cmd)
}
