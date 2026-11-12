package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

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

	if !slices.Contains(acc.commands, "simplify") || !slices.Contains(acc.commands, "dev:pr-create") {
		t.Errorf("commands = %v, want the init record's slash commands", acc.commands)
	}
	if acc.model != "claude-opus-5[1m]" || acc.context != 40716 || acc.cost != 0.751725 || acc.models["claude-opus-5[1m]"].ContextWindow != 1000000 {
		t.Errorf("model %q context %d cost %v models %v", acc.model, acc.context, acc.cost, acc.models)
	}
	if slices.Contains(acc.commands, "doctor") {
		t.Error("commands include doctor, a terminal-only command")
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

func TestArgsKeepThePromptOff(t *testing.T) {
	a := New()
	got := a.args(agent.Request{Prompt: "-do the thing"})

	if got[0] != "claude" {
		t.Errorf("args[0] = %q, want the default binary", got[0])
	}
	// The prompt goes on stdin, so a session can take the next one there.
	if slices.Contains(got, "-do the thing") {
		t.Errorf("args = %q, want the prompt on stdin", got)
	}
	joined := strings.Join(got, " ")
	for _, want := range []string{
		"-p --input-format stream-json",
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
	for _, unwanted := range []string{"--append-system-prompt", "--session-id", "--resume", "--permission-prompt-tool", "--permission-mode"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("args should omit %s when unset, got %q", unwanted, got)
		}
	}
}

func TestArgsAppendsRatherThanReplacesTheSystemPrompt(t *testing.T) {
	// The role in the workspace is an overlay on a working coding agent, not a
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
		Prompt:       "hi",
		Conversation: "11111111-1111-4111-8111-111111111111",
		Harness:      agent.Harness{URL: "http://127.0.0.1:4242/mcp", Token: "tok"},
	}), " ")
	if strings.Contains(got, "bypassPermissions") {
		t.Errorf("args = %q, want permission checks kept", got)
	}
	for _, want := range []string{
		"--session-id 11111111-1111-4111-8111-111111111111",
		"--permission-prompt-tool mcp__neoharness__permission_prompt",
		"--permission-mode auto",
		`--mcp-config {"mcpServers":{"neoharness":{"headers":{"Authorization":"Bearer tok"},"type":"http","url":"http://127.0.0.1:4242/mcp"}}}`,
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
	if runtime.GOOS == "windows" {
		t.Skip("the fake claude is a shell script")
	}
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

// TestStopEndsTheTurnWhileOthersHoldTheOutput: an ssh ControlMaster gets the
// client's stdout and stderr and keeps them open after the client is killed,
// until the remote turn ends. A stopped turn must end at once all the same.
func TestStopEndsTheTurnWhileOthersHoldTheOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake claude is a shell script")
	}
	script := filepath.Join(t.TempDir(), "claude")
	// set -m puts sleep in its own process group, out of reach of the kill.
	body := "#!/bin/sh\nset -m\nsleep 30 &\necho '{\"type\":\"system\"}'\nwait\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _ = New(script).Run(ctx, agent.Request{Prompt: "hi"})
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("the stopped turn took %v to end", d)
	}
}

func TestUnknownConversationIsLost(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake claude is a shell script")
	}
	script := filepath.Join(t.TempDir(), "claude")
	body := "#!/bin/sh\necho 'No conversation found with session ID: abc' >&2; exit 1\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, resume := range []bool{true, false} {
		res, err := New(script).Run(context.Background(), agent.Request{Prompt: "hi", Conversation: "abc", Resume: resume})
		if err != nil || res.Lost != resume {
			t.Fatalf("resume %v: lost = %v, err = %v", resume, res.Lost, err)
		}
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
	want := `exec "$SHELL" -lc 'export PATH="$HOME/.local/bin:$PATH"; cd ~/'\''my proj'\'' && exec env MCP_TOOL_TIMEOUT=1 claude -p '\''it'\''\'\'''\''s -x'\'''`
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

// TestBuiltinCommandOutputIsShown: /context and the like answer only in the
// result record.
func TestBuiltinCommandOutputIsShown(t *testing.T) {
	_, got := feed(t, []string{`{"type":"result","result":"## Context Usage\n31.7k / 400k","is_error":false}`})
	if len(got) != 2 || got[0].Kind != agent.KindAssistantText || !strings.Contains(got[0].Text, "Context Usage") {
		t.Fatalf("events = %+v", got)
	}
	_, got = feed(t, []string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Done."}]}}`,
		`{"type":"result","result":"Done.","is_error":false}`,
	})
	if len(got) != 2 {
		t.Fatalf("a normal reply must not show twice: %+v", got)
	}
}

