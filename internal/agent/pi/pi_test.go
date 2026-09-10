package pi

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MahammadAgayev/harness/internal/agent"
)

// feed replays a stream of NDJSON lines through the accumulator, the way the
// read loop does.
func feed(t *testing.T, lines []string) (*accumulator, []agent.Event) {
	t.Helper()
	var got []agent.Event
	acc := &accumulator{onEvent: func(e agent.Event) { got = append(got, e) }}
	for _, l := range lines {
		acc.consume([]byte(l))
	}
	return acc, got
}

// TestRealRunFixture parses a stream captured from an actual
// `pi -p --mode json` invocation. The fixture is recorded rather than invented
// precisely so that a change to pi's wire format fails here instead of quietly
// producing a display with nothing in it.
func TestRealRunFixture(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "real-run.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var got []agent.Event
	acc := &accumulator{onEvent: func(e agent.Event) { got = append(got, e) }}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		acc.consume(sc.Bytes())
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}

	// The captured run listed a directory and read a file.
	if acc.tools != 2 {
		t.Errorf("tools = %d, want 2", acc.tools)
	}
	if acc.tokens != 3509 {
		t.Errorf("tokens = %d, want 3509 (the highest cumulative total in the stream)", acc.tokens)
	}
	if !strings.Contains(acc.text, "hello world") {
		t.Errorf("final text should be the last assistant prose, got %q", acc.text)
	}

	var tools, texts, turns int
	for _, e := range got {
		switch e.Kind {
		case agent.KindToolStart:
			tools++
		case agent.KindAssistantText:
			texts++
		case agent.KindTurnEnd:
			turns++
		}
	}
	if tools != 2 {
		t.Errorf("emitted %d tool events, want 2", tools)
	}
	// Two assistant messages in the capture: the pre-tool sentence and the
	// final answer.
	if texts != 2 {
		t.Errorf("emitted %d text events, want 2", texts)
	}
	if turns != 2 {
		t.Errorf("emitted %d turn events, want 2", turns)
	}

	// 38 of the 60 lines are message_update. Emitting those would be one event
	// per token, which is the display flood this design exists to avoid.
	if len(got) != 6 {
		t.Errorf("emitted %d events from a 60-line stream, want 6 — token deltas must be dropped", len(got))
	}
}

func TestToolDetailIsTheFirstStringArgument(t *testing.T) {
	_, got := feed(t, []string{
		`{"type":"tool_execution_start","toolName":"ls","args":{"path":"/tmp/p"}}`,
	})
	if len(got) != 1 || got[0].Tool != "ls" || got[0].Detail != "/tmp/p" {
		t.Fatalf("got %+v", got)
	}
}

func TestToolDetailSkipsNonStringArguments(t *testing.T) {
	// `read` takes a path plus numeric offsets; the path is the useful part and
	// it is not the first key.
	_, got := feed(t, []string{
		`{"type":"tool_execution_start","toolName":"read","args":{"offset":10,"limit":5,"path":"/tmp/f.go"}}`,
	})
	if got[0].Detail != "/tmp/f.go" {
		t.Errorf("Detail = %q, want the first string argument", got[0].Detail)
	}
}

func TestToolDetailCollapsesNewlines(t *testing.T) {
	// A bash heredoc must not turn one tool call into thirty display lines.
	_, got := feed(t, []string{
		`{"type":"tool_execution_start","toolName":"bash","args":{"command":"cat <<EOF\nline one\nline two\nEOF"}}`,
	})
	if strings.Contains(got[0].Detail, "\n") {
		t.Errorf("Detail must be one line, got %q", got[0].Detail)
	}
	if got[0].Detail != "cat <<EOF line one line two EOF" {
		t.Errorf("Detail = %q", got[0].Detail)
	}
}

func TestToolDetailAbsentIsEmpty(t *testing.T) {
	for _, line := range []string{
		`{"type":"tool_execution_start","toolName":"ls","args":{}}`,
		`{"type":"tool_execution_start","toolName":"ls"}`,
		`{"type":"tool_execution_start","toolName":"ls","args":{"n":3,"deep":true}}`,
	} {
		_, got := feed(t, []string{line})
		if len(got) != 1 {
			t.Fatalf("expected one event for %s", line)
		}
		if got[0].Detail != "" {
			t.Errorf("Detail = %q, want empty for %s", got[0].Detail, line)
		}
		if got[0].Tool != "ls" {
			t.Errorf("Tool = %q, want ls", got[0].Tool)
		}
	}
}

