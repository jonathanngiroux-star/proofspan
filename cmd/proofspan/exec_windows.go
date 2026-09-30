//go:build windows

package main

import (
	"os"
	"os/exec"
)

// execFrontEnd: Windows has no syscall.Exec; spawn the front-end attached
// to our stdio and exit with its code.
func execFrontEnd(path string, argv []string, env []string) error {
	cmd := exec.Command(path)
	cmd.Args = argv
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = env
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		return err
	}
	os.Exit(0)
	return nil
}
