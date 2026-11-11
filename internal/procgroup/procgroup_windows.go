// Package procgroup starts a child so that it and everything it spawns can be
// killed together. A grandchild that outlives its parent holds the stdout pipe
// open and blocks the read loop long after the direct child is gone.
package procgroup

import (
	"os/exec"
	"strconv"
	"syscall"
)

const createNewProcessGroup = 0x00000200

// Isolate keeps a Ctrl+C in the terminal from reaching the child. Call it
// before Start.
func Isolate(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
}

// Kill kills the child and everything it spawned. Windows has no process
// group to signal, so taskkill walks the process tree.
func Kill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run(); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}
