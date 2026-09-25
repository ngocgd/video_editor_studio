//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// configureProcGroup puts cmd in its own process group so the whole
// group (the CLI may spawn its own subprocesses, e.g. the Node runtime)
// can be signaled together, and wires Cancel to send SIGTERM to that
// group on context cancellation/timeout.
func configureProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
}

// killProcessGroup force-kills the whole process group started by a
// cmd previously passed to configureProcGroup.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
