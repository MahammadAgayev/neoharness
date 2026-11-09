package claude

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MahammadAgayev/neoharness/internal/agent"
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

// TestResultRecordWinsOverTheLastAssistantMessage: the CLI's own final answer
// is authoritative.
func TestResultRecordWinsOverTheLastAssistantMessage(t *testing.T) {
	acc, _ := feed(t, []string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Let me check the tests."}]}}`,
		`{"type":"result","result":"Ran the suite, all green.\n\nPASS","is_error":false}`,
	})
	if !strings.Contains(acc.text, "PASS") {
		t.Fatalf("text = %q, want the result record", acc.text)
	}
}

// TestTrailingToolCallDoesNotBlankTheReply is the reason prose and tool_use
// are accumulated separately: a tool-call-only assistant message arriving
// after the reply must not empty it.
func TestTrailingToolCallDoesNotBlankTheReply(t *testing.T) {
	acc, _ := feed(t, []string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Checked the diff.\n\nPASS"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/tmp/f"}}]}}`,
	})
	if !strings.Contains(acc.text, "PASS") {
		t.Fatalf("reply was lost, text = %q", acc.text)
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

// TestIsErrorIsRecorded: Claude reports a failed turn in the result record
// while still exiting zero.
func TestIsErrorIsRecorded(t *testing.T) {
	acc, _ := feed(t, []string{
		`{"type":"result","result":"hit the turn limit","is_error":true}`,
	})
	if !acc.isError {
		t.Error("isError = false, want true — a failed turn must not read as a reply")
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
	// Tool results come back as `user` records, and must never become the
	// reply.
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

func TestArgsCarriesPromptLast(t *testing.T) {
	a := New()
	got := a.args(agent.Request{Prompt: "-do the thing"})

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
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q, got %q", want, joined)
		}
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
	for _, unwanted := range []string{"--append-system-prompt", "--session-id", "--resume", "--permission-prompt-tool"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("args should omit %s when unset, got %q", unwanted, got)
		}
	}
}

func TestArgsAppendsRatherThanReplacesTheSystemPrompt(t *testing.T) {
	// The role in the project is an overlay on a working coding agent, not a
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

// TestHubTurnsKeepPermissionChecks: a hub thread has a person watching, so
// it must never skip permission checks. Prompts go to the MCP tool instead.
func TestHubTurnsKeepPermissionChecks(t *testing.T) {
	got := strings.Join(New().args(agent.Request{
		Prompt:               "hi",
		Conversation:         "11111111-1111-4111-8111-111111111111",
		MCPConfig:            `{"mcpServers":{}}`,
		PermissionPromptTool: "mcp__neoharness__permission_prompt",
	}), " ")
	if strings.Contains(got, "bypassPermissions") {
		t.Errorf("args = %q, want permission checks kept", got)
	}
	for _, want := range []string{
		"--session-id 11111111-1111-4111-8111-111111111111",
		"--permission-prompt-tool mcp__neoharness__permission_prompt",
		`--mcp-config {"mcpServers":{}}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("args missing %q, got %q", want, got)
		}
	}
}

// TestSessionAlreadyInUseIsResumed: a turn stopped after claude created the
// session, but before it said anything, leaves the caller thinking the session
// is new. Claude then refuses the id on every later turn; the adapter must
// continue the session instead.
func TestSessionAlreadyInUseIsResumed(t *testing.T) {
	script := filepath.Join(t.TempDir(), "claude")
	body := `#!/bin/sh
case "$*" in
*--resume*) echo '{"type":"result","result":"resumed","is_error":false}' ;;
*) echo "Error: Session ID abc is already in use." >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := New(script).Run(context.Background(), agent.Request{Prompt: "hi", Conversation: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	if res.ExitCode != 0 || res.Text != "resumed" {
		t.Fatalf("result = %+v, want the turn continued with --resume", res)
	}
}

func TestResumeContinuesTheConversation(t *testing.T) {
	got := strings.Join(New().args(agent.Request{Prompt: "hi", Conversation: "abc", Resume: true}), " ")
	if !strings.Contains(got, "--resume abc") || strings.Contains(got, "--session-id") {
		t.Errorf("args = %q, want --resume and no --session-id", got)
	}
}

// --- remote ---

func TestSSHCommandQuotesEverything(t *testing.T) {
	got := sshCommand("gpu1", "~/my proj", []string{"MCP_TOOL_TIMEOUT=1"}, []string{"claude", "-p", "it's -x"})
	if strings.Join(got[:5], " ") != "ssh -o BatchMode=yes gpu1 --" {
		t.Fatalf("prefix = %q", got[:5])
	}
	want := `exec "$SHELL" -lc 'cd ~/'\''my proj'\'' && exec env MCP_TOOL_TIMEOUT=1 claude -p '\''it'\''\'\'''\''s -x'\'''`
	if got[5] != want {
		t.Errorf("remote =\n %s\nwant\n %s", got[5], want)
	}
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"plain":   "plain",
		"":        "''",
		"a b":     "'a b'",
		"it's":    `'it'\''s'`,
		"--x=1,2": "--x=1,2",
		`{"a":1}`: `'{"a":1}'`,
		"$HOME":   "'$HOME'",
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
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
