// Package project stores projects, their threads, and thread transcripts on
// disk under ~/.neoharness/projects/.
//
// The hub is the only writer. Files are written whole through a temp file and
// a rename, so a crash leaves either the old version or the new one, never a
// torn file. Transcripts are append-only JSON lines.
package project

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/MahammadAgayev/neoharness/internal/model"
)

// Project is a workspace: a name and an optional local folder it is pinned to.
type Project struct {
	Name      string    `json:"name"`
	Pin       string    `json:"pin,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// Message is one queued input for a thread.
type Message struct {
	From string    `json:"from"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

// Rule is an "allow always" answer. An empty Prefix matches every call of the
// tool; for Bash it holds the command's first word, so allowing `go test`
// once also allows `go build` but not `rm`.
type Rule struct {
	Tool   string `json:"tool"`
	Prefix string `json:"prefix,omitempty"`
}

// Thread is the persisted part of a thread. The visible state is derived from
// these fields by the hub, not stored, so it cannot drift from them.
type Thread struct {
	Name     string         `json:"name"`
	Role     model.Role     `json:"role"`
	Location model.Location `json:"location"`
	// SessionID is the Claude session this thread resumes. Started says
	// whether a turn has created it yet.
	SessionID string `json:"sessionId"`
	Started   bool   `json:"started"`
	// Token identifies this thread to the MCP server.
	Token string `json:"token"`

	Resolved bool   `json:"resolved,omitempty"`
	Asking   string `json:"asking,omitempty"`
	Paused   bool   `json:"paused,omitempty"`

	Progress   string    `json:"progress,omitempty"`
	ProgressAt time.Time `json:"progressAt,omitzero"`

	Queue []Message `json:"queue"`
	// InFlight is the batch the current turn is working on. Non-empty on load
	// means the hub died mid-turn.
	InFlight []Message `json:"inFlight,omitempty"`

	Rules []Rule `json:"rules,omitempty"`
	// AgentTurns counts turns in a row that no user message started.
	AgentTurns int `json:"agentTurns,omitempty"`
	// Turns holds the start times of recent turns, for the activity line.
	Turns []time.Time `json:"turns,omitempty"`

	Since     time.Time `json:"since"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Root is the folder that holds every project. It follows NEOHARNESS_HOME like
// the run folders do.
func Root() string {
	if h := os.Getenv("NEOHARNESS_HOME"); h != "" {
		return filepath.Join(h, "projects")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".neoharness", "projects")
	}
	return filepath.Join(home, ".neoharness", "projects")
}

// Dir is a project's folder.
func Dir(name string) string { return filepath.Join(Root(), name) }

func projectFile(name string) string { return filepath.Join(Dir(name), "project.json") }

// ThreadDir is a thread's folder.
func ThreadDir(project, thread string) string {
	return filepath.Join(Dir(project), "threads", thread)
}

func threadFile(project, thread string) string {
	return filepath.Join(ThreadDir(project, thread), "thread.json")
}

func eventsFile(project, thread string) string {
	return filepath.Join(ThreadDir(project, thread), "events.jsonl")
}

// Create writes a new project. It fails if one of that name exists.
func Create(p Project) error {
	if err := model.ValidName(p.Name); err != nil {
		return err
	}
	if _, err := os.Stat(projectFile(p.Name)); err == nil {
		return fmt.Errorf("project %q already exists", p.Name)
	}
	if err := os.MkdirAll(filepath.Join(Dir(p.Name), "threads"), 0o755); err != nil {
		return err
	}
	return writeJSON(projectFile(p.Name), p, 0o644)
}

// List returns every project, oldest first. A damaged project is skipped so
// one bad folder does not hide the rest.
func List() ([]Project, error) {
	entries, err := os.ReadDir(Root())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Project
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		var p Project
		if err := readJSON(projectFile(e.Name()), &p); err != nil {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// Threads loads every thread of a project.
func Threads(project string) ([]*Thread, error) {
	entries, err := os.ReadDir(filepath.Join(Dir(project), "threads"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Thread
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		var t Thread
		if err := readJSON(threadFile(project, e.Name()), &t); err != nil {
			continue
		}
		out = append(out, &t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// SaveThread writes a thread. 0600 because the file holds the thread's MCP
// token.
func SaveThread(project string, t *Thread) error {
	if err := os.MkdirAll(ThreadDir(project, t.Name), 0o755); err != nil {
		return err
	}
	t.UpdatedAt = time.Now().UTC()
	return writeJSON(threadFile(project, t.Name), t, 0o600)
}

// AppendEvent adds one event to a thread's transcript.
func AppendEvent(project, thread string, e model.Event) error {
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(eventsFile(project, thread), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

// Events reads the last n events of a transcript; n <= 0 reads all. A
// truncated last line, left by a crash mid-write, is skipped.
func Events(project, thread string, n int) ([]model.Event, error) {
	f, err := os.Open(eventsFile(project, thread))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []model.Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var e model.Event
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		out = append(out, e)
	}
	if n > 0 && len(out) > n {
		out = out[len(out)-n:]
	}
	return out, sc.Err()
}

// NewID returns a random UUID v4, the form Claude requires for --session-id.
func NewID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// NewToken returns a random secret for MCP authentication.
func NewToken() string {
	var b [24]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func writeJSON(path string, v any, mode os.FileMode) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*.json")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		os.Remove(name)
		return err
	}
	return os.Rename(name, path)
}
