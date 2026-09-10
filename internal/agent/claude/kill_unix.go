//go:build unix

package claude

import (
	"os/exec"
	"syscall"
)

// killGroup kills the child and everything it spawned. See the pi adapter's
// copy for why the negative pid matters: a grandchild holding the stdout pipe
// open blocks the read loop long after the direct child is gone.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
}
