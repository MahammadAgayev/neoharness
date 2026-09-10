// Package run owns a run's folder on disk: where it is, what is in it, and how
// it is read and written safely while something else is watching.
//
// Runs live in ~/.harness/<name>/ rather than beside the tree they operate on.
// A global dashboard is the whole point of this design, and it should be able
// to list one directory instead of scanning the filesystem for run folders or
// keeping an index that can go stale. The tree under work is recorded in
// state.json as Cwd, so nothing is lost except the ability to have two runs of
// the same name in two checkouts.
package run

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Status is where a run is in its life. It is written by the run's own process
// and read by anything watching, so it is advisory: a run whose machine lost
// power leaves "running" behind forever. Liveness is settled by asking tmux,
// not by trusting this field — see the tmuxctl package.
type Status string

const (
	StatusIdle    Status = "idle"    // created, never started
	StatusRunning Status = "running" // a process claims to be driving it
	StatusDone    Status = "done"    // the reviewer passed it
	StatusStopped Status = "stopped" // halted on request
	StatusFailed  Status = "failed"  // gave up: ceiling, repeated errors, crash
)

// Verdict is the reviewer's judgement on one iteration.
type Verdict string

const (
	VerdictUnknown   Verdict = "UNKNOWN"    // nothing reviewed yet
	VerdictPass      Verdict = "PASS"       // done
	VerdictNeedsWork Verdict = "NEEDS_WORK" // go again, with findings
	VerdictError     Verdict = "ERROR"      // the reviewer could not be run
)

// UnfilledGoal is the placeholder that init writes into task.md and that start
// refuses to run on.
//
// It is a single exported constant because the two commands must agree on the
// exact string. An untouched template is not blank, so without this check a
// start on a run whose task was never written sends the agent off to implement
// the template itself.
const UnfilledGoal = "(one paragraph — what should be true when this is done)"

// Iteration is one completed pass through the loop, kept for the history.
type Iteration struct {
	N             int       `json:"n"`
	Verdict       Verdict   `json:"verdict"`
	At            time.Time `json:"at"`
	WorkerSeconds int       `json:"workerSeconds"`
	WorkerTokens  int       `json:"workerTokens"`
	WorkerTools   int       `json:"workerTools"`
	ReviewSeconds int       `json:"reviewSeconds"`
	ReviewTools   int       `json:"reviewTools"`
}

// State is the single source of truth for a run.
//
// An earlier version of this harness derived the iteration count and last
// verdict by re-parsing the review files, which existed only because two
// places disagreed about the count. With one writer they cannot. The review
// files are still written — they are the readable record — but nothing reads
// them back.
type State struct {
	Name string `json:"name"`
	// Cwd is the tree this run operates on. Recorded because the run folder no
	// longer lives there.
	Cwd string `json:"cwd"`
	// Agent names the adapter driving it ("pi", "claude").
	Agent     string      `json:"agent"`
	Status    Status      `json:"status"`
	PID       int         `json:"pid"`
	Iteration int         `json:"iteration"`
	Verdict   Verdict     `json:"verdict"`
	Findings  string      `json:"findings"`
	History   []Iteration `json:"history"`
	CreatedAt time.Time   `json:"createdAt"`
	UpdatedAt time.Time   `json:"updatedAt"`
	// Ending is a sentence explaining why the run stopped, shown in the
	// dashboard once it is no longer live.
	Ending string `json:"ending,omitempty"`
}

// Dir is a run's folder.
type Dir struct {
	Name string
	Path string
}

// Root is the directory holding every run. Override with HARNESS_HOME, which
// exists so tests never touch the real one.
func Root() string {
	if h := os.Getenv("HARNESS_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		// No home directory is not a situation worth a recovery path; fall back
		// to the working directory so the error surfaces as a visible folder.
		return ".harness"
	}
	return filepath.Join(home, ".harness")
}

// Open names a run's folder. It does not check that the folder exists.
func Open(name string) Dir {
	return Dir{Name: name, Path: filepath.Join(Root(), name)}
}

// Paths inside a run folder. Methods rather than constants so callers cannot
// assemble one by hand and drift.
func (d Dir) TaskFile() string   { return filepath.Join(d.Path, "task.md") }
func (d Dir) StateFile() string  { return filepath.Join(d.Path, "state.json") }
func (d Dir) NotesFile() string  { return filepath.Join(d.Path, "notes.md") }
func (d Dir) LogFile() string    { return filepath.Join(d.Path, "log.md") }
func (d Dir) StopFile() string   { return filepath.Join(d.Path, "STOP") }
func (d Dir) ReviewsDir() string { return filepath.Join(d.Path, "reviews") }
func (d Dir) SessionsDir() string {
	return filepath.Join(d.Path, "sessions")
}

// ReviewFile names the review artifact for an iteration. Zero-padded so that
// ls and any future reader sort them the way they were written; unpadded,
// "10.md" sorts before "2.md".
func (d Dir) ReviewFile(n int) string {
	return filepath.Join(d.ReviewsDir(), fmt.Sprintf("%03d.md", n))
}

