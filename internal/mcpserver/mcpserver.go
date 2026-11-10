// Package mcpserver exposes the hub to agent sessions as the `neoharness` MCP
// server: status, messaging, progress, questions, permission prompts, and the
// coordinator's thread tools.
//
// Every session sends its thread's token as a bearer token, and the token
// alone decides who is calling. Tools act on the caller's own workspace and
// thread, so no tool takes a workspace argument and a thread cannot speak for
// another one.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MahammadAgayev/neoharness/internal/agent/claude"
	"github.com/MahammadAgayev/neoharness/internal/hub"
	"github.com/MahammadAgayev/neoharness/internal/model"
)

// Handler serves MCP over streamable HTTP.
func Handler(h *hub.Hub) http.Handler {
	var mu sync.Mutex
	servers := map[string]*mcp.Server{}

	mcpHandler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		token := bearer(r)
		id, ok := h.Identify(token)
		if !ok {
			return nil
		}
		mu.Lock()
		defer mu.Unlock()
		if s, ok := servers[token]; ok {
			return s
		}
		s := newServer(h, id)
		servers[token] = s
		return s
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := h.Identify(bearer(r)); !ok {
			http.Error(w, "unknown token", http.StatusUnauthorized)
			return
		}
		mcpHandler.ServeHTTP(w, r)
	})
}

func bearer(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}

type empty struct{}

type sendIn struct {
	To   string `json:"to" jsonschema:"thread name, or coordinator"`
	Text string `json:"text" jsonschema:"the message"`
}

type progressIn struct {
	Summary string `json:"summary" jsonschema:"one line: where you are"`
}

type askIn struct {
	Question string `json:"question" jsonschema:"the question for the person"`
}

type openIn struct {
	Name     string `json:"name" jsonschema:"short kebab-case name, unique in the workspace"`
	Brief    string `json:"brief" jsonschema:"the task: goal, area, what done means, who to agree with"`
	Location string `json:"location,omitempty" jsonschema:"a local folder or host:dir; empty for the workspace folder"`
}

type readIn struct {
	Name  string `json:"name" jsonschema:"thread to read, or coordinator"`
	Tail  int    `json:"tail" jsonschema:"how many of its last events to return, 1 to 200"`
	Query string `json:"query,omitempty" jsonschema:"only events that contain this text; empty for all"`
}

type artifactIn struct {
	Name    string `json:"name" jsonschema:"short kebab-case name; the same name replaces that artifact with a new version"`
	Content string `json:"content" jsonschema:"the whole document in markdown; start with a # title"`
}

type artifactNameIn struct {
	Name string `json:"name" jsonschema:"the artifact to read"`
}

type resolveIn struct {
	Name string `json:"name" jsonschema:"thread to close"`
}

