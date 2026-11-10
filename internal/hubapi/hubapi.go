// Package hubapi is the client side of the hub's local API: HTTP over a unix
// socket, with server-sent events for live updates.
//
// The TUI talks to the hub only through this package. It holds no hub state of
// its own, so closing and reopening the TUI loses nothing.
package hubapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/MahammadAgayev/neoharness/internal/model"
)

// SocketPath is where the hub listens. It follows NEOHARNESS_HOME so tests and
// alternate homes get their own hub.
func SocketPath() string {
	if h := os.Getenv("NEOHARNESS_HOME"); h != "" {
		return filepath.Join(h, "hub.sock")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".neoharness", "hub.sock")
	}
	return filepath.Join(home, ".neoharness", "hub.sock")
}

// Request bodies, shared with the server.
type (
	NewWorkspace struct {
		Name string `json:"name"`
		Pin  string `json:"pin,omitempty"`
	}
	NewThread struct {
		Name     string `json:"name"`
		Brief    string `json:"brief,omitempty"`
		Location string `json:"location,omitempty"`
	}
	Message struct {
		Text string `json:"text"`
		// Now stops the running turn so the message goes at once.
		Now bool `json:"now,omitempty"`
	}
	Folder struct {
		Pin string `json:"pin"`
	}
	ArtifactText struct {
		Artifact model.Artifact `json:"artifact"`
		Text     string         `json:"text"`
	}
	ArtifactEdit struct {
		Text string `json:"text"`
		// Base and BaseText are the version the edit started from, for a
		// merge when the artifact moved on.
		Base     int    `json:"base"`
		BaseText string `json:"baseText"`
		// Force saves over a newer version.
		Force bool `json:"force,omitempty"`
	}
	Answer struct {
		ID       string         `json:"id,omitempty"`
		Decision model.Decision `json:"decision"`
		Message  string         `json:"message,omitempty"`
	}
	Error struct {
		Error string `json:"error"`
	}
)

// Client talks to a hub.
type Client struct {
	http *http.Client
}

// New returns a client for the hub listening on socket.
func New(socket string) *Client {
	return &Client{http: &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
	}}}
}

// Ping reports whether a hub answers.
func (c *Client) Ping() error {
	_, err := c.Workspaces()
	return err
}

func (c *Client) Workspaces() ([]model.WorkspaceView, error) {
	var out []model.WorkspaceView
	return out, c.do("GET", "/workspaces", nil, &out)
}

func (c *Client) CreateWorkspace(name, pin string) error {
	return c.do("POST", "/workspaces", NewWorkspace{Name: name, Pin: pin}, nil)
}

func (c *Client) SetFolder(name, pin string) error {
	return c.do("PUT", "/workspaces/"+esc(name)+"/folder", Folder{Pin: pin}, nil)
}

func (c *Client) DeleteWorkspace(name string) error {
	return c.do("DELETE", "/workspaces/"+esc(name), nil, nil)
}

func (c *Client) DeletedWorkspaces() ([]string, error) {
	var out []string
	return out, c.do("GET", "/deleted", nil, &out)
}

func (c *Client) RestoreWorkspace(name string) error {
	return c.do("POST", "/workspaces/"+esc(name)+"/restore", nil, nil)
}

func (c *Client) Artifacts(workspace string) ([]model.Artifact, error) {
	var out []model.Artifact
	return out, c.do("GET", "/workspaces/"+esc(workspace)+"/artifacts", nil, &out)
}

func (c *Client) Artifact(workspace, name string) (model.Artifact, string, error) {
	var out ArtifactText
	err := c.do("GET", "/workspaces/"+esc(workspace)+"/artifacts/"+esc(name), nil, &out)
	return out.Artifact, out.Text, err
}

func (c *Client) EditArtifact(workspace, name, text, baseText string, base int, force bool) (model.Artifact, string, error) {
	var out ArtifactText
	err := c.do("PUT", "/workspaces/"+esc(workspace)+"/artifacts/"+esc(name), ArtifactEdit{Text: text, Base: base, BaseText: baseText, Force: force}, &out)
	return out.Artifact, out.Text, err
}

func (c *Client) Threads(workspace string) ([]model.ThreadView, error) {
	var out []model.ThreadView
	return out, c.do("GET", "/workspaces/"+esc(workspace)+"/threads", nil, &out)
}

func (c *Client) OpenThread(workspace, name, brief, location string) error {
	return c.do("POST", "/workspaces/"+esc(workspace)+"/threads", NewThread{Name: name, Brief: brief, Location: location}, nil)
}

func (c *Client) Events(workspace, thread string, tail int) ([]model.Event, error) {
	var out []model.Event
	return out, c.do("GET", fmt.Sprintf("%s/events?tail=%d", threadPath(workspace, thread), tail), nil, &out)
}

func (c *Client) Completions(workspace, thread string) (model.Completions, error) {
	var out model.Completions
	return out, c.do("GET", threadPath(workspace, thread)+"/completions", nil, &out)
}

func (c *Client) Send(workspace, thread, text string) error {
	return c.do("POST", threadPath(workspace, thread)+"/messages", Message{Text: text}, nil)
}

func (c *Client) SendNow(workspace, thread, text string) error {
	return c.do("POST", threadPath(workspace, thread)+"/messages", Message{Text: text, Now: true}, nil)
}

func (c *Client) Resolve(workspace, thread string) error {
	return c.do("POST", threadPath(workspace, thread)+"/resolve", nil, nil)
}

func (c *Client) Delete(workspace, thread string) error {
	return c.do("DELETE", threadPath(workspace, thread), nil, nil)
}

func (c *Client) Reopen(workspace, thread string) error {
	return c.do("POST", threadPath(workspace, thread)+"/reopen", nil, nil)
}

func (c *Client) Answer(workspace, thread, id string, d model.Decision, msg string) error {
	return c.do("POST", threadPath(workspace, thread)+"/answer", Answer{ID: id, Decision: d, Message: msg}, nil)
}

// Updates streams hub updates until ctx ends or the connection drops. The
// channel is closed when the stream ends; the caller reconnects and reloads.
func (c *Client) Updates(ctx context.Context) (<-chan model.Update, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "http://hub/updates", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("updates: %s", resp.Status)
	}
	ch := make(chan model.Update, 256)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
		for sc.Scan() {
			line, ok := strings.CutPrefix(sc.Text(), "data: ")
			if !ok {
				continue
			}
			var u model.Update
			if json.Unmarshal([]byte(line), &u) != nil {
				continue
			}
			select {
			case ch <- u:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

func (c *Client) do(method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, "http://hub"+path, rd)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e Error
		if json.NewDecoder(resp.Body).Decode(&e) == nil && e.Error != "" {
			return errors.New(e.Error)
		}
		return fmt.Errorf("%s %s: %s", method, path, resp.Status)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func threadPath(workspace, thread string) string {
	return "/workspaces/" + esc(workspace) + "/threads/" + esc(thread)
}

func esc(s string) string { return url.PathEscape(s) }