func TestTrailingToolCallMessageDoesNotBlankTheVerdict(t *testing.T) {
	// This is the expensive one. The reviewer's verdict arrives as prose; if a
	// later tool-call-only assistant message overwrote it, the loop would read
	// an empty reply as "the reviewer could not be run" and burn its error
	// budget on a review that actually succeeded.
	acc, _ := feed(t, []string{
		`{"type":"message_end","message":{"role":"assistant","usage":{"totalTokens":100},"content":[{"type":"text","text":"Checked the diff and ran the tests.\n\nPASS"}]}}`,
		`{"type":"message_end","message":{"role":"assistant","usage":{"totalTokens":120},"content":[{"type":"toolCall"}]}}`,
	})
	if !strings.Contains(acc.text, "PASS") {
		t.Fatalf("verdict was lost, text = %q", acc.text)
	}
}

func TestWhitespaceOnlyTextDoesNotOverwrite(t *testing.T) {
	acc, _ := feed(t, []string{
		`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"NEEDS_WORK"}]}}`,
		`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"   \n  "}]}}`,
	})
	if acc.text != "NEEDS_WORK" {
		t.Errorf("text = %q, want the real prose preserved", acc.text)
	}
}

func TestNonAssistantMessagesAreNotText(t *testing.T) {
	// A tool result carrying the word PASS must never become the verdict.
	acc, got := feed(t, []string{
		`{"type":"message_end","message":{"role":"user","content":[{"type":"text","text":"do the thing"}]}}`,
		`{"type":"message_end","message":{"role":"toolResult","content":[{"type":"text","text":"PASS"}]}}`,
	})
	if acc.text != "" {
		t.Errorf("text = %q, want empty — only assistant prose counts", acc.text)
	}
	if len(got) != 0 {
		t.Errorf("emitted %d events, want none", len(got))
	}
}

func TestTokensAreMonotonic(t *testing.T) {
	// Providers report cumulative usage at unpredictable points and some report
	// zero until the end; a lower later number must not walk the count back.
	acc, _ := feed(t, []string{
		`{"type":"message_end","message":{"role":"assistant","usage":{"totalTokens":500},"content":[{"type":"text","text":"a"}]}}`,
		`{"type":"message_end","message":{"role":"assistant","usage":{"totalTokens":0},"content":[{"type":"text","text":"b"}]}}`,
		`{"type":"message_end","message":{"role":"assistant","usage":{"totalTokens":300},"content":[{"type":"text","text":"c"}]}}`,
	})
	if acc.tokens != 500 {
		t.Errorf("tokens = %d, want 500", acc.tokens)
	}
}

func TestTokensCountedFromNonAssistantMessagesToo(t *testing.T) {
	acc, _ := feed(t, []string{
		`{"type":"message_end","message":{"role":"toolResult","usage":{"totalTokens":42},"content":[]}}`,
	})
	if acc.tokens != 42 {
		t.Errorf("tokens = %d, want 42", acc.tokens)
	}
}

func TestTruncatedAndJunkLinesAreIgnored(t *testing.T) {
	// A subprocess killed mid-write leaves a truncated final line; that is the
	// expected shape of a timeout, not a parse failure worth reporting.
	acc, got := feed(t, []string{
		`{"type":"tool_execution_start","toolName":"ls","args":{"path":"/a"}}`,
		`{"type":"message_end","message":{"role":"assist`,
		``,
		`not json at all`,
		`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}`,
	})
	if acc.tools != 1 {
		t.Errorf("tools = %d, want 1", acc.tools)
	}
	if acc.text != "done" {
		t.Errorf("text = %q, want done", acc.text)
	}
	if len(got) != 2 {
		t.Errorf("emitted %d events, want 2", len(got))
	}
}