type permissionIn struct {
	ToolName  string         `json:"tool_name"`
	Input     map[string]any `json:"input"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
}

func newServer(h *hub.Hub, id hub.Identity) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "neoharness", Version: "1"}, nil)
	p, me := id.Workspace, id.Thread

	mcp.AddTool(s, &mcp.Tool{Name: "whoami", Description: "Your name, role, workspace and location."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
			v, err := h.Whoami(p, me)
			if err != nil {
				return nil, nil, err
			}
			return text(fmt.Sprintf("You are %s (%s) in workspace %s, running in %s.", v.Name, v.Role, v.Workspace, v.Location)), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "workspace_status", Description: "Every thread in the workspace: state, last progress report, last update. Also the workspace's artifacts."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
			vs, err := h.Threads(p)
			if err != nil {
				return nil, nil, err
			}
			as, err := h.Artifacts(p)
			if err != nil {
				return nil, nil, err
			}
			return text(Status(vs, me, time.Now()) + Artifacts(as)), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "write_artifact", Description: "Write a markdown document for the person to read: a plan, design, report, or summary longer than a screen. The person opens it beside your chat. Writing the same name again replaces it with a new version; send the whole document each time."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in artifactIn) (*mcp.CallToolResult, any, error) {
			a, err := h.WriteArtifact(p, me, in.Name, in.Content)
			if err != nil {
				return nil, nil, err
			}
			return text(fmt.Sprintf("Saved artifact %s, version %d. The person opens it from your chat; do not repeat it in your reply.", a.Name, a.Version)), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "read_artifact", Description: "Read an artifact of this workspace, by name. workspace_status lists them."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in artifactNameIn) (*mcp.CallToolResult, any, error) {
			body, err := h.ReadArtifactAs(p, me, in.Name)
			if err != nil {
				return nil, nil, err
			}
			return text(body), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "send_message", Description: "Send a message to another thread or to the coordinator. It arrives as their next turn; replies come back to you the same way."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in sendIn) (*mcp.CallToolResult, any, error) {
			if err := h.Send(p, me, in.To, in.Text); err != nil {
				return nil, nil, err
			}
			return text("Sent to " + in.To + "."), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "read_thread", Description: "Read the transcript of a thread in this workspace: messages, replies, and tool calls, oldest first. Ask for only as much as you need."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in readIn) (*mcp.CallToolResult, any, error) {
			evs, err := h.ReadThread(p, in.Name, in.Tail, in.Query)
			if err != nil {
				return nil, nil, err
			}
			return text(Transcript(evs)), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "report_progress", Description: "Tell the coordinator and the person where you are, in one line."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in progressIn) (*mcp.CallToolResult, any, error) {
			if err := h.ReportProgress(p, me, in.Summary); err != nil {
				return nil, nil, err
			}
			return text("Reported."), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "ask_user", Description: "Ask the person a question you cannot continue without. End your turn after; the answer comes as your next message."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in askIn) (*mcp.CallToolResult, any, error) {
			if err := h.AskUser(p, me, in.Question); err != nil {
				return nil, nil, err
			}
			return text("Question posted. End your turn now; the answer arrives as your next message."), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "permission_prompt", Description: "Used by Claude Code to ask the person for permission. Do not call it yourself."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in permissionIn) (*mcp.CallToolResult, any, error) {
			input, _ := json.Marshal(in.Input)
			ok, msg := true, ""
			if !claude.HarnessTool(in.ToolName) {
				ok, msg = h.Permission(ctx, p, me, claude.Ask(in.ToolName, input))
			}
			return text(permissionReply(ok, msg, input)), nil, nil
		})

	if id.Role != model.RoleCoordinator {
		return s
	}

	mcp.AddTool(s, &mcp.Tool{Name: "open_thread", Description: "Open a new thread: a separate agent session that starts on the brief at once."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in openIn) (*mcp.CallToolResult, any, error) {
			if err := h.OpenThread(p, me, in.Name, in.Brief, in.Location); err != nil {
				return nil, nil, err
			}
			return text("Opened " + in.Name + "."), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "resolve_thread", Description: "Close a thread whose work is finished. The person can reopen it."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in resolveIn) (*mcp.CallToolResult, any, error) {
			if err := h.Resolve(p, me, in.Name); err != nil {
				return nil, nil, err
			}
			return text("Resolved " + in.Name + "."), nil, nil
		})
	return s
}

// permissionReply is the JSON Claude Code expects back from a permission
// prompt tool. An allow must echo the input, which Claude then uses as the
// tool's actual input.
func permissionReply(ok bool, msg string, input json.RawMessage) string {
	var reply map[string]any
	if ok {
		var in any = map[string]any{}
		if len(input) > 0 {
			_ = json.Unmarshal(input, &in)
		}
		if in == nil {
			in = map[string]any{}
		}
		reply = map[string]any{"behavior": "allow", "updatedInput": in}
	} else {
		if msg == "" {
			msg = "The person denied this."
		}
		reply = map[string]any{"behavior": "deny", "message": msg}
	}
	data, _ := json.Marshal(reply)
	return string(data)
}

// Status renders workspace_status as plain text, one thread per line.
func Status(vs []model.ThreadView, me string, now time.Time) string {
	var b strings.Builder
	for _, v := range vs {
		name := v.Name
		if name == me {
			name += " (you)"
		}
		fmt.Fprintf(&b, "- %s: %s", name, v.State)
		if v.Queued > 0 {
			fmt.Fprintf(&b, ", %d queued", v.Queued)
		}
		if v.Waiting != "" {
			fmt.Fprintf(&b, ", waiting on: %s", v.Waiting)
		}
		if v.Progress != "" {
			fmt.Fprintf(&b, " | progress: %s", v.Progress)
		}
		fmt.Fprintf(&b, " | updated %s ago\n", now.Sub(v.UpdatedAt).Round(time.Second))
	}
	return b.String()
}

// Artifacts renders the artifact list of workspace_status.
func Artifacts(as []model.Artifact) string {
	if len(as) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nArtifacts:\n")
	for _, a := range as {
		fmt.Fprintf(&b, "- %s: %q by %s, version %d", a.Name, a.Title, a.Thread, a.Version)
		if a.UserEdit {
			b.WriteString(", edited by the person")
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// Transcript renders events as plain text, one per line.
func Transcript(evs []model.Event) string {
	if len(evs) == 0 {
		return "No events."
	}
	var b strings.Builder
	tools := map[string]string{}
	for _, e := range evs {
		fmt.Fprintf(&b, "%s ", e.At.Format("15:04"))
		switch e.Kind {
		case model.EventMessage:
			fmt.Fprintf(&b, "message from %s: %s", e.From, e.Text)
		case model.EventText:
			fmt.Fprintf(&b, "reply: %s", e.Text)
		case model.EventTool:
			if e.Exit != 0 {
				b.WriteString("the tool call failed")
				if t := tools[e.ID]; t != "" {
					fmt.Fprintf(&b, ": %s", t)
				}
				if e.Text != "" {
					fmt.Fprintf(&b, ": %s", e.Text)
				}
				break
			}
			if e.ID != "" {
				tools[e.ID] = e.Tool
			}
			fmt.Fprintf(&b, "tool %s: %s", e.Tool, e.Detail)
		case model.EventPermission:
			fmt.Fprintf(&b, "permission asked for %s: %s", e.Tool, e.Detail)
		case model.EventAnswer:
			fmt.Fprintf(&b, "permission for %s: %s", e.Tool, e.Text)
		case model.EventTurnEnd:
			fmt.Fprintf(&b, "turn ended, %d tool calls", e.Tools)
		case model.EventArtifact:
			if e.From == model.User {
				fmt.Fprintf(&b, "the person edited artifact %s, version %d", e.Detail, e.Version)
			} else {
				fmt.Fprintf(&b, "wrote artifact %s (%q), version %d", e.Detail, e.Text, e.Version)
			}
		case model.EventShell:
			if e.Text == "" && e.Exit == 0 {
				fmt.Fprintf(&b, "the person started ! %s", e.Detail)
			} else {
				fmt.Fprintf(&b, "the person ran ! %s, exit %d", e.Detail, e.Exit)
			}
		default:
			fmt.Fprintf(&b, "%s: %s", e.Kind, e.Text)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}
