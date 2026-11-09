package main

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"syscall"
	"time"

	agentclaude "github.com/MahammadAgayev/neoharness/internal/agent/claude"
	"github.com/MahammadAgayev/neoharness/internal/model"
)

// listScript prints a folder's files relative to it: what git tracks and does
// not ignore, or else a walk with a depth limit. The same script runs locally
// and over ssh, so `@` completes the files Claude sees where it runs.
const listScript = `{ git ls-files --cached --others --exclude-standard 2>/dev/null ||
  find . -maxdepth 6 -type f -not -path '*/.*' -not -path '*/node_modules/*' | sed 's,^\./,,'; } | head -n 20000`

func listFiles(ctx context.Context, l model.Location) ([]string, error) {
	out, err := runIn(ctx, l, listScript)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range strings.Split(out, "\n") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

// gitBranch reads the branch of a location, or the short commit when HEAD is
// detached. Outside a repository it is empty.
func gitBranch(ctx context.Context, l model.Location) (string, error) {
	out, err := runIn(ctx, l, `git branch --show-current 2>/dev/null | grep . || git rev-parse --short HEAD 2>/dev/null || true`)
	return strings.TrimSpace(out), err
}

// runShell runs a `!` command in a location's folder, in the user's shell
// there ($SHELL, so zsh globs and the like work), over ssh for a remote one.
func runShell(ctx context.Context, l model.Location, command string) (string, int, error) {
	args := []string{"sh", "-c", `exec "${SHELL:-sh}" -c "$0"`, command}
	var cmd *exec.Cmd
	if l.Remote() {
		ssh := agentclaude.SSHCommand(l.Host, l.Dir, args)
		cmd = exec.CommandContext(ctx, ssh[0], ssh[1:]...)
	} else {
		cmd = exec.CommandContext(ctx, args[0], args[1:]...)
		cmd.Dir = l.Dir
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return string(out), exit.ExitCode(), nil
	}
	return string(out), 0, err
}

// runIn runs a shell script in a location's folder, over ssh for a remote one.
func runIn(ctx context.Context, l model.Location, script string) (string, error) {
	args := []string{"sh", "-c", script}
	var cmd *exec.Cmd
	if l.Remote() {
		ssh := agentclaude.SSHCommand(l.Host, l.Dir, args)
		cmd = exec.CommandContext(ctx, ssh[0], ssh[1:]...)
	} else {
		cmd = exec.CommandContext(ctx, args[0], args[1:]...)
		cmd.Dir = l.Dir
	}
	// A wrapper's child can outlive the kill and hold the pipe open; do not
	// wait for it, or the hub cannot stop.
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	return string(out), err
}