func TestMessageUpdateIsDropped(t *testing.T) {
	_, got := feed(t, []string{
		`{"type":"message_update","usage":{},"assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"Hel"}}`,
		`{"type":"message_update","usage":{},"assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"lo"}}`,
	})
	if len(got) != 0 {
		t.Fatalf("token deltas must not be emitted, got %d events", len(got))
	}
}

func TestTurnEndCarriesNoText(t *testing.T) {
	// turn_end repeats the assistant message that message_end already carried.
	// Emitting its text would double every reply in the display.
	_, got := feed(t, []string{
		`{"type":"turn_end","message":{"role":"assistant","content":[{"type":"text","text":"the answer"}]},"toolResults":[]}`,
	})
	if len(got) != 1 || got[0].Kind != agent.KindTurnEnd {
		t.Fatalf("got %+v", got)
	}
	if got[0].Text != "" {
		t.Errorf("turn_end must not carry text, got %q", got[0].Text)
	}
}

func TestNilOnEventIsSafe(t *testing.T) {
	acc := &accumulator{}
	acc.consume([]byte(`{"type":"tool_execution_start","toolName":"ls","args":{"path":"/x"}}`))
	if acc.tools != 1 {
		t.Errorf("accounting must work without a listener")
	}
}

func TestArgsCommandLine(t *testing.T) {
	a := New()
	got := a.args(agent.Request{
		Prompt:     "do the thing",
		Cwd:        "/tree",
		SessionID:  "demo-001",
		SessionDir: "/runs/demo/sessions",
		Tools:      []string{"read", "bash"},
	})
	joined := strings.Join(got, " ")
	for _, want := range []string{
		"-p --mode json -ne", // JSON mode: the only way a run is watchable
		"--session-dir /runs/demo/sessions",
		"--session-id demo-001",
		"-t read,bash",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("command line missing %q:\n%s", want, joined)
		}
	}
	// The prompt must be last and behind `--`, so a prompt starting with a dash
	// is not parsed as a flag.
	if got[len(got)-1] != "do the thing" || got[len(got)-2] != "--" {
		t.Errorf("prompt must be the final argument after --, got %v", got[len(got)-3:])
	}
}

func TestArgsOmitsEmptyOptions(t *testing.T) {
	got := strings.Join(New().args(agent.Request{Prompt: "x"}), " ")
	for _, unwanted := range []string{"--model", "--system-prompt", "--session-id", "--session-dir", "-t "} {
		if strings.Contains(got, unwanted) {
			t.Errorf("unset option %q should not appear: %s", unwanted, got)
		}
	}
}

func TestArgsIncludesModelAndSystemPrompt(t *testing.T) {
	got := New().args(agent.Request{
		Prompt:       "judge it",
		Model:        "claude-sonnet-5-thinking",
		SystemPrompt: "You are a reviewer.",
	})
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "--model claude-sonnet-5-thinking") {
		t.Errorf("missing model: %s", joined)
	}
	if !strings.Contains(joined, "--system-prompt You are a reviewer.") {
		t.Errorf("missing system prompt: %s", joined)
	}
}

func TestCommandIsConfigurable(t *testing.T) {
	// pi is not always invoked as bare `pi`; a wrapper that injects credentials
	// is a normal way to run it, and the claude adapter needs the same.
	a := New("authwrap", "agent", "run", "pi")
	got := a.args(agent.Request{Prompt: "x"})
	if got[0] != "authwrap" || got[3] != "pi" {
		t.Errorf("wrapper command not preserved: %v", got[:4])
	}
}

func TestName(t *testing.T) {
	if New().Name() != "pi" {
		t.Error("Name must identify the adapter for state.json")
	}
}

// --- process-level behaviour, using a stub instead of pi ------------------

// stubAgent builds an Agent whose "pi" is a shell script we control, so the
// process handling can be tested without a model.
func stubAgent(t *testing.T, script string) *Agent {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-pi")
	body := "#!/bin/sh\n" + script + "\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return New(path)
}

