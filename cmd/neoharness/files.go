package main

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/MahammadAgayev/neoharness/api/model"
	agentclaude "github.com/MahammadAgayev/neoharness/hub/agent/claude"
	"github.com/MahammadAgayev/neoharness/hub/procgroup"
)

// fileLimit is the most files `@` gets as one list. A folder with more, such
// as a monorepo, is searched on each query instead: its whole list is too
// large to send (1.8M files are 176 MB).
const fileLimit = 50000

// searchLimit is the most files one search returns.
const searchLimit = 2000

// listScript prints a folder's files relative to it, at most $1: what git
// tracks and does not ignore, or else a walk with a depth limit. A repository
// that tracks $1 or more prints only ":many", as listing its untracked files
// walks the whole tree and takes minutes there. The same script runs locally
// and over ssh, so `@` completes the files Claude sees where it runs.
const listScript = `if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  if [ "$(git ls-files | head -n "$1" | wc -l)" -ge "$1" ]; then echo :many; exit; fi
  git ls-files --cached --others --exclude-standard
else
  ` + walk + `
fi | head -n "$1"`

// searchScript prints the files of a folder whose path holds $1, in any
// case, at most $2. It lists only what git tracks, which is fast.
const searchScript = `{ git ls-files 2>/dev/null || ` + walk + `; } | grep -i -F -e "$1" | head -n "$2"`

const walk = `find . -maxdepth 6 -type f -not -path '*/.*' -not -path '*/node_modules/*' \
  -not -path '*/__pycache__/*' -not -path '*/venv/*' | sed 's,^\./,,'`

// listFiles lists a location's files for `@`, or with a query only those
// whose path holds it. many says the folder has too many to list: search it.
func listFiles(ctx context.Context, l model.Location, query string) (paths []string, many bool, err error) {
	var out string
	if query == "" {
		out, err = runIn(ctx, l, listScript, strconv.Itoa(fileLimit))
	} else {
		out, err = runIn(ctx, l, searchScript, query, strconv.Itoa(searchLimit))
	}
	if err != nil {
		return nil, false, err
	}
	for _, p := range strings.Split(out, "\n") {
		if p != "" {
			paths = append(paths, p)
		}
	}
	if query == "" && (len(paths) >= fileLimit || slices.Equal(paths, []string{":many"})) {
		return nil, true, nil
	}
	return paths, false, nil
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
		if sh := localShell(); sh != "" {
			args[0] = sh
		} else {
			// Windows without Git's sh: the command still runs, in cmd.
			args = []string{"cmd", "/C", command}
		}
		cmd = exec.CommandContext(ctx, args[0], args[1:]...)
		cmd.Dir = l.Dir
	}
	procgroup.Isolate(cmd)
	cmd.Cancel = func() error { return procgroup.Kill(cmd) }
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return string(out), exit.ExitCode(), nil
	}
	return string(out), 0, err
}

// runIn runs a shell script in a location's folder, over ssh for a remote one.
// The script sees args as $1, $2 and on.
func runIn(ctx context.Context, l model.Location, script string, args ...string) (string, error) {
	args = append([]string{"sh", "-c", script, "sh"}, args...)
	var cmd *exec.Cmd
	if l.Remote() {
		ssh := agentclaude.SSHCommand(l.Host, l.Dir, args)
		cmd = exec.CommandContext(ctx, ssh[0], ssh[1:]...)
	} else {
		sh := localShell()
		if sh == "" {
			return "", errors.New("no sh found: install Git for Windows")
		}
		cmd = exec.CommandContext(ctx, sh, args[1:]...)
		cmd.Dir = l.Dir
	}
	// A wrapper's child can outlive the kill and hold the pipe open; do not
	// wait for it, or the hub cannot stop.
	cmd.WaitDelay = time.Second
	out, err := cmd.Output()
	return string(out), err
}
