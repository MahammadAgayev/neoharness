// Package claude adapts the Claude Code CLI to the agent.Agent interface.
//
// Claude nests tool calls inside assistant message content rather than
// emitting a separate tool event, and ends with a single `result` record
// carrying the final text and an error flag. Both are handled here so that
// nothing above this package has to know which CLI ran.
package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"

	"github.com/MahammadAgayev/neoharness/internal/agent"
)

// Agent runs turns through the Claude Code CLI.
type Agent struct {
	// Command is the binary and any leading arguments. On some setups claude is
	// reached through a wrapper that injects credentials — `authwrap agent run
	// claude` — so this cannot be a constant.
	Command []string
	// Host runs the CLI on another machine over ssh, in Dir. Empty runs it
	// locally in the request's Cwd.
	Host string
	Dir  string
}

// New returns an Agent driving the given command, defaulting to `claude`.
func New(command ...string) *Agent {
	if len(command) == 0 {
		command = []string{"claude"}
	}
	return &Agent{Command: command}
}

const maxStderr = 2000

// Run executes one turn.
func (a *Agent) Run(ctx context.Context, req agent.Request) (agent.Result, error) {
	res, err := a.run(ctx, req)
	// A turn stopped after claude created the session, but before it said
	// anything, leaves the caller thinking the session is new. Claude then
	// refuses the id on every later turn, so continue the session instead.
	if err == nil && res.ExitCode != 0 && req.Conversation != "" && !req.Resume &&
		strings.Contains(res.Stderr, "is already in use") {
		req.Resume = true
		return a.run(ctx, req)
	}
	return res, err
}

func (a *Agent) run(ctx context.Context, req agent.Request) (agent.Result, error) {
	args := a.args(req)

	var cmd *exec.Cmd
	if a.Host != "" {
		ssh := sshCommand(a.Host, a.Dir, req.Env, args)
		cmd = exec.CommandContext(ctx, ssh[0], ssh[1:]...)
	} else {
		cmd = exec.CommandContext(ctx, args[0], args[1:]...)
		cmd.Dir = req.Cwd
		if len(req.Env) > 0 {
			cmd.Env = append(os.Environ(), req.Env...)
		}
	}
	// An inherited stdin pipe that nobody closes leaves a `--print` invocation
	// waiting on it forever.
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

	acc := &accumulator{onEvent: req.OnEvent}
	scanner := bufio.NewScanner(stdout)
	// Claude embeds whole tool results in the stream, so lines run well past
	// bufio's 64KB default. Without this the scanner stops early and silently.
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		acc.consume(scanner.Bytes())
	}

	waitErr := cmd.Wait()

	res := agent.Result{
		Text:     acc.text,
		Tools:    acc.tools,
		Tokens:   acc.tokens,
		Stderr:   trim(stderr.String(), maxStderr),
		Commands: acc.commands,
		Model:    acc.model,
		Context:  acc.context,
		Cost:     acc.cost,
	}
	for _, m := range acc.models {
		res.ContextWindow = max(res.ContextWindow, m.ContextWindow)
	}
	switch {
	case waitErr != nil:
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
		} else {
			return res, fmt.Errorf("running %s: %w", args[0], waitErr)
		}
	case acc.isError:
		// Claude reports a failed turn in the result record while still
		// exiting zero.
		res.ExitCode = 1
	}
	return res, nil
}

