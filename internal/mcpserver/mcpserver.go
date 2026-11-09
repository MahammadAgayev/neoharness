// Package mcpserver exposes the hub to Claude sessions as the `neoharness` MCP
// server: status, messaging, progress, questions, permission prompts, and the
// coordinator's thread tools.
//
// Every session sends its thread's token as a bearer token, and the token
// alone decides who is calling. Tools act on the caller's own project and
// thread, so no tool takes a project argument and a thread cannot speak for
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
	Name     string `json:"name" jsonschema:"short kebab-case name, unique in the project"`
	Brief    string `json:"brief" jsonschema:"the task: goal, area, what done means, who to agree with"`
	Location string `json:"location,omitempty" jsonschema:"a local folder or host:dir; empty for the project folder"`
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
	p, me := id.Project, id.Thread

	mcp.AddTool(s, &mcp.Tool{Name: "whoami", Description: "Your name, role, project and location."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
			v, err := h.Whoami(p, me)
			if err != nil {
				return nil, nil, err
			}
			return text(fmt.Sprintf("You are %s (%s) in project %s, running in %s.", v.Name, v.Role, v.Project, v.Location)), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "project_status", Description: "Every thread in the project: state, last progress report, last update."},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
			vs, err := h.Threads(p)
			if err != nil {
				return nil, nil, err
			}
			return text(Status(vs, me, time.Now())), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "send_message", Description: "Send a message to another thread or to the coordinator. It arrives as their next turn; replies come back to you the same way."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in sendIn) (*mcp.CallToolResult, any, error) {
			if err := h.Send(p, me, in.To, in.Text); err != nil {
				return nil, nil, err
			}
			return text("Sent to " + in.To + "."), nil, nil
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
			ok, msg := h.Permission(ctx, p, me, in.ToolName, input)
			return text(permissionReply(ok, msg, input)), nil, nil
		})

	if id.Role != model.RoleCoordinator {
		return s
	}

	mcp.AddTool(s, &mcp.Tool{Name: "open_thread", Description: "Open a new thread: a separate Claude Code session that starts on the brief at once."},
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

// Status renders project_status as plain text, one thread per line.
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

func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}
