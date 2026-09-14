package claude

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
// `claude -p --output-format stream-json` invocation. Recorded rather than
// invented so that a change to Claude's wire format fails here instead of
// quietly producing a display with nothing in it.
func TestRealRunFixture(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "real-run.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var got []agent.Event
	acc := &accumulator{onEvent: func(e agent.Event) { got = append(got, e) }}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		acc.consume(sc.Bytes())
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}

	// The captured run read one file and answered with its contents.
	if acc.tools != 1 {
		t.Errorf("tools = %d, want 1", acc.tools)
	}
	if acc.text != "pineapple" {
		t.Errorf("text = %q, want the result record's answer", acc.text)
	}
	if acc.isError {
		t.Error("isError = true, want false for a clean run")
	}
	// Cache reads dominate this capture, which is the point of summing the
	// components rather than reading input+output alone.
	if acc.tokens != 81422 {
		t.Errorf("tokens = %d, want 81422 (the highest total in the stream)", acc.tokens)
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
	if tools != 1 {
		t.Errorf("emitted %d tool events, want 1", tools)
	}
	if turns != 1 {
		t.Errorf("emitted %d turn events, want 1 — the result record ends the turn", turns)
	}
	// The six `system` and one `user` records are machinery and tool results;
	// surfacing them is the firehose this design exists to avoid.
	if texts+tools+turns != len(got) {
		t.Errorf("emitted %d events, more than the %d accounted for", len(got), texts+tools+turns)
	}
}

// TestResultRecordWinsOverTheLastAssistantMessage pins the rule that makes a
// verdict trustworthy: the CLI's own final answer is authoritative.
func TestResultRecordWinsOverTheLastAssistantMessage(t *testing.T) {
	acc, _ := feed(t, []string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Let me check the tests."}]}}`,
		`{"type":"result","result":"Ran the suite, all green.\n\nPASS","is_error":false}`,
	})
	if !strings.Contains(acc.text, "PASS") {
		t.Fatalf("text = %q, want the result record", acc.text)
	}
}

// TestTrailingToolCallDoesNotBlankTheVerdict is the expensive one, and the
// reason prose and tool_use are accumulated separately. A tool-call-only
// assistant message arriving after the verdict must not empty it: the loop
// reads empty text as "the reviewer could not be run" and spends a third of
// its error budget on a review that actually succeeded.
func TestTrailingToolCallDoesNotBlankTheVerdict(t *testing.T) {
	acc, _ := feed(t, []string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Checked the diff.\n\nPASS"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/tmp/f"}}]}}`,
	})
	if !strings.Contains(acc.text, "PASS") {
		t.Fatalf("verdict was lost, text = %q", acc.text)
	}
}

// TestEmptyResultDoesNotOverwrite guards the same property from the other
// side: a result record with nothing in it must leave real prose standing.
func TestEmptyResultDoesNotOverwrite(t *testing.T) {
	acc, _ := feed(t, []string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"NEEDS_WORK"}]}}`,
		`{"type":"result","result":"   \n ","is_error":false}`,
	})
	if acc.text != "NEEDS_WORK" {
		t.Errorf("text = %q, want the real prose preserved", acc.text)
	}
}

// TestIsErrorIsRecorded covers the case pi's protocol does not have: Claude
// reports a failed turn in the result record while still exiting zero.
func TestIsErrorIsRecorded(t *testing.T) {
	acc, _ := feed(t, []string{
		`{"type":"result","result":"hit the turn limit","is_error":true}`,
	})
	if !acc.isError {
		t.Error("isError = false, want true — a failed turn must not be parsed for a verdict")
	}
}

func TestToolDetailIsTheFirstStringArgument(t *testing.T) {
	_, got := feed(t, []string{
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/tmp/p"}}]}}`,
	})
	if len(got) != 1 || got[0].Tool != "Read" || got[0].Detail != "/tmp/p" {
		t.Fatalf("got %+v", got)
	}
}

func TestToolDetailSkipsNonStringArguments(t *testing.T) {
	_, got := feed(t, []string{
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"offset":10,"limit":5,"file_path":"/tmp/f.go"}}]}}`,
	})
	if got[0].Detail != "/tmp/f.go" {
		t.Errorf("Detail = %q, want the first string argument", got[0].Detail)
	}
}

func TestToolDetailCollapsesNewlines(t *testing.T) {
	// A bash heredoc must not turn one tool call into thirty display lines.
	_, got := feed(t, []string{
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"cat <<EOF\nline one\nline two\nEOF"}}]}}`,
	})
	if got[0].Detail != "cat <<EOF line one line two EOF" {
		t.Errorf("Detail = %q, want one flattened line", got[0].Detail)
	}
}

func TestTokensAreMonotonic(t *testing.T) {
	// Usage is reported at unpredictable points and a later record may carry
	// less; a lower number must not walk the count back.
	acc, _ := feed(t, []string{
		`{"type":"assistant","message":{"content":[],"usage":{"input_tokens":400,"output_tokens":100}}}`,
		`{"type":"assistant","message":{"content":[],"usage":{"input_tokens":10,"output_tokens":5}}}`,
	})
	if acc.tokens != 500 {
		t.Errorf("tokens = %d, want 500", acc.tokens)
	}
}

