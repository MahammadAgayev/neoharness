package run

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// harnessHome points Root() at a temp dir for the duration of a test, so no
// test can touch a real run folder.
func harnessHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HARNESS_HOME", dir)
	return dir
}

func TestRootHonoursEnv(t *testing.T) {
	dir := harnessHome(t)
	if Root() != dir {
		t.Fatalf("Root() = %q, want %q", Root(), dir)
	}
}

func TestCreateLaysOutFolder(t *testing.T) {
	harnessHome(t)
	d := Open("demo")
	if err := d.Create("/some/tree", "pi", "", ""); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{d.TaskFile(), d.StateFile(), d.LogFile(), d.NotesFile()} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s to exist: %v", filepath.Base(p), err)
		}
	}
	for _, p := range []string{d.ReviewsDir(), d.SessionsDir()} {
		fi, err := os.Stat(p)
		if err != nil || !fi.IsDir() {
			t.Errorf("expected %s to be a directory", filepath.Base(p))
		}
	}
}

func TestCreateRefusesToClobber(t *testing.T) {
	harnessHome(t)
	d := Open("demo")
	if err := d.Create("/tree", "pi", "", ""); err != nil {
		t.Fatal(err)
	}
	// The failure being guarded against is a second run silently inheriting the
	// first one's history, so a re-create must be an error, not a merge.
	if err := d.Create("/tree", "pi", "", ""); err == nil {
		t.Fatal("expected an error creating over an existing run")
	}
}

func TestCreateWritesUnfilledGoalByDefault(t *testing.T) {
	harnessHome(t)
	d := Open("demo")
	if err := d.Create("/tree", "pi", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := d.Task()
	if err != nil {
		t.Fatal(err)
	}
	// init and start must agree on the exact placeholder string, otherwise a
	// template that was never edited gets handed to an agent as the task.
	if !strings.Contains(task, UnfilledGoal) {
		t.Error("a default task.md must contain the placeholder that start refuses")
	}
	if Runnable(task) {
		t.Error("a default task.md must not be runnable")
	}
}

func TestCreateWithGoalIsRunnable(t *testing.T) {
	harnessHome(t)
	d := Open("demo")
	if err := d.Create("/tree", "pi", "", "Make the build green."); err != nil {
		t.Fatal(err)
	}
	task, err := d.Task()
	if err != nil {
		t.Fatal(err)
	}
	if !Runnable(task) {
		t.Error("a task.md created with a real goal must be runnable")
	}
	if !strings.Contains(task, "Make the build green.") {
		t.Error("the supplied goal should appear in task.md")
	}
}

func TestRunnable(t *testing.T) {
	for _, tc := range []struct {
		name string
		task string
		want bool
	}{
		{"empty", "", false},
		{"whitespace only", "   \n\t\n  ", false},
		{"untouched template", "# Task\n\n## Goal\n\n" + UnfilledGoal + "\n", false},
		{"placeholder buried in a filled file", "# Task\n\nreal words\n" + UnfilledGoal, false},
		{"real task", "# Task\n\n## Goal\n\nMake the tests pass.\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Runnable(tc.task); got != tc.want {
				t.Errorf("Runnable(%q) = %v, want %v", tc.task, got, tc.want)
			}
		})
	}
}

func TestStateRoundTrip(t *testing.T) {
	harnessHome(t)
	d := Open("demo")
	if err := d.Create("/tree", "pi", "", "goal"); err != nil {
		t.Fatal(err)
	}
	want := &State{
		Name:      "demo",
		Cwd:       "/tree",
		Agent:     "pi",
		Status:    StatusRunning,
		PID:       4242,
		Iteration: 3,
		Verdict:   VerdictNeedsWork,
		Findings:  "the retry branch is untested",
		History: []Iteration{
			{N: 1, Verdict: VerdictNeedsWork, At: time.Now().UTC().Truncate(time.Second), WorkerSeconds: 12, ReviewTools: 4},
		},
		CreatedAt: time.Now().UTC().Truncate(time.Second),
	}
	if err := d.WriteState(want); err != nil {
		t.Fatal(err)
	}
	got, err := d.ReadState()
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != want.Name || got.Cwd != want.Cwd || got.Agent != want.Agent {
		t.Errorf("identity fields did not survive: %+v", got)
	}
	if got.Status != want.Status || got.PID != want.PID || got.Iteration != want.Iteration {
		t.Errorf("progress fields did not survive: %+v", got)
	}
	if got.Verdict != want.Verdict || got.Findings != want.Findings {
		t.Errorf("verdict fields did not survive: %+v", got)
	}
	if len(got.History) != 1 || got.History[0].N != 1 || got.History[0].ReviewTools != 4 {
		t.Errorf("history did not survive: %+v", got.History)
	}
	if got.UpdatedAt.IsZero() {
		t.Error("WriteState should stamp UpdatedAt")
	}
}