func TestRunParsesStubStream(t *testing.T) {
	a := stubAgent(t, `
echo '{"type":"tool_execution_start","toolName":"ls","args":{"path":"/x"}}'
echo '{"type":"message_end","message":{"role":"assistant","usage":{"totalTokens":77},"content":[{"type":"text","text":"all done"}]}}'
`)
	var seen []agent.Event
	res, err := a.Run(context.Background(), agent.Request{
		Prompt:  "x",
		Cwd:     t.TempDir(),
		OnEvent: func(e agent.Event) { seen = append(seen, e) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK() {
		t.Fatalf("expected a usable result, got %+v", res)
	}
	if res.Text != "all done" || res.Tools != 1 || res.Tokens != 77 {
		t.Errorf("got %+v", res)
	}
	if len(seen) != 2 {
		t.Errorf("expected 2 forwarded events, got %d", len(seen))
	}
	if res.Duration <= 0 {
		t.Error("Duration should be measured")
	}
}

func TestRunReportsExitCodeAndStderr(t *testing.T) {
	a := stubAgent(t, `echo "auth failed" >&2; exit 3`)
	res, err := a.Run(context.Background(), agent.Request{Prompt: "x", Cwd: t.TempDir()})
	// A process that ran and failed is a Result, not an error: the loop
	// distinguishes those.
	if err != nil {
		t.Fatalf("a non-zero exit must not be an error: %v", err)
	}
	if res.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3", res.ExitCode)
	}
	if !strings.Contains(res.Stderr, "auth failed") {
		t.Errorf("Stderr = %q", res.Stderr)
	}
	if res.OK() {
		t.Error("a failed process must not be OK")
	}
}

func TestRunEmptyOutputIsNotOK(t *testing.T) {
	// An agent that said nothing measured nothing. For the reviewer especially,
	// silence must never be parsed into a verdict.
	a := stubAgent(t, `exit 0`)
	res, err := a.Run(context.Background(), agent.Request{Prompt: "x", Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("expected a clean exit, got %d", res.ExitCode)
	}
	if res.OK() {
		t.Error("a clean exit with no text must still not be OK")
	}
}

func TestRunMissingBinaryIsAnError(t *testing.T) {
	// Unlike a failed model, a missing binary cannot be reasoned about by the
	// loop and must surface as an error.
	a := New(filepath.Join(t.TempDir(), "definitely-not-here"))
	if _, err := a.Run(context.Background(), agent.Request{Prompt: "x", Cwd: t.TempDir()}); err == nil {
		t.Fatal("expected an error for a missing binary")
	}
}

func TestRunTimeoutKillsTheWholeProcessTree(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	// The child spawns a grandchild that holds stdout open and outlives it.
	// Killing only the direct child leaves the read loop blocked on that pipe —
	// the exact hang the process group exists to prevent. If the group kill
	// works, Run returns at the deadline rather than after the grandchild's
	// sleep.
	a := stubAgent(t, `
sh -c 'sleep 30' &
sleep 30
`)
	start := time.Now()
	res, err := a.Run(context.Background(), agent.Request{
		Prompt:  "x",
		Cwd:     t.TempDir(),
		Timeout: 300 * time.Millisecond,
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut {
		t.Error("TimedOut should be set")
	}
	if res.OK() {
		t.Error("a killed run must not be OK")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("Run took %s — the grandchild held the pipe open, so the group kill failed", elapsed)
	}
	if !strings.Contains(res.Stderr, "Killed after") {
		t.Errorf("Stderr should say it was killed, got %q", res.Stderr)
	}
}

func TestRunTimeoutNotTrippedByASilentButLiveProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	// A watchdog timer must fire on time whatever the child is doing. A deadline
	// checked per-event would never fire here, because the process emits nothing
	// between its first line and the end of the sleep.
	a := stubAgent(t, `
echo '{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"starting"}]}}'
sleep 30
`)
	start := time.Now()
	res, _ := a.Run(context.Background(), agent.Request{
		Prompt:  "x",
		Cwd:     t.TempDir(),
		Timeout: 300 * time.Millisecond,
	})
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("watchdog did not fire during silence: took %s", elapsed)
	}
	if !res.TimedOut {
		t.Error("TimedOut should be set")
	}
}

func TestRunHonoursContextCancellation(t *testing.T) {
	if testing.Short() {
		t.Skip("timing test")
	}
	a := stubAgent(t, `sleep 30`)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _ = a.Run(ctx, agent.Request{Prompt: "x", Cwd: t.TempDir()})
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("context cancellation ignored: took %s", elapsed)
	}
}

func TestRunDoesNotInheritStdin(t *testing.T) {
	// With an inherited pipe on stdin that nobody closes, `pi -p` waits on it
	// forever. The stub reads stdin; if it is /dev/null the read returns
	// immediately at EOF, and if it is an open pipe this test hangs.
	a := stubAgent(t, `
cat > /dev/null
echo '{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"read eof"}]}}'
`)
	done := make(chan struct{})
	var res agent.Result
	go func() {
		res, _ = a.Run(context.Background(), agent.Request{Prompt: "x", Cwd: t.TempDir()})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run blocked — stdin was not /dev/null")
	}
	if res.Text != "read eof" {
		t.Errorf("text = %q", res.Text)
	}
}

func TestRunHandlesLinesLargerThanTheDefaultScannerBuffer(t *testing.T) {
	// A large tool result makes a single line far longer than bufio's 64KB
	// default. Without a raised limit the scanner stops early and the verdict
	// on a later line is never seen — silently, with no error.
	big := strings.Repeat("x", 200_000)
	msg, err := json.Marshal(map[string]any{
		"type": "message_end",
		"message": map[string]any{
			"role":    "toolResult",
			"content": []map[string]string{{"type": "text", "text": big}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	script := "cat <<'JSONEOF'\n" + string(msg) + "\n" +
		`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"after the big line"}]}}` +
		"\nJSONEOF\n"
	a := stubAgent(t, script)
	res, err := a.Run(context.Background(), agent.Request{Prompt: "x", Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "after the big line" {
		t.Errorf("text = %q — a long line truncated the stream", res.Text)
	}
}

func TestRunUsesRequestCwd(t *testing.T) {
	dir := t.TempDir()
	a := stubAgent(t, `
printf '{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"%s"}]}}\n' "$(pwd)"
`)
	res, err := a.Run(context.Background(), agent.Request{Prompt: "x", Cwd: dir})
	if err != nil {
		t.Fatal(err)
	}
	// macOS reports /private/var for /var, so compare resolved paths.
	want, _ := filepath.EvalSymlinks(dir)
	got, _ := filepath.EvalSymlinks(strings.TrimSpace(res.Text))
	if got != want {
		t.Errorf("agent ran in %q, want %q", got, want)
	}
}

func TestTrimKeepsTheTail(t *testing.T) {
	// The end of stderr is where the actual error is; the start is startup
	// noise.
	if got := trim("aaaaBOOM", 4); got != "BOOM" {
		t.Errorf("trim = %q, want BOOM", got)
	}
	if got := trim("  short  ", 100); got != "short" {
		t.Errorf("trim = %q, want short", got)
	}
}

// TestAgainstRealPi runs the actual binary. It is skipped unless pi is on PATH,
// so the suite stays runnable on a machine without it, but when it does run it
// is the only check that the command line this adapter builds is one pi
// accepts — every other test here would pass just as well against a wrong flag.
func TestAgainstRealPi(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a model")
	}
	if _, err := exec.LookPath("pi"); err != nil {
		t.Skip("pi is not on PATH")
	}
	if os.Getenv("HARNESS_INTEGRATION") == "" {
		t.Skip("set HARNESS_INTEGRATION=1 to run tests that spend tokens")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("pineapple\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	var sawTool bool
	res, err := New().Run(ctx, agent.Request{
		Prompt:  "Read marker.txt in the current directory and reply with only the word it contains.",
		Cwd:     dir,
		Tools:   []string{"read", "ls"},
		Timeout: 3 * time.Minute,
		OnEvent: func(e agent.Event) {
			if e.Kind == agent.KindToolStart {
				sawTool = true
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK() {
		t.Fatalf("real pi run was not OK: %+v", res)
	}
	if !strings.Contains(strings.ToLower(res.Text), "pineapple") {
		t.Errorf("text = %q, expected it to contain the marker", res.Text)
	}
	if !sawTool {
		t.Error("expected at least one tool event from a real run")
	}
	if res.Tokens == 0 {
		t.Error("expected a non-zero token count from a real run")
	}
}
