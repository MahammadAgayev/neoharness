package tmuxctl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeTmux puts a scripted `tmux` at the front of PATH, so these tests exercise
// the argument construction and output parsing without needing a tmux server —
// and without the test suite's behaviour depending on whatever sessions happen
// to be open on the developer's machine.
//
// The script appends its arguments to $TMUX_CALLS so a test can assert what was
// asked of tmux, which is the part that actually has to be right.
func fakeTmux(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls.txt")
	// $STARTED lets a script model session creation: new-session touches it and
	// has-session tests for it, so StartDetached's post-check sees a session
	// that actually came into being.
	script := "#!/bin/sh\nSTARTED=" + filepath.Join(dir, "started") +
		"\necho \"$@\" >> " + calls + "\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// Inside() consults $TMUX first; clear it so tests choose their own answer.
	t.Setenv("TMUX", "")
	return calls
}

func readCalls(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func TestSessionNameIsNamespaced(t *testing.T) {
	// The prefix is what keeps a run from colliding with a project session of
	// the same name, and what makes runs recognisable in a picker.
	if got := SessionName("my-run"); got != "harness/my-run" {
		t.Errorf("SessionName = %q, want harness/my-run", got)
	}
}

func TestHasSessionReflectsExitStatus(t *testing.T) {
	calls := fakeTmux(t, `exit 0`)
	if !HasSession("demo") {
		t.Error("exit 0 from has-session means the session exists")
	}
	if !strings.Contains(readCalls(t, calls), "has-session -t harness/demo") {
		t.Errorf("wrong invocation: %s", readCalls(t, calls))
	}

	fakeTmux(t, `exit 1`)
	if HasSession("demo") {
		t.Error("a non-zero exit means no session")
	}
}

func TestLiveRunsParsesAndStripsThePrefix(t *testing.T) {
	fakeTmux(t, `
cat <<'EOF'
harness/alpha
harness/beta
devenv
app-code
EOF`)
	live := LiveRuns()
	if !live["alpha"] || !live["beta"] {
		t.Errorf("expected alpha and beta to be live, got %v", live)
	}
	// Non-harness sessions are other work and must not show up as runs.
	if live["devenv"] || live["app-code"] || live["harness/alpha"] {
		t.Errorf("unrelated sessions leaked into the run list: %v", live)
	}
	if len(live) != 2 {
		t.Errorf("expected exactly 2 live runs, got %d: %v", len(live), live)
	}
}

func TestLiveRunsWithNoServer(t *testing.T) {
	// No tmux server is a legitimate answer — no runs — not an error the
	// dashboard should have to render.
	fakeTmux(t, `echo "no server running" >&2; exit 1`)
	if got := LiveRuns(); len(got) != 0 {
		t.Errorf("expected no live runs, got %v", got)
	}
}

func TestLiveRunsIgnoresBlankLines(t *testing.T) {
	fakeTmux(t, `printf 'harness/one\n\n\nharness/two\n\n'`)
	if got := LiveRuns(); len(got) != 2 {
		t.Errorf("expected 2 runs, got %v", got)
	}
}

func TestStartDetachedBuildsTheRightCommand(t *testing.T) {
	// has-session must report "no session" or StartDetached refuses; the script
	// distinguishes the two subcommands.
	calls := fakeTmux(t, `
case "$1" in
  has-session) test -f `+"$STARTED"+` ;;
  new-session) touch `+"$STARTED"+`; exit 0 ;;
  *) exit 0 ;;
esac`)
	err := StartDetached("demo", "/some/tree", []string{"harness", "run", "demo"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := readCalls(t, calls)
	// -d is the whole point: the session is created detached, so the caller is
	// never attached and closing its terminal cannot take the run down.
	if !strings.Contains(got, "new-session -d -s harness/demo -c /some/tree harness run demo") {
		t.Errorf("wrong new-session invocation:\n%s", got)
	}
}

func TestStartDetachedRefusesADuplicate(t *testing.T) {
	fakeTmux(t, `exit 0`) // has-session succeeds: a session already exists
	err := StartDetached("demo", "/tree", []string{"harness", "run", "demo"}, nil)
	if err == nil {
		t.Fatal("expected a refusal to start a second session for one run")
	}
	if !strings.Contains(err.Error(), "already has a live session") {
		t.Errorf("error should explain the conflict, got: %v", err)
	}
}

func TestStartDetachedReportsTmuxFailure(t *testing.T) {
	calls := fakeTmux(t, `
case "$1" in
  has-session) exit 1 ;;
  *) echo "some tmux problem" >&2; exit 1 ;;
esac`)
	err := StartDetached("demo", "/tree", []string{"harness", "run", "demo"}, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	// The message tmux printed is the only clue to what went wrong.
	if !strings.Contains(err.Error(), "some tmux problem") {
		t.Errorf("tmux's own message should be surfaced, got: %v", err)
	}
	_ = calls
}

func TestStartDetachedOmitsCwdWhenEmpty(t *testing.T) {
	calls := fakeTmux(t, `
case "$1" in
  has-session) test -f `+"$STARTED"+` ;;
  new-session) touch `+"$STARTED"+`; exit 0 ;;
  *) exit 0 ;;
esac`)
	if err := StartDetached("demo", "", []string{"harness", "run", "demo"}, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readCalls(t, calls), "-c ") {
		t.Errorf("no cwd should mean no -c flag:\n%s", readCalls(t, calls))
	}
}

func TestAttachUsesSwitchClientInsideTmux(t *testing.T) {
	calls := fakeTmux(t, `exit 0`)
	t.Setenv("TMUX", "/tmp/tmux-501/default,123,0")
	if err := Attach("demo"); err != nil {
		t.Fatal(err)
	}
	got := readCalls(t, calls)
	if !strings.Contains(got, "switch-client -t harness/demo") {
		t.Errorf("inside tmux, attach must switch-client:\n%s", got)
	}
	// attach-session from inside tmux is an error tmux refuses outright.
	if strings.Contains(got, "attach-session") {
		t.Errorf("attach-session must not be used from inside tmux:\n%s", got)
	}
}

func TestAttachUsesAttachSessionOutsideTmux(t *testing.T) {
	calls := fakeTmux(t, `exit 0`)
	// No $TMUX and list-clients returns nothing: we are outside.
	if err := Attach("demo"); err != nil {
		t.Fatal(err)
	}
	got := readCalls(t, calls)
	if !strings.Contains(got, "attach-session -t harness/demo") {
		t.Errorf("outside tmux, attach must attach-session:\n%s", got)
	}
}

func TestAttachRefusesADeadRun(t *testing.T) {
	fakeTmux(t, `
case "$1" in
  has-session) exit 1 ;;
  *) exit 0 ;;
esac`)
	err := Attach("demo")
	if err == nil {
		t.Fatal("expected an error attaching to a run with no session")
	}
	if !strings.Contains(err.Error(), "no live session") {
		t.Errorf("error should say the run is not live, got: %v", err)
	}
}

func TestKillSessionOnADeadRunIsNotAnError(t *testing.T) {
	fakeTmux(t, `
case "$1" in
  has-session) exit 1 ;;
  *) exit 0 ;;
esac`)
	// Killing something already gone is the desired end state, not a failure.
	if err := KillSession("demo"); err != nil {
		t.Errorf("killing an absent session should be a no-op, got %v", err)
	}
}

func TestKillSession(t *testing.T) {
	calls := fakeTmux(t, `exit 0`)
	if err := KillSession("demo"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readCalls(t, calls), "kill-session -t harness/demo") {
		t.Errorf("wrong invocation:\n%s", readCalls(t, calls))
	}
}

func TestInsideHonoursTmuxEnv(t *testing.T) {
	fakeTmux(t, `exit 1`) // list-clients fails, so only $TMUX can say yes
	t.Setenv("TMUX", "/tmp/tmux-501/default,123,0")
	if !Inside() {
		t.Error("$TMUX being set means we are inside tmux")
	}
}

func TestInsideFallsBackToListClients(t *testing.T) {
	// $TMUX is not exported into every launch context, so a running client is
	// the backstop. Without it, Attach would pick attach-session and wedge.
	fakeTmux(t, `echo "/dev/ttys001: 1 [80x24]"`)
	if !Inside() {
		t.Error("a live client means we are inside tmux even without $TMUX")
	}
}

func TestInsideFalseWhenNoClientsAndNoEnv(t *testing.T) {
	fakeTmux(t, `exit 1`)
	if Inside() {
		t.Error("no $TMUX and no clients means we are outside tmux")
	}
}

func TestAvailable(t *testing.T) {
	fakeTmux(t, `exit 0`)
	if !Available() {
		t.Error("tmux on PATH should be available")
	}
	// An empty PATH is the only reliable way to simulate a machine without it.
	t.Setenv("PATH", t.TempDir())
	if Available() {
		t.Error("tmux absent from PATH should not be available")
	}
}

func TestStartDetachedWithoutTmux(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	err := StartDetached("demo", "/tree", []string{"harness", "run", "demo"}, nil)
	if err == nil {
		t.Fatal("expected an error without tmux")
	}
	// tmux is how runs detach, so its absence needs an explanation rather than
	// an exec error.
	if !strings.Contains(err.Error(), "not installed") {
		t.Errorf("error should explain what tmux is for, got: %v", err)
	}
}

// TestStartDetachedCarriesEnvironment pins a bug that made every detached run
// die instantly and silently.
//
// A tmux session inherits the server's environment, not the caller's. The
// server may have been started hours earlier from an unrelated shell, so
// HARNESS_HOME — and the agent's API key, and any PATH entry it needs — simply
// were not there. The run exited on its first line and new-session still
// reported success.
func TestStartDetachedCarriesEnvironment(t *testing.T) {
	calls := fakeTmux(t, `
case "$1" in
  has-session) test -f `+"$STARTED"+` ;;
  new-session) touch `+"$STARTED"+`; exit 0 ;;
  *) exit 0 ;;
esac`)
	env := []string{"HARNESS_HOME=/tmp/h", "PATH=/usr/bin"}
	if err := StartDetached("demo", "/tree", []string{"harness", "run", "demo"}, env); err != nil {
		t.Fatal(err)
	}
	got := readCalls(t, calls)
	for _, want := range []string{"-e HARNESS_HOME=/tmp/h", "-e PATH=/usr/bin"} {
		if !strings.Contains(got, want) {
			t.Errorf("environment not passed to tmux (%q missing):\n%s", want, got)
		}
	}
	// The command must still come last, after the -e flags, or tmux reads the
	// binary name as another option.
	if !strings.Contains(got, "-e PATH=/usr/bin harness run demo") {
		t.Errorf("the command must follow the -e flags:\n%s", got)
	}
}

// TestStartDetachedDetectsAnImmediateExit pins the second half of the same
// bug: new-session returning 0 only means tmux forked something. Reporting
// "started" for a run that already died sends you to attach to a session that
// is not there.
func TestStartDetachedDetectsAnImmediateExit(t *testing.T) {
	// new-session succeeds, but the session never exists afterwards.
	fakeTmux(t, `
case "$1" in
  has-session) exit 1 ;;
  *) exit 0 ;;
esac`)
	err := StartDetached("demo", "/tree", []string{"harness", "run", "demo"}, nil)
	if err == nil {
		t.Fatal("a session that exited immediately must not be reported as started")
	}
	if !strings.Contains(err.Error(), "exited immediately") {
		t.Errorf("error should say the session died, got: %v", err)
	}
	// And it should say how to find out why.
	if !strings.Contains(err.Error(), "harness run demo") {
		t.Errorf("error should show the command to run by hand, got: %v", err)
	}
}
