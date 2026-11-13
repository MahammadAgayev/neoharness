//go:build unix

// Package procgroup starts a child so that it and everything it spawns can be
// killed together. A grandchild that outlives its parent holds the stdout pipe
// open and blocks the read loop long after the direct child is gone.
package procgroup

import (
	"os/exec"
	"syscall"
)

// Isolate puts the child in its own process group. Call it before Start.
func Isolate(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// Kill kills the child and everything it spawned.
func Kill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}
