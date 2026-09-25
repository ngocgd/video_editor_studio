//go:build windows

package main

import (
	"os/exec"
	"strconv"
)

// configureProcGroup and killProcessGroup exist so the host-side
// fallback (decision: "the same binary bound to 127.0.0.1, reusing
// authorized()... spawns the host claude with the same golden argv")
// can run natively as a Windows process, not just inside the Linux
// container. Windows has no POSIX process-group signal; `taskkill /T`
// (kill the process tree) is the standard equivalent and is what both
// Cancel and the explicit kill path below use — there is no separate
// graceful/forceful distinction to make here the way SIGTERM/SIGKILL
// gives on Unix, since taskkill /F is already forceful.
func configureProcGroup(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return taskkillTree(cmd.Process.Pid)
	}
}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = taskkillTree(cmd.Process.Pid)
	}
}

func taskkillTree(pid int) error {
	return exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run()
}
