// Package claude adapts the Claude Code CLI to the agent.Agent interface.
//
// Claude nests tool calls inside assistant message content rather than
// emitting a separate tool event, and ends with a single `result` record
// carrying the final text and an error flag. Both are handled here so that
// nothing above this package has to know which CLI ran.
package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

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

	pool *pool
}

// New returns an Agent driving the given command, defaulting to `claude`.
func New(command ...string) *Agent {
	if len(command) == 0 {
		command = []string{"claude"}
	}
	return &Agent{Command: command, pool: newPool()}
}

// Provider makes claude agents. Its agents share the sessions it keeps
// between turns.
type Provider struct {
	pool *pool
}

func NewProvider() *Provider { return &Provider{pool: newPool()} }

// Agent runs s.Command here, or s.RemoteCommand on host; empty means
// `claude`.
func (p *Provider) Agent(s agent.Settings, host, dir string) agent.Agent {
	a := New(s.Command...)
	if host != "" {
		a = New(s.RemoteCommand...)
		a.Host, a.Dir = host, dir
	}
	a.pool = p.pool
	return a
}

// Close ends the sessions.
func (p *Provider) Close() { p.pool.close() }

const maxStderr = 2000

const (
	// harnessTools is how Claude names the neoharness server's MCP tools.
	harnessTools = "mcp__neoharness__"
	// permissionTool is the harness tool Claude asks before a tool call it
	// may not make on its own.
	permissionTool = harnessTools + "permission_prompt"
	// permissionMode is Claude's auto mode: it decides what is safe on its
	// own, and asks permissionTool only for the rest.
	permissionMode = "auto"
)

// env keeps Claude from giving up on a permission prompt while the person is
// away from the keyboard.
var env = []string{"MCP_TOOL_TIMEOUT=86400000"}

// Efforts are the levels `claude --effort` takes.
func (a *Agent) Efforts() []string { return []string{"low", "medium", "high", "xhigh", "max"} }

// Models are the aliases `claude --model` takes; a full model name works too.
func (a *Agent) Models() []string { return []string{"opus", "sonnet", "haiku"} }

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
	// A turn that failed before claude made the session, such as one before
	// login, can look started; claude's own files can also be gone.
	res.Lost = err == nil && res.ExitCode != 0 && req.Resume &&
		strings.Contains(res.Stderr+res.Text, "No conversation found with session ID")
	return res, err
}

func (a *Agent) run(ctx context.Context, req agent.Request) (agent.Result, error) {
	args := a.args(req)
	key := strings.Join(append(sessionArgs(args), a.Host, a.Dir, req.Cwd), "\x00")
	if s := a.pool.take(req.Conversation, key); s != nil {
		// A session can die while it waits; then a new one takes the turn.
		if res, ok := a.turn(ctx, s, req, true); ok {
			return res, nil
		}
	}
	s, err := start(a.command(args, req.Cwd), key)
	if err != nil {
		return agent.Result{}, fmt.Errorf("starting %s: %w", args[0], err)
	}
	res, _ := a.turn(ctx, s, req, false)
	return res, nil
}