func TestTokensIncludeCache(t *testing.T) {
	// Cache reads are real context the run paid to carry, and on a long run
	// they are most of it.
	acc, _ := feed(t, []string{
		`{"type":"result","result":"done","usage":{"input_tokens":1,"output_tokens":2,"cache_creation_input_tokens":30,"cache_read_input_tokens":400}}`,
	})
	if acc.tokens != 433 {
		t.Errorf("tokens = %d, want 433", acc.tokens)
	}
}

func TestTruncatedLineIsSkipped(t *testing.T) {
	// A process killed mid-write leaves a partial final line.
	acc, got := feed(t, []string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"good"}]}}`,
		`{"type":"assistant","mess`,
	})
	if acc.text != "good" {
		t.Errorf("text = %q, want the complete line's prose", acc.text)
	}
	if len(got) != 1 {
		t.Errorf("emitted %d events, want 1 — a truncated line is not an event", len(got))
	}
}

func TestUserAndSystemRecordsAreIgnored(t *testing.T) {
	// Tool results come back as `user` records. One carrying the word PASS must
	// never become the verdict.
	acc, got := feed(t, []string{
		`{"type":"system","subtype":"init"}`,
		`{"type":"user","message":{"content":[{"type":"text","text":"PASS"}]}}`,
	})
	if acc.text != "" {
		t.Errorf("text = %q, want empty — only assistant prose and the result count", acc.text)
	}
	if len(got) != 0 {
		t.Errorf("emitted %d events, want none", len(got))
	}
}

// --- command line ---

func TestArgsCarriesPromptLastAndUnattendedFlags(t *testing.T) {
	a := New()
	got := a.args(agent.Request{Prompt: "-do the thing", Tools: []string{"read", "bash"}})

	if got[0] != "claude" {
		t.Errorf("args[0] = %q, want the default binary", got[0])
	}
	// The prompt must be the value of a trailing -p, or a prompt starting with
	// a dash is read as a flag.
	if n := len(got); got[n-2] != "-p" || got[n-1] != "-do the thing" {
		t.Errorf("args tail = %q, want -p followed by the prompt", got[n-2:])
	}
	joined := strings.Join(got, " ")
	for _, want := range []string{
		"--output-format stream-json",
		"--verbose",
		"--permission-mode bypassPermissions",
		"--allowedTools Read,Bash",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q, got %q", want, joined)
		}
	}
}

// TestReviewerCannotWrite is the security-shaped one.
//
// --allowedTools only auto-approves; it does not refuse what is missing, and
// --permission-mode bypassPermissions removes the prompt that would otherwise
// have stopped an unlisted tool. Verified against the real CLI: with only
// Read,Bash allowed, "overwrite this file with the Write tool" still
// overwrote it. The loop withholds edit/write from the reviewer precisely so
// it cannot fix what it is judging, so the denial has to be explicit.
func TestReviewerCannotWrite(t *testing.T) {
	// The loop's ReviewTools, in Claude's vocabulary.
	got := strings.Join(New().args(agent.Request{
		Prompt: "review it",
		Tools:  []string{"read", "grep", "find", "ls", "bash"},
	}), " ")
	if !strings.Contains(got, "--disallowedTools") {
		t.Fatalf("args = %q, want the mutating tools denied", got)
	}
	for _, tool := range mutatingTools {
		if !strings.Contains(got, tool) {
			t.Errorf("args = %q, want %s denied", got, tool)
		}
	}
}

// TestWorkerKeepsItsTools is the other half: the worker asks for edit and
// write, so those two must not be denied to it. NotebookEdit still is — it was
// never requested, and the loop's vocabulary has no name for it.
func TestWorkerKeepsItsTools(t *testing.T) {
	args := New().args(agent.Request{
		Prompt: "do it",
		Tools:  []string{"read", "bash", "edit", "write", "grep", "find", "ls"},
	})
	var blocked string
	for i, a := range args {
		if a == "--disallowedTools" && i+1 < len(args) {
			blocked = args[i+1]
		}
	}
	// Split rather than substring-matched: "NotebookEdit" contains "Edit".
	for _, tool := range strings.Split(blocked, ",") {
		if tool == "Write" || tool == "Edit" {
			t.Errorf("denied %q, but the worker asked for %s", blocked, tool)
		}
	}
}

func TestDeniedOnlyListsWhatWasNotAllowed(t *testing.T) {
	got := denied([]string{"Read", "Write"})
	if len(got) != 2 || got[0] != "Edit" || got[1] != "NotebookEdit" {
		t.Errorf("denied = %q, want the mutating tools that were not allowed", got)
	}
	if len(denied(mutatingTools)) != 0 {
		t.Error("denied should be empty when every mutating tool was allowed")
	}
}