func TestModelAndEffortFlags(t *testing.T) {
	args := strings.Join(New().args(agent.Request{Prompt: "hi", Model: "sonnet", Effort: "high"}), " ")
	if !strings.Contains(args, "--model sonnet") || !strings.Contains(args, "--effort high") {
		t.Fatalf("args = %s", args)
	}
}

func TestFailedToolResultIsReported(t *testing.T) {
	_, got := feed(t, []string{
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tu1","name":"Bash","input":{"command":"git push --force"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tu1","is_error":true,"content":"Permission to use Bash was denied.\nmore"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tu2","name":"Read","input":{"file_path":"a"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tu2","content":[{"type":"text","text":"ok"}]}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tu3","is_error":true,"content":[{"type":"text","text":"no such file"}]}]}}`,
	})
	if len(got) != 4 || got[0].ID != "tu1" {
		t.Fatalf("events = %+v", got)
	}
	if f := got[1]; f.Kind != agent.KindToolFailed || f.ID != "tu1" || f.Text != "Permission to use Bash was denied." {
		t.Fatalf("a denied call = %+v", f)
	}
	if f := got[3]; f.Kind != agent.KindToolFailed || f.Text != "no such file" {
		t.Fatalf("a failed call with text blocks = %+v", f)
	}
}

func TestPermissionPromptsInTheHubsTerms(t *testing.T) {
	for _, c := range []struct {
		tool, input string
		want        agent.Permission
	}{
		{"Bash", `{"command":"go  test ./..."}`, agent.Permission{Tool: "Bash", Detail: "go test ./...", Prefix: "go"}},
		{"Bash", `{"command":"go test ./... && rm -rf /"}`, agent.Permission{Tool: "Bash", Detail: "go test ./... && rm -rf /", Prefix: "go", Compound: true}},
		{"Write", `{"file_path":"x"}`, agent.Permission{Tool: "Write", Detail: "x"}},
	} {
		if got := Ask(c.tool, json.RawMessage(c.input)); got != c.want {
			t.Errorf("Ask(%s, %s) = %+v, want %+v", c.tool, c.input, got, c.want)
		}
	}
	if !HarnessTool("mcp__neoharness__whoami") || HarnessTool("mcp__slack__read") {
		t.Error("only the neoharness tools skip the person")
	}
}

func TestHarnessOnlyDropsTheOtherTools(t *testing.T) {
	req := agent.Request{Prompt: "hi", Harness: agent.Harness{URL: "http://127.0.0.1:4242/mcp", Token: "tok"}}
	if got := New().args(req); slices.Contains(got, "--tools") || slices.Contains(got, "--strict-mcp-config") {
		t.Errorf("args = %q, want the agent's own tools for a thread", got)
	}
	req.HarnessOnly = true
	got := New().args(req)
	if i := slices.Index(got, "--tools"); i < 0 || got[i+1] != "" || !slices.Contains(got, "--strict-mcp-config") {
		t.Errorf("args = %q, want --tools \"\" and --strict-mcp-config", got)
	}
}

// --- sessions that outlive a turn ---

type fakeProvider struct {
	*Provider
	script string
}

// fakeSession writes a claude that answers each prompt on stdin with a
// result, "slow" only when interrupted, and counts its starts in a file.
func fakeSession(t *testing.T, exitAfterTurn bool) (provider *fakeProvider, starts func() int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake claude is a shell script")
	}
	dir := t.TempDir()
	count := filepath.Join(dir, "starts")
	exit := ""
	if exitAfterTurn {
		exit = "exit 0"
	}
	body := fmt.Sprintf(`#!/bin/sh
echo x >> %q
n=0
while IFS= read -r line; do
  case "$line" in
  *control_request*) echo '{"type":"result","is_error":true}' ;;
  *'"slow"'*) ;;
  *) n=$((n+1)); echo "{\"type\":\"result\",\"result\":\"turn $n\",\"total_cost_usd\":0.$n}"; %s ;;
  esac
done
`, count, exit)
	script := filepath.Join(dir, "claude")
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	provider = &fakeProvider{Provider: NewProvider(), script: script}
	t.Cleanup(provider.Close)
	return provider, func() int {
		b, _ := os.ReadFile(count)
		return strings.Count(string(b), "x")
	}
}