// turn runs one turn in s, and gives s back to the pool if it is still up.
// ok is false when a reused session had exited before it read anything.
func (a *Agent) turn(ctx context.Context, s *session, req agent.Request, reused bool) (res agent.Result, ok bool) {
	if reused {
		// What the session wrote between turns is not this turn's.
		s.stderr.take()
	}
	acc := &accumulator{onEvent: req.OnEvent}
	// A failed write means the process is gone; the read below sees it.
	_ = s.prompt(req.Prompt)
	read, exited := 0, false
	stopped, kill := ctx.Done(), (<-chan time.Time)(nil)
	for !acc.ended && !exited {
		select {
		case line, open := <-s.lines:
			if !open {
				exited = true
				break
			}
			read++
			acc.consume(line)
		case <-stopped:
			// Claude ends the turn and keeps the process for the next one.
			stopped = nil
			_ = s.interrupt()
			kill = time.After(interruptWait)
		case <-kill:
			kill = nil
			s.kill()
		}
	}
	if exited && reused && read == 0 {
		return agent.Result{}, false
	}

	res = agent.Result{
		Text:     acc.text,
		Tools:    acc.tools,
		Tokens:   acc.tokens,
		Stderr:   trim(s.stderr.take(), maxStderr),
		Commands: acc.commands,
		Model:    acc.model,
		Context:  acc.context,
	}
	for _, m := range acc.models {
		res.ContextWindow = max(res.ContextWindow, m.ContextWindow)
	}
	if acc.cost > 0 {
		res.Cost, s.cost = acc.cost-s.cost, acc.cost
	}
	switch {
	case exited && s.exitCode() != 0:
		res.ExitCode = s.exitCode()
	case acc.isError || exited && !acc.ended:
		// Claude reports a failed turn in the result record while still
		// exiting zero.
		res.ExitCode = 1
	}
	if exited {
		s.kill()
	} else {
		a.pool.put(req.Conversation, s)
	}
	return res, true
}

// command runs claude with args here in cwd, or on the ssh host.
func (a *Agent) command(args []string, cwd string) *exec.Cmd {
	if a.Host != "" {
		ssh := sshCommand(a.Host, a.Dir, env, args)
		return exec.Command(ssh[0], ssh[1:]...)
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), env...)
	return cmd
}

// sessionArgs are the start arguments a running session must match to take
// a turn. The conversation flags differ between the first turn and the rest,
// and claude keeps the system prompt of a conversation's first request.
func sessionArgs(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--session-id", "--resume", "--append-system-prompt":
			i++
		default:
			out = append(out, args[i])
		}
	}
	return out
}

func (a *Agent) args(req agent.Request) []string {
	args := append([]string{}, a.Command...)
	// --verbose is required for stream-json to emit anything per-event; without
	// it the stream carries only the final result.
	args = append(args, "-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose")
	if h := req.Harness; h.URL != "" {
		cfg, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"neoharness": map[string]any{
			"type":    "http",
			"url":     h.URL,
			"headers": map[string]string{"Authorization": "Bearer " + h.Token},
		}}})
		args = append(args, "--permission-prompt-tool", permissionTool, "--permission-mode", permissionMode,
			"--mcp-config", string(cfg))
		if req.HarnessOnly {
			// No built-in tools and no MCP servers but ours. Without the
			// built-in ToolSearch, claude must load the harness tools up front.
			args = append(args, "--tools", "", "--strict-mcp-config")
		}
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
	// The prompt goes on stdin, as a session's turns do.
	return args
}

// accumulator folds Claude's stream into a Result.
type accumulator struct {
	onEvent  func(agent.Event)
	text     string
	tools    int
	tokens   int
	isError  bool
	ended    bool // saw the result record
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
			ID    string          `json:"id"`
			// A tool result, in a "user" record.
			ToolUseID string          `json:"tool_use_id"`
			IsError   bool            `json:"is_error"`
			Result    json.RawMessage `json:"content"`
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
					ID:     c.ID,
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

	case "user":
		// Tool results come back here. Only a failed or denied one is shown,
		// as a mark on its call; the rest stay off the screen.
		if e.Message == nil {
			return
		}
		for _, c := range e.Message.Content {
			if c.Type == "tool_result" && c.IsError {
				a.emit(agent.Event{Kind: agent.KindToolFailed, ID: c.ToolUseID, Text: firstLine(resultText(c.Result))})
			}
		}

	case "result":
		if t := e.Usage.total(); t > a.tokens {
			a.tokens = t
		}
		a.ended = true
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
}

// resultText is a tool result's content: a string, or text blocks.
func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(raw, &blocks)
	var parts []string
	for _, b := range blocks {
		parts = append(parts, b.Text)
	}
	return strings.Join(parts, "\n")
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
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