func (a *Agent) args(req agent.Request) []string {
	args := append([]string{}, a.Command...)
	// --verbose is required for stream-json to emit anything per-event; without
	// it the stream carries only the final result.
	args = append(args, "--output-format", "stream-json", "--verbose")
	if req.PermissionPromptTool != "" {
		args = append(args, "--permission-prompt-tool", req.PermissionPromptTool)
	}
	if req.PermissionMode != "" {
		args = append(args, "--permission-mode", req.PermissionMode)
	}
	if req.MCPConfig != "" {
		args = append(args, "--mcp-config", req.MCPConfig)
	}
	if req.Conversation != "" {
		if req.Resume {
			args = append(args, "--resume", req.Conversation)
		} else {
			// Only ever a fresh UUID the hub made for the thread: Claude
			// refuses a --session-id that already exists.
			args = append(args, "--session-id", req.Conversation)
		}
	}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	if req.Effort != "" {
		args = append(args, "--effort", req.Effort)
	}
	if req.SystemPrompt != "" {
		// Appended, not replaced: the role in the workspace is an overlay on a
		// working coding agent, not a replacement for one.
		args = append(args, "--append-system-prompt", req.SystemPrompt)
	}
	// -p last and immediately before the prompt: Claude reads the prompt as
	// -p's value, and a prompt beginning with a dash would otherwise be taken
	// for a flag.
	return append(args, "-p", req.Prompt)
}

// accumulator folds Claude's stream into a Result.
type accumulator struct {
	onEvent  func(agent.Event)
	text     string
	tools    int
	tokens   int
	isError  bool
	said     bool // emitted assistant text this turn
	commands []string
	model    string
	context  int
	cost     float64
	models   map[string]modelUsage
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
	// Init record.
	Model            string   `json:"model"`
	SlashCommands    []string `json:"slash_commands"`
	TerminalCommands []string `json:"terminal_slash_commands"`
	// Terminal record.
	Cost       float64               `json:"total_cost_usd"`
	ModelUsage map[string]modelUsage `json:"modelUsage"`
}

type modelUsage struct {
	ContextWindow int `json:"contextWindow"`
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
	case "system":
		if e.Subtype == "init" {
			a.model = e.Model
			// Terminal commands act on the interactive screen, which a
			// --print session does not have.
			a.commands = slices.DeleteFunc(e.SlashCommands, func(c string) bool {
				return slices.Contains(e.TerminalCommands, c)
			})
		}

	case "assistant":
		if e.Message == nil {
			return
		}
		if t := e.Message.Usage.total(); t > a.tokens {
			a.tokens = t
		}
		if t := e.Message.Usage.total(); t > 0 {
			a.context = t
		}
		var prose []string
		for _, c := range e.Message.Content {
			switch c.Type {
			case "text":
				if strings.TrimSpace(c.Text) != "" {
					prose = append(prose, c.Text)
				}
			case "tool_use":
				// The tool call is a content block, not its own event.
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
			a.said = true
			a.text = strings.TrimSpace(strings.Join(prose, " "))
			a.emit(agent.Event{Kind: agent.KindAssistantText, Text: a.text, Tokens: a.tokens})
		}

	case "result":
		if t := e.Usage.total(); t > a.tokens {
			a.tokens = t
		}
		a.isError = e.IsError
		a.cost = e.Cost
		a.models = e.ModelUsage
		// The authoritative final answer. Preferred over the last assistant
		// message because that is what the CLI itself considers the reply.
		if strings.TrimSpace(e.Result) != "" {
			a.text = strings.TrimSpace(e.Result)
			// Built-in commands such as /context answer only here, with no
			// assistant message; without this the turn looks silent.
			if !a.said {
				a.emit(agent.Event{Kind: agent.KindAssistantText, Text: a.text, Tokens: a.tokens})
			}
		}
		a.emit(agent.Event{Kind: agent.KindTurnEnd, Tokens: a.tokens})
	}
	// Other "system" records are hooks and progress — machinery, not work.
	// "user" records carry tool results back in; the neoharness does not display
	// them, on the same principle that keeps the tool firehose off the screen.
}

func (a *accumulator) emit(e agent.Event) {
	if a.onEvent != nil {
		a.onEvent(e)
	}
}

// detailOf picks the interesting argument out of a tool input object, in the
// order the CLI emitted it: the first string is nearly always the path,
// command, or pattern worth showing.
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
