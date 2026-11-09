package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MahammadAgayev/neoharness/internal/agent"
	"github.com/MahammadAgayev/neoharness/internal/hub"
	"github.com/MahammadAgayev/neoharness/internal/model"
	"github.com/MahammadAgayev/neoharness/internal/project"
)

type idle struct{}

func (idle) Name() string { return "idle" }
func (idle) Run(ctx context.Context, req agent.Request) (agent.Result, error) {
	return agent.Result{Text: "ok"}, nil
}

type withToken struct {
	token string
	next  http.RoundTripper
}

func (w withToken) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+w.token)
	return w.next.RoundTrip(r)
}

func setup(t *testing.T) (*hub.Hub, *httptest.Server) {
	t.Helper()
	t.Setenv("NEOHARNESS_HOME", t.TempDir())
	h, err := hub.New(hub.Config{Factory: func(project.Project, project.Thread) agent.Agent { return idle{} }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	if err := h.CreateProject("demo", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(Handler(h))
	t.Cleanup(srv.Close)
	return h, srv
}

func token(t *testing.T, name string) string {
	t.Helper()
	ths, _ := project.Threads("demo")
	for _, th := range ths {
		if th.Name == name {
			return th.Token
		}
	}
	t.Fatalf("no thread %s", name)
	return ""
}

func connect(t *testing.T, srv *httptest.Server, tok string) *mcp.ClientSession {
	t.Helper()
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	s, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:   srv.URL,
		HTTPClient: &http.Client{Transport: withToken{tok, http.DefaultTransport}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func call(t *testing.T, s *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

func toolNames(t *testing.T, s *mcp.ClientSession) []string {
	t.Helper()
	res, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, tool := range res.Tools {
		out = append(out, tool.Name)
	}
	return out
}

func TestUnknownTokenIsRejected(t *testing.T) {
	_, srv := setup(t)
	req, _ := http.NewRequest("POST", srv.URL, strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer nope")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestCoordinatorOpensThreadsAndThreadsCannot(t *testing.T) {
	h, srv := setup(t)
	coord := connect(t, srv, token(t, model.Coordinator))
	if names := toolNames(t, coord); !slices.Contains(names, "open_thread") || !slices.Contains(names, "resolve_thread") {
		t.Fatalf("coordinator tools = %v", names)
	}
	if out, isErr := call(t, coord, "open_thread", map[string]any{"name": "api-tests", "brief": "write tests"}); isErr {
		t.Fatalf("open_thread failed: %s", out)
	}
	if out, isErr := call(t, coord, "open_thread", map[string]any{"name": "api-tests", "brief": "again"}); !isErr || !strings.Contains(out, "already exists") {
		t.Fatalf("duplicate name should be a tool error, got %q", out)
	}

	th := connect(t, srv, token(t, "api-tests"))
	names := toolNames(t, th)
	if slices.Contains(names, "open_thread") || slices.Contains(names, "resolve_thread") {
		t.Fatalf("a thread must not get coordinator tools: %v", names)
	}
	out, _ := call(t, th, "whoami", nil)
	if !strings.Contains(out, "api-tests (thread) in project demo") {
		t.Errorf("whoami = %q", out)
	}
	call(t, th, "report_progress", map[string]any{"summary": "halfway"})
	out, _ = call(t, coord, "project_status", nil)
	if !strings.Contains(out, "api-tests") || !strings.Contains(out, "progress: halfway") || !strings.Contains(out, "coordinator (you)") {
		t.Errorf("project_status = %q", out)
	}
	if out, isErr := call(t, th, "send_message", map[string]any{"to": "coordinator", "text": "done?"}); isErr {
		t.Fatalf("send_message: %s", out)
	}
	_ = h
}

func TestPermissionPromptRoundTrip(t *testing.T) {
	h, srv := setup(t)
	coord := connect(t, srv, token(t, model.Coordinator))

	go func() {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			vs, _ := h.Threads("demo")
			if vs[0].Permission != nil {
				_ = h.Answer("demo", model.Coordinator, vs[0].Permission.ID, model.Allow, "")
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	out, _ := call(t, coord, "permission_prompt", map[string]any{
		"tool_name": "Bash",
		"input":     map[string]any{"command": "ls"},
	})
	var reply struct {
		Behavior     string         `json:"behavior"`
		UpdatedInput map[string]any `json:"updatedInput"`
	}
	if err := json.Unmarshal([]byte(out), &reply); err != nil {
		t.Fatalf("reply %q: %v", out, err)
	}
	if reply.Behavior != "allow" || reply.UpdatedInput["command"] != "ls" {
		t.Fatalf("reply = %+v", reply)
	}
}

func TestPermissionReplyDeny(t *testing.T) {
	got := permissionReply(false, "", nil)
	if got != `{"behavior":"deny","message":"The person denied this."}` {
		t.Errorf("deny reply = %s", got)
	}
}
