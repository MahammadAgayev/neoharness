package claude

import (
	"bufio"
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/MahammadAgayev/neoharness/hub/procgroup"
)

// A turn of `claude -p` pays for starting Node, the hooks and the MCP servers
// before the model sees anything. A session keeps one process for a
// conversation, with --input-format stream-json: each turn is a line on its
// stdin, and ends with the `result` record on its stdout.

// idleFor is how long a session waits for its next turn before it ends. Each
// process holds a few hundred MB.
var idleFor = 5 * time.Minute

// interruptWait is how long a stopped turn waits for claude to end it before
// the process is killed.
var interruptWait = 3 * time.Second

type session struct {
	// key is the start arguments that a turn must match to use the session.
	key    string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	lines  chan []byte // closed when the process exited
	stderr tail
	// cost is what claude reported so far: it counts from the process start.
	cost float64
	idle *time.Timer
}

// start starts a session. It outlives the turn that started it, so no
// context ends it: the turn interrupts it, and kill ends it.
func start(cmd *exec.Cmd, key string) (*session, error) {
	s := &session{key: key, cmd: cmd, lines: make(chan []byte)}
	procgroup.Isolate(cmd)
	var err error
	if s.stdin, err = cmd.StdinPipe(); err != nil {
		return nil, err
	}
	if s.stdout, err = cmd.StdoutPipe(); err != nil {
		return nil, err
	}
	cmd.Stderr = &s.stderr
	// An ssh ControlMaster holds the client's stderr until the remote command
	// ends, even after the client is gone.
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() {
		scanner := bufio.NewScanner(s.stdout)
		// Claude embeds whole tool results in the stream, so lines run well
		// past bufio's 64KB default. Without this the scanner stops early and
		// silently.
		scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
		for scanner.Scan() {
			s.lines <- append([]byte(nil), scanner.Bytes()...)
		}
		_ = cmd.Wait()
		close(s.lines)
	}()
	return s, nil
}

func (s *session) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.stdin.Write(append(b, '\n'))
	return err
}

func (s *session) prompt(text string) error {
	return s.send(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": text}})
}

func (s *session) interrupt() error {
	return s.send(map[string]any{"type": "control_request", "request_id": "stop", "request": map[string]any{"subtype": "interrupt"}})
}

// kill ends the process now. Closing our ends of the pipes also ends a
// remote one whose ssh client a ControlMaster outlives.
func (s *session) kill() {
	_ = procgroup.Kill(s.cmd)
	_ = s.stdin.Close()
	_ = s.stdout.Close()
}

func (s *session) exitCode() int {
	if st := s.cmd.ProcessState; st != nil {
		return st.ExitCode()
	}
	return -1
}

// pool keeps the idle sessions, one per conversation. A session is out of
// the pool while a turn uses it.
type pool struct {
	mu       sync.Mutex
	sessions map[string]*session
	closed   bool
}

func newPool() *pool { return &pool{sessions: map[string]*session{}} }

// take returns the idle session of the conversation if it was started with
// key, and ends one that was not: a new model, for example, needs a new
// process.
func (p *pool) take(conversation, key string) *session {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.sessions[conversation]
	if s == nil {
		return nil
	}
	delete(p.sessions, conversation)
	s.idle.Stop()
	if s.key != key {
		s.kill()
		return nil
	}
	return s
}

// put keeps a session that ended a turn for the conversation's next one.
func (p *pool) put(conversation string, s *session) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || conversation == "" || p.sessions[conversation] != nil {
		s.kill()
		return
	}
	p.sessions[conversation] = s
	s.idle = time.AfterFunc(idleFor, func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.sessions[conversation] == s {
			delete(p.sessions, conversation)
			s.kill()
		}
	})
}

// close ends every idle session, and those that turns give back later.
func (p *pool) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	for c, s := range p.sessions {
		s.idle.Stop()
		s.kill()
		delete(p.sessions, c)
	}
}

// tail keeps the end of what a process writes to stderr.
type tail struct {
	mu sync.Mutex
	b  strings.Builder
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.b.Len() > 64*1024 {
		rest := trim(t.b.String(), maxStderr)
		t.b.Reset()
		t.b.WriteString(rest)
	}
	return t.b.Write(p)
}

// take returns what was written since the last take.
func (t *tail) take() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.b.String()
	t.b.Reset()
	return s
}
