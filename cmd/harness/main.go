// Command harness runs unattended coding agents and makes watching them easy.
//
// The loop is: prompt an agent, have an independent reviewer judge the result,
// repeat until it passes. A run lives in its own tmux session so it survives
// the terminal that started it, reports through a notes file rather than a
// tool-call firehose, and prints a plain line log you can pipe or scroll back.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/MahammadAgayev/harness/internal/agent"
	agentclaude "github.com/MahammadAgayev/harness/internal/agent/claude"
	agentpi "github.com/MahammadAgayev/harness/internal/agent/pi"
	"github.com/MahammadAgayev/harness/internal/loop"
	"github.com/MahammadAgayev/harness/internal/run"
	"github.com/MahammadAgayev/harness/internal/tmuxctl"
)

const usage = `harness — unattended agent runs

  harness init <name> [-C dir] [-a agent] [-cmd "..."]
                                            create a run and open its task
  harness start <name>                      run it, detached in tmux
  harness attach <name>                     jump to a run's tmux session
  harness stop <name>                       halt after the current iteration
  harness list                              one line per run
  harness run <name>                        run in the foreground (used by tmux)

A run is a folder under ~/.harness/<name>/ holding task.md (the contract),
notes.md (what the agent reports), log.md, state.json, and reviews/.

Edit task.md while a run is going and the next iteration works to the new
version — that is how you change course.

The agent command is overridable per run with -cmd, or for every run with
$HARNESS_PI_COMMAND / $HARNESS_CLAUDE_COMMAND, which take precedence.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]

	var err error
	switch cmd {
	case "init":
		err = cmdInit(args)
	case "start":
		err = cmdStart(args)
	case "run":
		err = cmdRun(args)
	case "attach":
		err = cmdAttach(args)
	case "stop":
		err = cmdStop(args)
	case "list", "ls":
		err = cmdList(args)
	case "-h", "--help", "help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "harness: "+err.Error())
		os.Exit(1)
	}
}

// newAgent builds the adapter for a name.
//
// The command is overridable because neither CLI is always invoked as a bare
// binary — a wrapper that injects credentials is a normal deployment, and on
// one of the author's profiles claude is reached through `authwrap agent run`.
// cmd is the command recorded for the run, from `init -cmd`. Empty means the
// adapter's default. The environment variable wins over both, so a machine
// where the CLI moved can be fixed without editing every run's state.json.
func newAgent(name, cmd string) (agent.Agent, error) {
	switch name {
	case "", "pi":
		return agentpi.New(commandFor("HARNESS_PI_COMMAND", cmd)...), nil
	case "claude":
		return agentclaude.New(commandFor("HARNESS_CLAUDE_COMMAND", cmd)...), nil
	}
	return nil, fmt.Errorf("unknown agent %q (want pi or claude)", name)
}

// commandFor resolves the command words for an adapter: environment first,
// then the run's own setting, then nothing — which each adapter reads as "use
// the bare binary".
func commandFor(envKey, cmd string) []string {
	if v := strings.TrimSpace(os.Getenv(envKey)); v != "" {
		return fields(v)
	}
	return fields(cmd)
}

func fields(s string) []string { return strings.Fields(s) }

// parseFlags parses flags that may appear on either side of the positional
// arguments.
//
// Go's flag package stops at the first non-flag argument, so `harness init
// demo -C /tree` silently drops the -C and records the wrong directory — a
// run then works on the wrong tree with no error. Since every harness command
// takes a run name as its positional, pulling the non-flags out first and
// parsing the rest is both safe and what anyone would expect from a CLI.
func parseFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	var flagArgs, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") || a == "-" {
			positional = append(positional, a)
			continue
		}
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		flagArgs = append(flagArgs, a)
		// A flag that takes a value and was written as `-C dir` rather than
		// `-C=dir` consumes the next argument, which must not be mistaken for
		// a positional.
		if strings.Contains(a, "=") {
			continue
		}
		name := strings.TrimLeft(a, "-")
		if f := fs.Lookup(name); f != nil && !isBoolFlag(f) && i+1 < len(args) {
			i++
			flagArgs = append(flagArgs, args[i])
		}
	}
	if err := fs.Parse(flagArgs); err != nil {
		return nil, err
	}
	return positional, nil
}

