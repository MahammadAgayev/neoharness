// Package workspace stores workspaces, their threads, and thread transcripts on
// disk under ~/.neoharness/workspaces/.
//
// The hub is the only writer. Files are written whole through a temp file and
// a rename, so a crash leaves either the old version or the new one, never a
// torn file. Transcripts are append-only JSON lines.
package workspace

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

// Workspace is a workspace: a name and an optional local folder it is pinned to.
type Workspace struct {
	Name      string    `json:"name"`
	Pin       string    `json:"pin,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	// Deleted hides the workspace. Its data stays, so the delete can be
	// undone by hand.
	Deleted bool `json:"deleted,omitempty"`
	// Coordinator and Threads are agents :setlocal chose for the roles here;
	// empty runs the config's.
	Coordinator string `json:"coordinator,omitempty"`
	Threads     string `json:"threads,omitempty"`
}

// Message is one queued input for a thread.
type Message struct {
	From string    `json:"from"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
	// Compact marks a /compact request; Text is what to focus on.
	Compact bool `json:"compact,omitempty"`
	// Shown says the message is in the transcript: it goes there when the
	// turn that reads it starts, not when it is queued.
	Shown bool `json:"shown,omitempty"`
}

// Edit is an artifact as a thread last saw it.
type Edit struct {
	Version int    `json:"version"`
	Text    string `json:"text"`
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
	// WakeAt is when the thread asked to be woken, with WakeNote as the
	// reason; zero is never.
	WakeAt   time.Time `json:"wakeAt,omitzero"`
	WakeNote string    `json:"wakeNote,omitempty"`

	Queue []Message `json:"queue"`
	// InFlight is the batch the current turn is working on. Non-empty on load
	// means the hub died mid-turn.
	InFlight []Message `json:"inFlight,omitempty"`

	Rules []Rule `json:"rules,omitempty"`
	// AgentTurns counts turns in a row that no user message started.
	AgentTurns int `json:"agentTurns,omitempty"`
	// Turns holds the start times of recent turns, for the activity line.
	Turns []time.Time `json:"turns,omitempty"`
	// Commands is the slash commands its last turn reported.
	Commands []string `json:"commands,omitempty"`
	// Model and Context are as of its last turn; Cost adds up every turn.
	Model         string  `json:"model,omitempty"`
	Context       int     `json:"context,omitempty"`
	ContextWindow int     `json:"contextWindow,omitempty"`
	Cost          float64 `json:"cost,omitempty"`
	// ModelChoice and Effort are what /model and /effort chose; empty is
	// Claude's default.
	ModelChoice string `json:"modelChoice,omitempty"`
	Effort      string `json:"effort,omitempty"`
	// Summary is what /compact left for the next session's first message.
	Summary string `json:"summary,omitempty"`
	// Shell holds `!` commands and their output for the next turn's prompt.
	Shell []string `json:"shell,omitempty"`
	// Edits holds, per artifact the thread wrote, the version it last saw
	// before the person edited it. The next turn's prompt gets the diff.
	Edits map[string]Edit `json:"edits,omitempty"`

	Since     time.Time `json:"since"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Root is the folder that holds every workspace. It follows NEOHARNESS_HOME like
// the run folders do.
func Root() string {
	if h := os.Getenv("NEOHARNESS_HOME"); h != "" {
		return filepath.Join(h, "workspaces")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".neoharness", "workspaces")
	}
	return filepath.Join(home, ".neoharness", "workspaces")
}

// Migrate moves data from before workspaces were called projects: the
// projects folder becomes the workspaces folder and each project.json becomes
// workspace.json. A projects symlink stays behind, because an unpinned
// coordinator runs inside that folder and Claude finds its session by the
// path it ran in.
func Migrate() error {
	root := Root()
	old := filepath.Join(filepath.Dir(root), "projects")
	fi, err := os.Lstat(old)
	if err != nil || !fi.IsDir() {
		return nil
	}
	if _, err := os.Stat(root); err == nil {
		return fmt.Errorf("both %s and %s exist; move your data into one", old, root)
	}
	if err := os.Rename(old, root); err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		from := filepath.Join(root, e.Name(), "project.json")
		if _, err := os.Stat(from); err == nil {
			if err := os.Rename(from, workspaceFile(e.Name())); err != nil {
				return err
			}
		}
	}
	return linkDir(filepath.Base(root), old)
}

// Dir is a workspace's folder.
func Dir(name string) string { return filepath.Join(Root(), name) }

func workspaceFile(name string) string { return filepath.Join(Dir(name), "workspace.json") }

// ThreadDir is a thread's folder.
func ThreadDir(workspace, thread string) string {
	return filepath.Join(Dir(workspace), "threads", thread)
}

func artifactDir(workspace string) string { return filepath.Join(Dir(workspace), "artifacts") }

// SaveArtifact writes an artifact's text and summary.
func SaveArtifact(workspace string, a model.Artifact, text string) error {
	dir := artifactDir(workspace)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, a.Name+".md"), []byte(text), 0o644); err != nil {
		return err
	}
	return writeJSON(filepath.Join(dir, a.Name+".json"), a, 0o644)
}

// Artifact reads an artifact's summary and text. A missing one is
// os.ErrNotExist.
func Artifact(workspace, name string) (model.Artifact, string, error) {
	var a model.Artifact
	if err := readJSON(filepath.Join(artifactDir(workspace), name+".json"), &a); err != nil {
		return a, "", err
	}
	text, err := os.ReadFile(filepath.Join(artifactDir(workspace), name+".md"))
	return a, string(text), err
}

// Artifacts lists a workspace's artifacts, newest first.
func Artifacts(workspace string) ([]model.Artifact, error) {
	paths, err := filepath.Glob(filepath.Join(artifactDir(workspace), "*.json"))
	if err != nil {
		return nil, err
	}
	out := []model.Artifact{}
	for _, p := range paths {
		var a model.Artifact
		if readJSON(p, &a) == nil {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

func threadFile(workspace, thread string) string {
	return filepath.Join(ThreadDir(workspace, thread), "thread.json")
}

func eventsFile(workspace, thread string) string {
	return filepath.Join(ThreadDir(workspace, thread), "events.jsonl")
}

// Create writes a new workspace. It fails if one of that name exists.
func Create(p Workspace) error {
	if err := model.ValidName(p.Name); err != nil {
		return err
	}
	if _, err := os.Stat(workspaceFile(p.Name)); err == nil {
		return fmt.Errorf("workspace %q already exists", p.Name)
	}
	if err := os.MkdirAll(filepath.Join(Dir(p.Name), "threads"), 0o755); err != nil {
		return err
	}
	return writeJSON(workspaceFile(p.Name), p, 0o644)
}

// Save writes an existing workspace.
func Save(p Workspace) error {
	return writeJSON(workspaceFile(p.Name), p, 0o644)
}

// List returns every workspace, oldest first. A damaged workspace is skipped so
// one bad folder does not hide the rest.
func List() ([]Workspace, error) {
	entries, err := os.ReadDir(Root())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Workspace
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		var p Workspace
		if err := readJSON(workspaceFile(e.Name()), &p); err != nil {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// Threads loads every thread of a workspace.
func Threads(workspace string) ([]*Thread, error) {
	entries, err := os.ReadDir(filepath.Join(Dir(workspace), "threads"))
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
		if err := readJSON(threadFile(workspace, e.Name()), &t); err != nil {
			continue
		}
		out = append(out, &t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// SaveThread writes a thread. 0600 because the file holds the thread's MCP
// token.
func SaveThread(workspace string, t *Thread) error {
	if err := os.MkdirAll(ThreadDir(workspace, t.Name), 0o755); err != nil {
		return err
	}
	t.UpdatedAt = time.Now().UTC()
	return writeJSON(threadFile(workspace, t.Name), t, 0o600)
}

// DeleteThread removes a thread's folder: its state and transcript.
func DeleteThread(workspace, thread string) error {
	return os.RemoveAll(ThreadDir(workspace, thread))
}

// AppendEvent adds one event to a thread's transcript.
func AppendEvent(workspace, thread string, e model.Event) error {
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(eventsFile(workspace, thread), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

func spendFile(workspace string) string { return filepath.Join(Dir(workspace), "spend.jsonl") }

// AppendSpend adds a paid turn to the workspace's spend log.
func AppendSpend(workspace string, c model.TurnCost) error {
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(spendFile(workspace), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

// Spends reads the workspace's spend log, oldest first.
func Spends(workspace string) ([]model.TurnCost, error) {
	f, err := os.Open(spendFile(workspace))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []model.TurnCost
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var c model.TurnCost
		if json.Unmarshal(sc.Bytes(), &c) == nil {
			out = append(out, c)
		}
	}
	return out, sc.Err()
}

// Events reads the last n events of a transcript; n <= 0 reads all. A
// truncated last line, left by a crash mid-write, is skipped.
func Events(workspace, thread string, n int) ([]model.Event, error) {
	f, err := os.Open(eventsFile(workspace, thread))
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
	return writeFile(path, append(data, '\n'), mode)
}

// writeFile replaces a file in one step, so a crash never leaves half of it.
func writeFile(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
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