func turn(t *testing.T, p *fakeProvider, ctx context.Context, req agent.Request) agent.Result {
	t.Helper()
	res, err := p.Agent(agent.Settings{Command: []string{p.script}}, "", "").Run(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// TestTheNextTurnUsesTheSameProcess: a turn must not pay for starting claude
// again.
func TestTheNextTurnUsesTheSameProcess(t *testing.T) {
	p, starts := fakeSession(t, false)
	req := agent.Request{Prompt: "hi", Conversation: "c1"}
	first := turn(t, p, context.Background(), req)
	req.Resume = true
	second := turn(t, p, context.Background(), req)
	if first.Text != "turn 1" || second.Text != "turn 2" || starts() != 1 {
		t.Fatalf("texts %q %q, %d starts; want both turns from one process", first.Text, second.Text, starts())
	}
	// Claude counts the cost from the process start.
	if math.Abs(second.Cost-0.1) > 1e-9 {
		t.Errorf("second turn cost %v, want 0.1", second.Cost)
	}
}

func TestAStoppedTurnKeepsTheProcess(t *testing.T) {
	p, starts := fakeSession(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	begin := time.Now()
	res := turn(t, p, ctx, agent.Request{Prompt: "slow", Conversation: "c1"})
	if d := time.Since(begin); d > 2*time.Second || res.ExitCode == 0 {
		t.Fatalf("stopped turn took %v, exit %d", d, res.ExitCode)
	}
	if res := turn(t, p, context.Background(), agent.Request{Prompt: "hi", Conversation: "c1", Resume: true}); res.Text != "turn 1" || starts() != 1 {
		t.Fatalf("next turn %q after %d starts, want the same process", res.Text, starts())
	}
}

func TestATurnThatClaudeIgnoresIsKilled(t *testing.T) {
	old := interruptWait
	interruptWait = 100 * time.Millisecond
	t.Cleanup(func() { interruptWait = old })
	if runtime.GOOS == "windows" {
		t.Skip("the fake claude is a shell script")
	}
	script := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	begin := time.Now()
	res, err := New(script).Run(ctx, agent.Request{Prompt: "hi", Conversation: "c1"})
	if err != nil || res.ExitCode == 0 || time.Since(begin) > 3*time.Second {
		t.Fatalf("exit %d, err %v, after %v", res.ExitCode, err, time.Since(begin))
	}
}

func TestANewModelStartsANewProcess(t *testing.T) {
	p, starts := fakeSession(t, false)
	turn(t, p, context.Background(), agent.Request{Prompt: "hi", Conversation: "c1"})
	turn(t, p, context.Background(), agent.Request{Prompt: "hi", Conversation: "c1", Resume: true, Model: "sonnet"})
	if starts() != 2 {
		t.Fatalf("%d starts, want 2", starts())
	}
}

func TestAProcessThatEndedIsStartedAgain(t *testing.T) {
	p, starts := fakeSession(t, true)
	turn(t, p, context.Background(), agent.Request{Prompt: "hi", Conversation: "c1"})
	time.Sleep(100 * time.Millisecond)
	res := turn(t, p, context.Background(), agent.Request{Prompt: "hi", Conversation: "c1", Resume: true})
	if res.ExitCode != 0 || res.Text != "turn 1" || starts() != 2 {
		t.Fatalf("result %+v after %d starts, want a new process to take the turn", res, starts())
	}
}

func TestAnIdleProcessEnds(t *testing.T) {
	old := idleFor
	idleFor = 50 * time.Millisecond
	t.Cleanup(func() { idleFor = old })
	p, starts := fakeSession(t, false)
	turn(t, p, context.Background(), agent.Request{Prompt: "hi", Conversation: "c1"})
	time.Sleep(200 * time.Millisecond)
	p.pool.mu.Lock()
	n := len(p.pool.sessions)
	p.pool.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d idle sessions after idleFor", n)
	}
	turn(t, p, context.Background(), agent.Request{Prompt: "hi", Conversation: "c1", Resume: true})
	if starts() != 2 {
		t.Fatalf("%d starts, want 2", starts())
	}
}

// TestAnotherAgentOfTheKindTakesOverTheSession: :setlocal can move a session
// to another agent of the same kind. The old process must end, not wait for
// its idle time next to the new one.
func TestAnotherAgentOfTheKindTakesOverTheSession(t *testing.T) {
	p, starts := fakeSession(t, false)
	turn(t, p, context.Background(), agent.Request{Prompt: "hi", Conversation: "c1"})
	old := p.pool.sessions["c1"]
	other := agent.Settings{Command: []string{p.script, "--other"}}
	if _, err := p.Agent(other, "", "").Run(context.Background(), agent.Request{Prompt: "hi", Conversation: "c1", Resume: true}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-old.lines:
	case <-time.After(3 * time.Second):
		t.Fatal("the old process is still up")
	}
	if starts() != 2 {
		t.Fatalf("%d starts, want 2", starts())
	}
}