func TestReadStateReportsDamage(t *testing.T) {
	harnessHome(t)
	d := Open("demo")
	if err := d.Create("/tree", "pi", "", "goal"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(d.StateFile(), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A damaged run must be distinguishable from a missing one, so the caller
	// can say so instead of reporting "no such run".
	_, err := d.ReadState()
	if err == nil {
		t.Fatal("expected an error on damaged state.json")
	}
	if !strings.Contains(err.Error(), "damaged") {
		t.Errorf("error should name the problem, got: %v", err)
	}
}

func TestWriteStateIsAtomicUnderConcurrentReads(t *testing.T) {
	harnessHome(t)
	d := Open("demo")
	if err := d.Create("/tree", "pi", "", "goal"); err != nil {
		t.Fatal(err)
	}
	s, err := d.ReadState()
	if err != nil {
		t.Fatal(err)
	}
	// `harness list` reads state.json from another process while the run writes
	// it. A non-atomic write leaves a window where the reader sees a truncated
	// file; this reproduces that race and asserts it never happens.
	var wg sync.WaitGroup
	stop := make(chan struct{})
	var readErr error
	var mu sync.Mutex
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				data, err := os.ReadFile(d.StateFile())
				if err != nil {
					continue // a rename window is allowed to miss the file entirely
				}
				var probe State
				if err := json.Unmarshal(data, &probe); err != nil {
					mu.Lock()
					readErr = err
					mu.Unlock()
					return
				}
			}
		}()
	}
	for i := 0; i < 200; i++ {
		s.Iteration = i
		s.Findings = strings.Repeat("x", i*40) // vary the size to widen the window
		if err := d.WriteState(s); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if readErr != nil {
		t.Fatalf("a concurrent reader saw a partially written state.json: %v", readErr)
	}
}

func TestWriteStateLeavesNoTempFiles(t *testing.T) {
	harnessHome(t)
	d := Open("demo")
	if err := d.Create("/tree", "pi", "", "goal"); err != nil {
		t.Fatal(err)
	}
	s, _ := d.ReadState()
	for i := 0; i < 5; i++ {
		if err := d.WriteState(s); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(d.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".state-") {
			t.Errorf("temp file %s was left behind", e.Name())
		}
	}
}

func TestStopRequestedConsumesTheRequest(t *testing.T) {
	harnessHome(t)
	d := Open("demo")
	if err := d.Create("/tree", "pi", "", "goal"); err != nil {
		t.Fatal(err)
	}
	if d.StopRequested() {
		t.Fatal("a fresh run must not report a stop")
	}
	if err := d.RequestStop(); err != nil {
		t.Fatal(err)
	}
	if !d.StopRequested() {
		t.Fatal("expected the stop to be seen")
	}
	// Consuming it is what stops a leftover file from immediately cancelling
	// the next start of the same run.
	if d.StopRequested() {
		t.Fatal("a stop must apply exactly once")
	}
}

func TestReviewFileIsZeroPadded(t *testing.T) {
	harnessHome(t)
	d := Open("demo")
	// Unpadded, "10.md" sorts before "2.md" and the readable record reads out
	// of order.
	if got := filepath.Base(d.ReviewFile(2)); got != "002.md" {
		t.Errorf("ReviewFile(2) = %q, want 002.md", got)
	}
	if got := filepath.Base(d.ReviewFile(10)); got != "010.md" {
		t.Errorf("ReviewFile(10) = %q, want 010.md", got)
	}
}

func TestWriteReview(t *testing.T) {
	harnessHome(t)
	d := Open("demo")
	if err := d.Create("/tree", "pi", "", "goal"); err != nil {
		t.Fatal(err)
	}
	if err := d.WriteReview(1, "12s, 4 tool calls", "Looks good.\n\nPASS"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(d.ReviewFile(1))
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	for _, want := range []string{"Iteration 1", "12s, 4 tool calls", "PASS"} {
		if !strings.Contains(body, want) {
			t.Errorf("review file missing %q:\n%s", want, body)
		}
	}
}

func TestAppendLog(t *testing.T) {
	harnessHome(t)
	d := Open("demo")
	if err := d.Create("/tree", "pi", "", "goal"); err != nil {
		t.Fatal(err)
	}
	if err := d.AppendLog("Iteration %d: %s", 1, "PASS"); err != nil {
		t.Fatal(err)
	}
	if err := d.AppendLog("Run ended"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(d.LogFile())
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if !strings.Contains(body, "Iteration 1: PASS") || !strings.Contains(body, "Run ended") {
		t.Errorf("log.md should accumulate both lines:\n%s", body)
	}
	if !strings.Contains(body, "# Log: demo") {
		t.Error("appending must not clobber the log header")
	}
}

func TestListSkipsDamagedAndSortsNewestFirst(t *testing.T) {
	home := harnessHome(t)

	older := Open("older")
	if err := older.Create("/tree", "pi", "", "goal"); err != nil {
		t.Fatal(err)
	}
	// WriteState stamps UpdatedAt from the clock, so make the gap unambiguous.
	time.Sleep(10 * time.Millisecond)
	newer := Open("newer")
	if err := newer.Create("/tree", "pi", "", "goal"); err != nil {
		t.Fatal(err)
	}

	// One broken folder must not hide the others.
	broken := filepath.Join(home, "broken")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "state.json"), []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A stray file at the root is not a run either.
	if err := os.WriteFile(filepath.Join(home, "stray.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 listable runs, got %d", len(got))
	}
	if got[0].Name != "newer" || got[1].Name != "older" {
		t.Errorf("expected newest first, got %s then %s", got[0].Name, got[1].Name)
	}
}

func TestListOnMissingRootIsEmpty(t *testing.T) {
	t.Setenv("HARNESS_HOME", filepath.Join(t.TempDir(), "does-not-exist"))
	got, err := List()
	if err != nil {
		t.Fatalf("a missing root is not an error, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no runs, got %d", len(got))
	}
}

func TestExists(t *testing.T) {
	harnessHome(t)
	d := Open("demo")
	if d.Exists() {
		t.Fatal("a run that was never created must not exist")
	}
	if err := d.Create("/tree", "pi", "", "goal"); err != nil {
		t.Fatal(err)
	}
	if !d.Exists() {
		t.Fatal("a created run must exist")
	}
}