// first returns the first positional argument, or "".
func first(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

func isBoolFlag(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}

func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	dir := fs.String("C", "", "directory the run operates on (default: cwd)")
	agentName := fs.String("a", "pi", "agent to drive: pi or claude")
	goal := fs.String("goal", "", "one-line goal, written into task.md")
	command := fs.String("cmd", "", `command that runs the agent, e.g. "authwrap agent run claude"`)
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	name := first(pos)
	if name == "" {
		return errors.New("usage: harness init <name>")
	}
	if _, err := newAgent(*agentName, *command); err != nil {
		return err
	}

	cwd := *dir
	if cwd == "" {
		var err error
		if cwd, err = os.Getwd(); err != nil {
			return err
		}
	}
	// Resolved now, because the run folder is global and the recorded cwd is
	// the only thing tying a run back to the tree it works on.
	cwd, err = filepath.Abs(cwd)
	if err != nil {
		return err
	}

	d := run.Open(name)
	if err := d.Create(cwd, *agentName, *command, *goal); err != nil {
		return err
	}
	fmt.Printf("created %s\n", d.Path)
	fmt.Printf("  working on %s with %s\n", cwd, *agentName)
	if *command != "" {
		fmt.Printf("  running %s\n", *command)
	}
	fmt.Println()
	if *goal == "" {
		fmt.Printf("Write the task, then start it:\n  $EDITOR %s\n  harness start %s\n",
			d.TaskFile(), name)
	} else {
		fmt.Printf("Refine it if you like, then start:\n  $EDITOR %s\n  harness start %s\n",
			d.TaskFile(), name)
	}
	return nil
}

func cmdStart(args []string) error {
	fs := flag.NewFlagSet("start", flag.ExitOnError)
	attach := fs.Bool("attach", false, "jump to the run's session after starting")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	name := first(pos)
	if name == "" {
		return errors.New("usage: harness start <name>")
	}

	d := run.Open(name)
	if !d.Exists() {
		return fmt.Errorf("no run named %q — create it with: harness init %s", name, name)
	}
	// Checked here rather than only in the loop, so a bad task fails in the
	// terminal the user is looking at instead of inside a detached session
	// they would have to go and find.
	task, err := d.Task()
	if err != nil {
		return err
	}
	if !run.Runnable(task) {
		return fmt.Errorf("task.md has not been written yet:\n  $EDITOR %s", d.TaskFile())
	}
	st, err := d.ReadState()
	if err != nil {
		return err
	}

	self, err := os.Executable()
	if err != nil {
		self = "harness"
	}
	if err := tmuxctl.StartDetached(name, st.Cwd, []string{self, "run", name}, sessionEnv()); err != nil {
		return err
	}

	fmt.Printf("started %s in tmux session %s\n", name, tmuxctl.SessionName(name))
	fmt.Printf("  harness attach %s   watch it\n", name)
	fmt.Printf("  harness list         all runs\n")
	if *attach {
		return tmuxctl.Attach(name)
	}
	return nil
}

// sessionEnv is the environment a detached run needs carried into its tmux
// session.
//
// A tmux session inherits the server's environment, not the caller's, and the
// server may have been started hours ago from a different shell. Anything the
// run depends on that was set in this shell is otherwise silently absent — the
// run dies on its first iteration and the session vanishes before you can look
// at it.
//
// Deliberately a short allowlist rather than os.Environ(): copying the whole
// environment into a long-lived tmux session bakes in a stale PATH and every
// secret that happens to be exported, and tmux keeps them for the life of the
// session.
func sessionEnv() []string {
	keys := []string{
		"HARNESS_HOME",       // the run folder root; tests and alternate homes
		"HARNESS_PI_COMMAND", // adapter command overrides
		"HARNESS_CLAUDE_COMMAND",
		"PATH", // the agent binary itself
		"HOME",
		"SHELL",
		"LANG", "LC_ALL",
		// Credentials. An agent that cannot authenticate fails on iteration one,
		// which is exactly the failure that used to be invisible.
		"ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN",
		"OPENAI_API_KEY", "OPENAI_BASE_URL",
		"GEMINI_API_KEY", "GOOGLE_API_KEY",
	}
	var out []string
	for _, k := range keys {
		if v, ok := os.LookupEnv(k); ok {
			out = append(out, k+"="+v)
		}
	}
	return out
}

// cmdRun is the foreground loop. tmux executes this; it is not normally typed
// by hand, though running it directly is a fine way to watch a run.
func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	quiet := fs.Bool("quiet", false, "iteration and verdict lines only, no tool activity")
	pos, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	name := first(pos)
	if name == "" {
		return errors.New("usage: harness run <name>")
	}

	d := run.Open(name)
	st, err := d.ReadState()
	if err != nil {
		return err
	}
	ag, err := newAgent(st.Agent, st.Command)
	if err != nil {
		return err
	}

	// A signal stops the run the same way `harness stop` does, so an
	// interrupted run still unwinds and records why it ended rather than
	// leaving a stale "running" behind.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	log := &lineLog{quiet: *quiet}
	log.header(name, st)

	// The loop runs on this goroutine and the observer prints from it, so the
	// lines come out in the order they happened with no coordination needed.
	_, _, err = loop.Run(ctx, loop.Config{Dir: d, Agent: ag, Observer: log.observer()})
	return err
}