func TestArgsUsesTheConfiguredCommand(t *testing.T) {
	// The deployment this exists for: claude reached through a wrapper.
	a := New("authwrap", "agent", "run", "claude")
	got := a.args(agent.Request{Prompt: "hi"})
	if strings.Join(got[:4], " ") != "authwrap agent run claude" {
		t.Errorf("args prefix = %q, want the configured command", got[:4])
	}
}

func TestArgsOmitsEmptyOptionals(t *testing.T) {
	got := strings.Join(New().args(agent.Request{Prompt: "hi"}), " ")
	for _, unwanted := range []string{"--model", "--append-system-prompt", "--allowedTools", "--session-id"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("args should omit %s when unset, got %q", unwanted, got)
		}
	}
}

func TestArgsAppendsRatherThanReplacesTheSystemPrompt(t *testing.T) {
	// The evaluator persona is an overlay on a working coding agent, not a
	// replacement for one: --system-prompt here would discard Claude's own.
	got := strings.Join(New().args(agent.Request{Prompt: "hi", SystemPrompt: "be strict"}), " ")
	if !strings.Contains(got, "--append-system-prompt be strict") {
		t.Errorf("args = %q, want --append-system-prompt", got)
	}
	if strings.Contains(got, "--system-prompt be strict") {
		t.Error("the CLI's own system prompt must not be replaced")
	}
}

// --- sessions ---

// TestArgsIgnoresTheSessionFields pins a deliberate difference from the pi
// adapter, and a bug that killed a run on iteration one.
//
// pi's model is that the caller names a session and the CLI files the
// transcript under it. Claude owns its own session store and refuses a
// --session-id that already exists, so deriving a stable id from the run name
// meant the second run of a given name could never start. Each `-p` is a fresh
// session regardless, which is what the loop wants.
func TestArgsIgnoresTheSessionFields(t *testing.T) {
	got := strings.Join(New().args(agent.Request{
		Prompt:     "hi",
		SessionID:  "demo-003",
		SessionDir: "/tmp/sessions",
	}), " ")
	if strings.Contains(got, "--session-id") || strings.Contains(got, "--session-dir") {
		t.Errorf("args = %q, want no session flags", got)
	}
	if strings.Contains(got, "demo-003") || strings.Contains(got, "/tmp/sessions") {
		t.Errorf("args = %q, want the session fields unused", got)
	}
}

// --- models ---

// TestMapModelTranslatesTheEvaluatorModel guards a bug that cost a whole run.
// evaluator.md pins "claude-sonnet-5-thinking", which is pi's name for it;
// Claude answers an unknown model with a 404 in a result record that still
// exits zero, so the reviewer measured nothing three times and the run ended
// "the reviewer could not be dispatched".
func TestMapModelTranslatesTheEvaluatorModel(t *testing.T) {
	for in, want := range map[string]string{
		"claude-sonnet-5-thinking": "sonnet",
		"claude-opus-4":            "opus",
	} {
		if got := mapModel(in); got != want {
			t.Errorf("mapModel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMapModelPassesOtherNamesThrough(t *testing.T) {
	// A family alias is what the CLI documents, and a genuinely wrong name
	// should still reach it and fail loudly rather than be rewritten here.
	for _, name := range []string{"sonnet", "opus", "claude-3-5-haiku-20241022"} {
		if got := mapModel(name); got != name {
			t.Errorf("mapModel(%q) = %q, want it unchanged", name, got)
		}
	}
}

func TestArgsTranslatesTheModel(t *testing.T) {
	got := strings.Join(New().args(agent.Request{Prompt: "hi", Model: "claude-sonnet-5-thinking"}), " ")
	if !strings.Contains(got, "--model sonnet") {
		t.Errorf("args = %q, want --model sonnet", got)
	}
}

// --- tools ---

func TestMapToolsTranslatesTheHarnessVocabulary(t *testing.T) {
	got := mapTools([]string{"read", "write", "edit", "bash", "grep", "find", "ls"})
	want := []string{"Read", "Write", "Edit", "Bash", "Grep", "Glob"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("mapTools = %q, want %q", got, want)
	}
}

// TestMapToolsDeduplicates: find and ls both land on Glob, and a repeated
// name in --allowedTools is noise at best.
func TestMapToolsDeduplicates(t *testing.T) {
	if got := mapTools([]string{"find", "ls"}); len(got) != 1 || got[0] != "Glob" {
		t.Errorf("mapTools = %q, want [Glob] once", got)
	}
}

func TestMapToolsPassesUnknownNamesThrough(t *testing.T) {
	// So a Claude-specific tool can still be requested.
	if got := mapTools([]string{"WebFetch"}); len(got) != 1 || got[0] != "WebFetch" {
		t.Errorf("mapTools = %q, want [WebFetch]", got)
	}
}

func TestTrimKeepsTheTail(t *testing.T) {
	// The error is at the end of a stderr dump, not the start.
	if got := trim("aaaabcde", 4); got != "bcde" {
		t.Errorf("trim = %q, want the last 4 chars", got)
	}
	if got := trim("  hi  ", 100); got != "hi" {
		t.Errorf("trim = %q, want trimmed", got)
	}
}
