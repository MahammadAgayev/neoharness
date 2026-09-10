// Package claude adapts the Claude Code CLI to the agent.Agent interface.
//
// It exists to prove the agent boundary is real: the loop, the prompts, the
// state, and both TUIs are unchanged by its presence, and swapping it in is a
// field in state.json.
//
// The stream shape differs from pi's in ways that matter. Claude nests tool
// calls inside assistant message content rather than emitting a separate
// tool event, and ends with a single `result` record carrying the final text
// and an error flag. Both are handled here so that nothing above this package
// has to know which CLI ran.
package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/MahammadAgayev/harness/internal/agent"
)

// Agent runs turns through the Claude Code CLI.
type Agent struct {
	// Command is the binary and any leading arguments. On some setups claude is
	// reached through a wrapper that injects credentials — `authwrap agent run
	// claude` — so this cannot be a constant.
	Command []string
}

// New returns an Agent driving the given command, defaulting to `claude`.
func New(command ...string) *Agent {
	if len(command) == 0 {
		command = []string{"claude"}
	}
	return &Agent{Command: command}
}

func (a *Agent) Name() string { return "claude" }

const maxStderr = 2000

// toolNames maps the harness's tool vocabulary onto Claude's.
//
// The loop asks for tools by pi's names because it had to pick one vocabulary;
// translating here keeps that choice from leaking. An unmapped name is passed
// through, so a Claude-specific tool can still be requested.
var toolNames = map[string]string{
	"read":  "Read",
	"write": "Write",
	"edit":  "Edit",
	"bash":  "Bash",
	"grep":  "Grep",
	"find":  "Glob",
	"ls":    "Read",
}

func mapTools(tools []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range tools {
		name, ok := toolNames[t]
		if !ok {
			name = t
		}
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// Run executes one turn.
func (a *Agent) Run(ctx context.Context, req agent.Request) (agent.Result, error) {
	args := a.args(req)
	started := time.Now()

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = req.Cwd
	// Same reasoning as the pi adapter: an inherited stdin pipe that nobody
	// closes leaves a `--print` invocation waiting on it forever.
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		killGroup(cmd)
		return nil
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return agent.Result{}, err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return agent.Result{}, fmt.Errorf("starting %s: %w", args[0], err)
	}

	var timedOut bool
	var mu sync.Mutex
	if req.Timeout > 0 {
		// A watchdog timer, not a per-event deadline: an agent blocked in one
		// long build emits nothing, and a deadline that only fires on the next
		// event would never fire at all.
		t := time.AfterFunc(req.Timeout, func() {
			mu.Lock()
			timedOut = true
			mu.Unlock()
			killGroup(cmd)
		})
		defer t.Stop()
	}

	acc := &accumulator{onEvent: req.OnEvent}
	scanner := bufio.NewScanner(stdout)
	// Claude embeds whole tool results in the stream, so lines run well past
	// bufio's 64KB default. Without this the scanner stops early and silently.
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		acc.consume(scanner.Bytes())
	}

	waitErr := cmd.Wait()

	mu.Lock()
	killed := timedOut
	mu.Unlock()

	res := agent.Result{
		Text:     acc.text,
		Tools:    acc.tools,
		Tokens:   acc.tokens,
		Duration: time.Since(started),
		Stderr:   trim(stderr.String(), maxStderr),
		TimedOut: killed,
	}
	switch {
	case killed:
		res.ExitCode = 1
		res.Stderr = strings.TrimSpace(res.Stderr + fmt.Sprintf("\nKilled after %s.", req.Timeout))
	case waitErr != nil:
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
		} else {
			return res, fmt.Errorf("running %s: %w", args[0], waitErr)
		}
	case acc.isError:
		// Claude reports a failed turn in the result record while still
		// exiting zero. Left unhandled, a failed review would be parsed for a
		// verdict — the exact class of bug the pi adapter's empty-text rule
		// guards against.
		res.ExitCode = 1
	}
	return res, nil
}

