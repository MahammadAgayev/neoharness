// Package tmuxctl is the harness's use of tmux: starting a run detached,
// jumping to it, and asking whether it is still alive.
//
// tmux is not an implementation detail that could be swapped for something
// else — it is the answer to "where does a run live when I close the
// terminal". Sessions rather than windows, one per run, named harness/<run>,
// so a run appears as a peer of the project sessions in a session picker
// rather than buried inside one.
//
// Asking tmux whether a session exists is also how liveness is decided. The
// status field in state.json is written by the run itself and cannot be
// trusted: a machine that lost power leaves "running" on disk forever. A tmux
// session either exists or it does not.
package tmuxctl

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// SessionPrefix namespaces harness sessions so they are recognisable in a
// picker and cannot collide with a project session of the same name.
const SessionPrefix = "harness/"

// SessionName is the tmux session for a run.
func SessionName(runName string) string { return SessionPrefix + runName }

// Available reports whether tmux is installed and a server is reachable.
//
// Distinguished from "no session" because the advice differs: install tmux
// versus start a run.
func Available() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

// Inside reports whether this process is running under tmux.
//
// $TMUX is not exported into every context a command can be launched from, so
// probing the server as a fallback avoids the case where switch-client is
// skipped and attach-session then wedges on an already-attached terminal.
func Inside() bool {
	if os.Getenv("TMUX") != "" {
		return true
	}
	out, err := exec.Command("tmux", "list-clients").Output()
	return err == nil && len(strings.TrimSpace(string(out))) > 0
}

// HasSession reports whether a run's session exists — the liveness oracle.
func HasSession(runName string) bool {
	return exec.Command("tmux", "has-session", "-t", SessionName(runName)).Run() == nil
}

// LiveRuns returns the set of run names with a live session, in one call.
//
// The dashboard needs this for every run on every poll; one list-sessions is
// cheaper and more consistent than N has-session calls, which could otherwise
// disagree with each other mid-refresh.
func LiveRuns() map[string]bool {
	live := map[string]bool{}
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		// No server running means no sessions, which is a legitimate answer.
		return live
	}
	for _, line := range strings.Split(string(out), "\n") {
		if name, ok := strings.CutPrefix(strings.TrimSpace(line), SessionPrefix); ok && name != "" {
			live[name] = true
		}
	}
	return live
}

// StartDetached creates the run's session running command, and returns at once.
//
// This is the line that makes a run outlive its terminal. The session is
// created detached, so the caller is never attached to it and closing the
// caller's terminal has no effect on the run.
func StartDetached(runName, dir string, command []string) error {
	if !Available() {
		return fmt.Errorf("tmux is not installed — it is how runs detach from your terminal")
	}
	if HasSession(runName) {
		return fmt.Errorf("run %q already has a live session (%s)", runName, SessionName(runName))
	}
	args := []string{"new-session", "-d", "-s", SessionName(runName)}
	if dir != "" {
		args = append(args, "-c", dir)
	}
	// The command is passed as separate arguments rather than a joined string,
	// so a path with a space in it cannot turn into two arguments.
	args = append(args, command...)
	out, err := exec.Command("tmux", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("starting tmux session: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Attach brings the run's session to the foreground.
//
// Inside tmux this is switch-client; outside it is attach-session. Using
// attach-session from within tmux is an error tmux refuses, and using
// switch-client without a client does nothing at all, so the distinction has
// to be made rather than guessed.
func Attach(runName string) error {
	if !HasSession(runName) {
		return fmt.Errorf("run %q has no live session", runName)
	}
	target := SessionName(runName)
	if Inside() {
		out, err := exec.Command("tmux", "switch-client", "-t", target).CombinedOutput()
		if err != nil {
			return fmt.Errorf("switching to %s: %w: %s", target, err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	// attach-session needs the terminal, so it replaces this process rather
	// than running as a child whose output nobody is reading.
	cmd := exec.Command("tmux", "attach-session", "-t", target)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// KillSession removes a run's session.
//
// This is the blunt stop: it kills the process tree without letting the loop
// finish its iteration or write its final state. The graceful path is the STOP
// file; this exists for a run that is wedged.
func KillSession(runName string) error {
	if !HasSession(runName) {
		return nil
	}
	out, err := exec.Command("tmux", "kill-session", "-t", SessionName(runName)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("killing %s: %w: %s", SessionName(runName), err, strings.TrimSpace(string(out)))
	}
	return nil
}