// lineLog prints a run as plain timestamped lines.
//
// This is the entire display. Append-only and line-oriented on purpose: the
// output survives being piped to a file, read back over ssh, or scrolled
// through hours later, none of which a full-screen UI does. Anything that
// wants to be re-read rather than watched — the notes, the reviews, the
// findings in full — is already a file in the run folder.
type lineLog struct {
	quiet bool

	iterAt time.Time
	tools  int
}

func (l *lineLog) header(name string, st *run.State) {
	agentName := st.Agent
	if agentName == "" {
		agentName = "pi"
	}
	l.printf("%s · %s · %s", name, agentName, st.Cwd)
}

func (l *lineLog) printf(format string, args ...any) {
	fmt.Printf("%s  %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
}

func (l *lineLog) observer() loop.Observer {
	return loop.Observer{
		OnPhase: func(p loop.Phase, i int) {
			if p == loop.PhaseWorking {
				// A new iteration: the counters describe one pass, not the run.
				l.iterAt = time.Now()
				l.tools = 0
				fmt.Println()
			}
			l.printf("iteration %d  %s", i, p)
		},
		OnActivity: func(e agent.Event) {
			switch e.Kind {
			case agent.KindToolStart:
				// Counted even when not printed, so -quiet still reports how
				// much work an iteration did.
				l.tools++
				if !l.quiet {
					l.printf("  · %s %s", e.Tool, truncate(e.Detail, 100))
				}
			case agent.KindError:
				l.printf("  ! %s", truncate(e.Text, 200))
			}
		},
		OnVerdict: func(i int, v run.Verdict, findings string) {
			l.printf("iteration %d  %s  (%s, %d tool calls)",
				i, v, since(l.iterAt), l.tools)
			// One line of why, for the common case of reading this as it
			// happens. reviews/%03d.md has the whole thing.
			if v != run.VerdictPass {
				if s := firstLine(findings); s != "" {
					l.printf("  %s", truncate(s, 160))
				}
			}
		},
		OnEnded: func(status run.Status, ending string) {
			fmt.Println()
			l.printf("run ended: %s (%s)", ending, status)
		},
	}
}

// since renders an elapsed time at a resolution a human cares about.
func since(t time.Time) string {
	if t.IsZero() {
		return "0s"
	}
	d := time.Since(t).Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// firstLine is the first non-empty line, flattened. Reviewer findings open
// with a heading or a blank line often enough to be worth skipping.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(strings.TrimLeft(line, "#-* ")); t != "" {
			return t
		}
	}
	return ""
}

// truncate flattens a string to one line and caps it at n runes.
//
// Runes, not bytes: reviewer findings and tool arguments are full of em-dashes
// and quotes, and cutting one in half emits a broken rune that renders as a
// replacement character.
func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func cmdAttach(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: harness attach <name>")
	}
	return tmuxctl.Attach(args[0])
}

func cmdStop(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: harness stop <name>")
	}
	return stopRun(args[0])
}

func stopRun(name string) error {
	d := run.Open(name)
	if !d.Exists() {
		return fmt.Errorf("no run named %q", name)
	}
	if err := d.RequestStop(); err != nil {
		return err
	}
	// Graceful: the loop notices the file and halts after the current
	// iteration, having written its final state. Killing the tmux session
	// would be faster and would leave the run's record a lie.
	fmt.Printf("stop requested — %s will halt after the current iteration\n", name)
	if !tmuxctl.HasSession(name) {
		fmt.Println("  (no live session; the flag will apply to the next start)")
	}
	return nil
}

func cmdList(args []string) error {
	states, err := run.List()
	if err != nil {
		return err
	}
	if len(states) == 0 {
		fmt.Println("no runs yet — harness init <name>")
		return nil
	}
	live := tmuxctl.LiveRuns()

	// run.List already returns them newest first.

	w := 0
	for _, s := range states {
		if len(s.Name) > w {
			w = len(s.Name)
		}
	}
	for _, s := range states {
		status := string(s.Status)
		// Reconciled against tmux: state.json cannot be
		// trusted about liveness.
		if s.Status == run.StatusRunning && !live[s.Name] {
			status = "dead"
		}
		verdict := string(s.Verdict)
		if verdict == string(run.VerdictUnknown) {
			verdict = "—"
		}
		fmt.Printf("%-*s  %-8s  iter %-3d  %-10s  %s\n",
			w, s.Name, status, s.Iteration, verdict, s.Cwd)
	}
	return nil
}
