//go:build unix

package main

import (
	"os/exec"
	"syscall"
)

// detach starts the child in a new session, with no terminal to hang up.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
