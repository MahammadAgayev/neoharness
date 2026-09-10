//go:build unix

package pi

import (
	"errors"
	"os/exec"
	"syscall"
)

// killGroup kills the child and everything it spawned.
//
// The negative pid targets the whole process group, which is the point: an
// agent that shelled out to a build has children holding the stdout pipe open,
// and killing only the direct child leaves the read loop blocked until that
// grandchild finishes on its own — exactly the hang the timeout exists to
// prevent. SIGKILL rather than SIGTERM because this path is already the
// last resort; a process that ignored its deadline gets no chance to ignore a
// polite signal too.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		// The group may already be gone, or may never have been created if the
		// child died between Start and here. Fall back to the single process.
		_ = cmd.Process.Kill()
	}
}

// asExitError reports whether err is a process exit status, as opposed to a
// failure to run the process at all. The distinction matters upstream: an agent
// that ran and exited non-zero is a Result the loop can reason about, while a
// missing binary is an error that ends the run.
func asExitError(err error, target **exec.ExitError) bool {
	return errors.As(err, target)
}
