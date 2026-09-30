package app

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
)

// ServeProcess is a managed long-running `proofspan serve`. Stop() kills
// the whole process group so the server cannot outlive the GUI.
type ServeProcess struct {
	mu   sync.Mutex
	cmd  *exec.Cmd
	done chan struct{}
}

// StartServe launches `proofspan serve` and streams its output to onLine.
// dir is the working directory ("" = inherit); the CLI resolves its
// relative defaults from it.
func StartServe(args []string, extraEnv map[string]string, dir string, onLine func(string)) (*ServeProcess, error) {
	bin := ResolveBinary()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), envSlice(extraEnv)...)
	cmd.Stderr = cmd.Stdout
	if dir != "" {
		cmd.Dir = dir
	}
	// New process group → Stop() can kill the whole tree.
	cmd.SysProcAttr = sysProcAttrForGroup()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w (is the binary on PATH? set PROOFSPAN_BIN)", bin, err)
	}
	// Windows: assign the process to a kill-on-close Job Object (the
	// platform equivalent of a process group). Best-effort: a failure to
	// assign degrades Stop() to a direct kill, never breaks serving.
	_ = postStart(cmd)
	p := &ServeProcess{cmd: cmd, done: make(chan struct{})}
	go func() {
		defer close(p.done)
		sc := NewLineScanner(stdout)
		for sc.Scan() {
			if onLine != nil {
				onLine(sc.Text())
			}
		}
	}()
	return p, nil
}

// Running reports whether the server process is alive.
func (p *ServeProcess) Running() bool {
	if p == nil || p.cmd == nil {
		return false
	}
	select {
	case <-p.done:
		return false
	default:
		return p.cmd.ProcessState == nil
	}
}

// Stop terminates the server and waits for it to exit.
func (p *ServeProcess) Stop() error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return fmt.Errorf("no process")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	killProcessGroup(p.cmd)
	<-p.done
	return nil
}