// Exists reports whether this run has been created.
func (d Dir) Exists() bool {
	_, err := os.Stat(d.TaskFile())
	return err == nil
}

// Create lays out a new run folder. It fails if one is already there rather
// than merging, because the failure it is guarding against is a second run
// silently inheriting the first one's history.
func (d Dir) Create(cwd, agentName, goal string) error {
	if d.Exists() {
		return fmt.Errorf("run %q already exists at %s", d.Name, d.Path)
	}
	for _, dir := range []string{d.ReviewsDir(), d.SessionsDir()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if goal == "" {
		goal = UnfilledGoal
	}
	if err := os.WriteFile(d.TaskFile(), []byte(renderTask(d.Name, goal)), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(d.LogFile(), []byte("# Log: "+d.Name+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(d.NotesFile(), nil, 0o644); err != nil {
		return err
	}
	now := time.Now().UTC()
	return d.WriteState(&State{
		Name:      d.Name,
		Cwd:       cwd,
		Agent:     agentName,
		Status:    StatusIdle,
		Verdict:   VerdictUnknown,
		History:   []Iteration{},
		CreatedAt: now,
		UpdatedAt: now,
	})
}

// ReadState loads state.json.
func (d Dir) ReadState() (*State, error) {
	data, err := os.ReadFile(d.StateFile())
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("run %q has damaged state.json: %w", d.Name, err)
	}
	if s.History == nil {
		s.History = []Iteration{}
	}
	return &s, nil
}

// WriteState persists state.json atomically.
//
// Atomicity is load-bearing here, not a nicety. The dashboard polls this file
// every few seconds from another process; a plain truncate-and-write leaves a
// window in which it reads a half-written file and reports the run as damaged.
// Writing a temp file and renaming makes the swap atomic on POSIX, so a reader
// always sees one whole version or the other.
func (d Dir) WriteState(s *State) error {
	s.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(d.Path, ".state-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		os.Remove(name)
		return err
	}
	return os.Rename(name, d.StateFile())
}

// Task reads task.md. It is re-read at the top of every iteration, which is
// what makes editing it mid-run the way to change a run's course.
func (d Dir) Task() (string, error) {
	data, err := os.ReadFile(d.TaskFile())
	return string(data), err
}

// Runnable reports whether this task.md is worth spending a turn on.
//
// Separate from starting a run so it can be tested without going near an
// agent. The placeholder half is the load-bearing one; see UnfilledGoal.
func Runnable(task string) bool {
	return strings.TrimSpace(task) != "" && !strings.Contains(task, UnfilledGoal)
}

// AppendLog adds a timestamped line to log.md, the durable record of what the
// harness itself did. Failures are returned but callers generally ignore them:
// losing a log line must not end a run that is otherwise working.
func (d Dir) AppendLog(format string, args ...any) error {
	f, err := os.OpenFile(d.LogFile(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	line := fmt.Sprintf("\n- %s — %s\n", time.Now().UTC().Format(time.RFC3339), fmt.Sprintf(format, args...))
	_, err = f.WriteString(line)
	return err
}

// WriteReview stores the reviewer's reply for an iteration. Never parsed back;
// it exists to be read by a human working out why a run went the way it did.
func (d Dir) WriteReview(n int, header, body string) error {
	if err := os.MkdirAll(d.ReviewsDir(), 0o755); err != nil {
		return err
	}
	content := fmt.Sprintf("# Iteration %d — %s\n\n%s\n\n---\n\n%s\n",
		n, time.Now().UTC().Format(time.RFC3339), header, body)
	return os.WriteFile(d.ReviewFile(n), []byte(content), 0o644)
}

// StopRequested reports whether a stop was asked for, consuming the request.
//
// Consuming it here means a stop applies exactly once: the run halts, and a
// later start of the same run is not immediately cancelled by a leftover file.
func (d Dir) StopRequested() bool {
	if _, err := os.Stat(d.StopFile()); err != nil {
		return false
	}
	os.Remove(d.StopFile())
	return true
}

// RequestStop asks a running harness to halt after the current iteration.
func (d Dir) RequestStop() error {
	return os.WriteFile(d.StopFile(), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644)
}

// List returns every run, newest first.
//
// A run whose state.json is missing or damaged is skipped rather than failing
// the listing: one broken folder must not blind the dashboard to the others.
func List() ([]*State, error) {
	entries, err := os.ReadDir(Root())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []*State
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		s, err := Open(e.Name()).ReadState()
		if err != nil {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

func renderTask(name, goal string) string {
	return `# Task: ` + name + `

## Goal

` + goal + `

## Done when

An independent reviewer, reading the diff and running this project's own build
and tests, agrees the work is genuinely complete rather than merely plausible.

If there is a specific command that proves it — a test suite, a build, a lint —
name it here and the reviewer will use it.

## Constraints

- Stay within the goal above. Do not refactor code it did not ask about.
- Leave the working tree in a state that builds.

## Notes

(Add detail before starting — the more precise this file is, the less the run
drifts. It is re-read at the top of every iteration.)
`
}