func (a *Agent) args(req agent.Request) []string {
	args := append([]string{}, a.Command...)
	// --verbose is required for stream-json to emit anything per-event; without
	// it the stream carries only the final result.
	args = append(args, "-p", req.Prompt, "--output-format", "stream-json", "--verbose")
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	if req.SystemPrompt != "" {
		// Claude keeps its own system prompt and appends to it, which is the
		// right shape here: the evaluator persona is an overlay on a working
		// coding agent, not a replacement for one.
		args = append(args, "--append-system-prompt", req.SystemPrompt)
	}
	if tools := mapTools(req.Tools); len(tools) > 0 {
		args = append(args, "--allowedTools", strings.Join(tools, ","))
	}
	return args
}

// accumulator folds Claude's stream into a Result.
type accumulator struct {
	onEvent func(agent.Event)
	text    string
	tools   int
	tokens  int
	isError bool
}

type wire struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	Message *struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		Usage *usage `json:"usage"`
	} `json:"message"`
	// Terminal record.
	Result  string `json:"result"`
	IsError bool   `json:"is_error"`
	Usage   *usage `json:"usage"`
}

type usage struct {
	Input         int `json:"input_tokens"`
	Output        int `json:"output_tokens"`
	CacheCreation int `json:"cache_creation_input_tokens"`
	CacheRead     int `json:"cache_read_input_tokens"`
}

// total is the comparable number. Claude reports components rather than a
// total, and cache reads are real context the run paid to carry.
func (u *usage) total() int {
	if u == nil {
		return 0
	}
	return u.Input + u.Output + u.CacheCreation + u.CacheRead
}

func (a *accumulator) consume(line []byte) {
	var e wire
	if err := json.Unmarshal(line, &e); err != nil {
		// A process killed mid-write leaves a truncated final line.
		return
	}

	switch e.Type {
	case "assistant":
		if e.Message == nil {
			return
		}
		if t := e.Message.Usage.total(); t > a.tokens {
			a.tokens = t
		}
		var prose []string
		for _, c := range e.Message.Content {
			switch c.Type {
			case "text":
				if strings.TrimSpace(c.Text) != "" {
					prose = append(prose, c.Text)
				}
			case "tool_use":
				// The shape that differs most from pi: the tool call is a
				// content block, not its own event.
				a.tools++
				a.emit(agent.Event{
					Kind:   agent.KindToolStart,
					Tool:   c.Name,
					Detail: detailOf(c.Input),
					Tokens: a.tokens,
				})
			}
		}
		if len(prose) > 0 {
			a.text = strings.TrimSpace(strings.Join(prose, " "))
			a.emit(agent.Event{Kind: agent.KindAssistantText, Text: a.text, Tokens: a.tokens})
		}

	case "result":
		if t := e.Usage.total(); t > a.tokens {
			a.tokens = t
		}
		a.isError = e.IsError
		// The authoritative final answer. Preferred over the last assistant
		// message because that is what the CLI itself considers the reply, and
		// for the reviewer this is the line the verdict is read from.
		if strings.TrimSpace(e.Result) != "" {
			a.text = strings.TrimSpace(e.Result)
		}
		a.emit(agent.Event{Kind: agent.KindTurnEnd, Tokens: a.tokens})
	}
	// "system" records are hooks, init, and progress — machinery, not work.
	// "user" records carry tool results back in; the harness does not display
	// them, on the same principle that keeps the tool firehose off the screen.
}

func (a *accumulator) emit(e agent.Event) {
	if a.onEvent != nil {
		a.onEvent(e)
	}
}

// detailOf picks the interesting argument out of a tool input object, in the
// order the CLI emitted it. Same rule as the pi adapter: the first string is
// nearly always the path, command, or pattern worth showing.
func detailOf(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	tok, err := dec.Token()
	if err != nil {
		return ""
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return ""
	}
	for dec.More() {
		if _, err := dec.Token(); err != nil {
			return ""
		}
		var v any
		if err := dec.Decode(&v); err != nil {
			return ""
		}
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			return strings.Join(strings.Fields(s), " ")
		}
	}
	return ""
}

func trim(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
