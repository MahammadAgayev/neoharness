// Package pi adapts the `pi` CLI to the agent.Agent interface.
//
// There is no Go SDK for pi, so this drives the binary as a subprocess in JSON
// mode and parses its NDJSON event stream. The wire format is documented in
// pi's docs/json.md; the parsing here follows what a real run actually emits,
// and the fixtures in the test are captured from one rather than invented, so a
// change to pi's format fails the suite instead of silently emptying the
// display.
package pi

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/MahammadAgayev/harness/internal/agent"
)

// Agent runs turns through the pi CLI.
type Agent struct {
	// Command is the binary and any leading arguments. Configurable rather than
	// a constant because pi is not always invoked as bare `pi` — a wrapper that
	// injects credentials is a normal way to run it.
	Command []string
}

// New returns an Agent driving the given command, defaulting to `pi`.
func New(command ...string) *Agent {
	if len(command) == 0 {
		command = []string{"pi"}
	}
	return &Agent{Command: command}
}

func (a *Agent) Name() string { return "pi" }

// maxStderr bounds what is kept from a failed process. Enough to identify the
// failure, not enough to fill state.json with a stack trace.
const maxStderr = 2000

// Run executes one turn.
func (a *Agent) Run(ctx context.Context, req agent.Request) (agent.Result, error) {
	args := a.args(req)
	started := time.Now()

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = req.Cwd

	// stdin must be /dev/null, not an inherited pipe. With a pipe that nobody
	// ever closes, `pi -p` waits on it forever: an earlier version of this
	// harness sat at "iteration 1" for three minutes having burned 0.27s of CPU.
	// exec.Cmd with a nil Stdin gives the process /dev/null, which is what we
	// want — this is stated rather than left implicit because it looks like an
	// omission.
	cmd.Stdin = nil

	// Its own process group, so a timeout can kill the whole tree. Killing just
	// `pi` leaves the build it spawned holding the stdout pipe open, and the
	// read loop below then blocks until that grandchild finishes anyway.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Cancellation has to kill the group too. CommandContext's default is to
	// signal the direct child only, which leaves exactly the wedged grandchild
	// described above: a cancelled Run then blocks on the pipe until the
	// grandchild finishes on its own, which is the opposite of cancelling.
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

	// The timeout is a watchdog timer, not a check inside the read loop.
	// Checking on each event means the deadline only fires when the subprocess
	// says something, and an agent blocked in one long `bash` call says nothing:
	// a 600s limit once let a build run to 815s before the next event arrived to
	// trip it. A timer fires on time whatever the child is doing.
	var timedOut bool
	var mu sync.Mutex
	if req.Timeout > 0 {
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
	// pi emits whole messages on a line; a large tool result makes those lines
	// far longer than bufio's 64KB default, and the scanner would stop early
	// with no error visible to the caller.
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
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
		if ok := asExitError(waitErr, &exitErr); ok {
			res.ExitCode = exitErr.ExitCode()
		} else {
			return res, fmt.Errorf("running %s: %w", args[0], waitErr)
		}
	}
	return res, nil
}

// args builds the pi command line for a request.
func (a *Agent) args(req agent.Request) []string {
	args := append([]string{}, a.Command...)
	// JSON mode is what makes a run watchable at all: `pi -p` in text mode
	// prints nothing until the process exits, so a 40-minute iteration is
	// indistinguishable from a hang.
	args = append(args, "-p", "--mode", "json", "-ne")
	if req.SessionDir != "" {
		args = append(args, "--session-dir", req.SessionDir)
	}
	if req.SessionID != "" {
		args = append(args, "--session-id", req.SessionID)
	}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	if len(req.Tools) > 0 {
		args = append(args, "-t", strings.Join(req.Tools, ","))
	}
	if req.SystemPrompt != "" {
		args = append(args, "--system-prompt", req.SystemPrompt)
	}
	// `--` so a prompt beginning with a dash is not read as a flag.
	return append(args, "--", req.Prompt)
}

// accumulator folds the event stream into a Result while forwarding the
// interesting events onward.
//
// Rendering and accounting read the same stream in one pass. They were once two
// functions over two copies of it, which meant a verdict was parsed from a
// string kept in memory purely to be re-split.
type accumulator struct {
	onEvent func(agent.Event)
	text    string
	tools   int
	tokens  int
}

// wire mirrors only the fields this adapter reads. Decoding into a narrow
// struct rather than a map keeps the field names in one place where a pi format
// change shows up as a compile-time-shaped diff instead of a typo in a string
// key.
type wire struct {
	Type     string          `json:"type"`
	ToolName string          `json:"toolName"`
	Args     json.RawMessage `json:"args"`
	Message  *struct {
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage *struct {
			TotalTokens int `json:"totalTokens"`
		} `json:"usage"`
	} `json:"message"`
}

func (a *accumulator) consume(line []byte) {
	var e wire
	if err := json.Unmarshal(line, &e); err != nil {
		// A subprocess killed mid-write leaves a truncated final line. That is
		// expected on a timeout, not an error worth surfacing.
		return
	}
	switch e.Type {
	case "tool_execution_start":
		a.tools++
		a.emit(agent.Event{
			Kind:   agent.KindToolStart,
			Tool:   e.ToolName,
			Detail: detailOf(e.Args),
			Tokens: a.tokens,
		})
	case "message_end":
		if e.Message == nil {
			return
		}
		if u := e.Message.Usage; u != nil && u.TotalTokens > a.tokens {
			// Monotonic: providers report cumulative usage at unpredictable
			// points, and some report zero until the very end.
			a.tokens = u.TotalTokens
		}
		if e.Message.Role != "assistant" {
			return
		}
		var parts []string
		for _, c := range e.Message.Content {
			if c.Type == "text" && strings.TrimSpace(c.Text) != "" {
				parts = append(parts, c.Text)
			}
		}
		if len(parts) == 0 {
			// Only overwrite when there is prose. A trailing tool-call-only
			// message would otherwise blank a real verdict into a failed
			// dispatch, which the loop reads as "the reviewer could not be run".
			return
		}
		a.text = strings.TrimSpace(strings.Join(parts, " "))
		a.emit(agent.Event{Kind: agent.KindAssistantText, Text: a.text, Tokens: a.tokens})
	case "turn_end":
		a.emit(agent.Event{Kind: agent.KindTurnEnd, Tokens: a.tokens})
	}
	// message_update is dropped: it is one event per token and the bulk of the
	// stream. turn_end deliberately carries no text — it repeats the assistant
	// message that message_end already delivered, and matching it would double
	// every reply.
}

func (a *accumulator) emit(e agent.Event) {
	if a.onEvent != nil {
		a.onEvent(e)
	}
}

// detailOf picks the interesting argument out of a tool call.
//
// The first string argument is nearly always the one worth showing — a path, a
// command, a pattern — and which key holds it varies by tool. Decoding to a map
// loses ordering, so the raw JSON is scanned in the order pi emitted it.
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
		if _, err := dec.Token(); err != nil { // key
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
