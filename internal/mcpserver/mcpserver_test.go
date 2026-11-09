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
	"github.com/MahammadAgayev/neoharness/internal/workspace"
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
	h, err := hub.New(hub.Config{Factory: func(workspace.Workspace, workspace.Thread) agent.Agent { return idle{} }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	if err := h.CreateWorkspace("demo", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(Handler(h))
	t.Cleanup(srv.Close)
	return h, srv
}

func token(t *testing.T, name string) string {
	t.Helper()
	ths, _ := workspace.Threads("demo")
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
	if !strings.Contains(out, "api-tests (thread) in workspace demo") {
		t.Errorf("whoami = %q", out)
	}
	call(t, th, "report_progress", map[string]any{"summary": "halfway"})
	out, _ = call(t, coord, "workspace_status", nil)
	if !strings.Contains(out, "api-tests") || !strings.Contains(out, "progress: halfway") || !strings.Contains(out, "coordinator (you)") {
		t.Errorf("workspace_status = %q", out)
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

func TestReadThreadNeedsATail(t *testing.T) {
	h, srv := setup(t)
	if err := h.OpenThread("demo", model.User, "api", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := h.Send("demo", model.User, model.Coordinator, "plan the token fix"); err != nil {
		t.Fatal(err)
	}
	th := connect(t, srv, token(t, "api"))
	res, err := th.CallTool(context.Background(), &mcp.CallToolParams{Name: "read_thread", Arguments: map[string]any{"name": model.Coordinator}})
	if err == nil && !res.IsError {
		t.Fatal("read_thread without a tail should fail")
	}
	out, isErr := call(t, th, "read_thread", map[string]any{"name": model.Coordinator, "tail": 10, "query": "token"})
	if isErr || !strings.Contains(out, "message from user: plan the token fix") {
		t.Fatalf("read_thread = %q", out)
	}
}

func TestArtifactsBelongToTheWorkspace(t *testing.T) {
	h, srv := setup(t)
	coord := connect(t, srv, token(t, model.Coordinator))
	call(t, coord, "open_thread", map[string]any{"name": "api-tests", "brief": "write tests"})
	th := connect(t, srv, token(t, "api-tests"))

	out, isErr := call(t, th, "write_artifact", map[string]any{"name": "test-plan", "content": "# Test plan\n\n- unit\n"})
	if isErr || !strings.Contains(out, "version 1") {
		t.Fatalf("write_artifact = %q", out)
	}
	if out, isErr := call(t, th, "write_artifact", map[string]any{"name": "Bad Name", "content": "# x"}); !isErr {
		t.Fatalf("a bad name should be a tool error, got %q", out)
	}
	out, _ = call(t, coord, "workspace_status", nil)
	if !strings.Contains(out, `test-plan: "Test plan" by api-tests, version 1`) {
		t.Errorf("workspace_status = %q", out)
	}
	if out, _ := call(t, coord, "read_artifact", map[string]any{"name": "test-plan"}); out != "# Test plan\n\n- unit\n" {
		t.Errorf("read_artifact = %q", out)
	}
	out, _ = call(t, coord, "write_artifact", map[string]any{"name": "test-plan", "content": "# Test plan\n\n- unit\n- e2e\n"})
	if !strings.Contains(out, "version 2") {
		t.Fatalf("an update by another session = %q", out)
	}
	a, _, err := h.ReadArtifact("demo", "test-plan")
	if err != nil || a.Thread != model.Coordinator || a.Version != 2 {
		t.Fatalf("artifact = %+v, %v", a, err)
	}
}

func TestTranscriptTellsThePersonsEditApart(t *testing.T) {
	got := Transcript([]model.Event{
		{Kind: model.EventArtifact, Detail: "plan", Text: "Plan", Version: 1},
		{Kind: model.EventArtifact, From: model.User, Detail: "plan", Text: "Plan", Version: 2},
	})
	if !strings.Contains(got, `wrote artifact plan ("Plan"), version 1`) || !strings.Contains(got, "the person edited artifact plan, version 2") {
		t.Fatalf("transcript = %q", got)
	}
}

func TestArtifactListShowsThePersonsEdit(t *testing.T) {
	got := Artifacts([]model.Artifact{{Name: "plan", Title: "Plan", Thread: "api", Version: 3, UserEdit: true}})
	if !strings.Contains(got, `- plan: "Plan" by api, version 3, edited by the person`) {
		t.Fatalf("artifacts = %q", got)
	}
}
