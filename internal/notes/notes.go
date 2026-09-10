// Package notes reads the file an agent writes its progress to.
//
// The notes file is the run's voice. Everything else the display can show —
// tool calls, elapsed time, token counts — is activity, and activity is not
// progress. This is the one channel that carries intent, so reading it
// correctly matters more than its size suggests.
//
// Tailing is by polling rather than filesystem events. A run's iterations take
// minutes, so a couple of seconds of lag is invisible, and polling has no
// watcher lifecycle to leak, no per-platform behaviour, and no dependency.
package notes

import (
	"bufio"
	"io"
	"os"
	"strings"
	"time"
)

// Note is one line the agent wrote, stamped when the harness first saw it.
//
// The timestamp is the read time, not a time the agent supplied. An agent
// asked to write its own timestamps writes plausible ones, and a plausible
// timestamp on a stuck run is worse than none.
type Note struct {
	At   time.Time
	Text string
}

// Tailer reads new lines appended to a file.
//
// It is not safe for concurrent use; one Tailer belongs to one reader.
type Tailer struct {
	path   string
	offset int64
	// partial holds a trailing fragment with no newline yet. An agent appending
	// with a shell redirect can be observed mid-write, and emitting half a line
	// as a note would put a truncated sentence permanently in the display.
	partial string
}

// NewTailer starts tailing path from the beginning.
func NewTailer(path string) *Tailer {
	return &Tailer{path: path}
}

// NewTailerFromEnd starts tailing path from wherever it currently ends, so
// existing content is not replayed. Used when attaching to a run in progress.
func NewTailerFromEnd(path string) *Tailer {
	t := &Tailer{path: path}
	if fi, err := os.Stat(path); err == nil {
		t.offset = fi.Size()
	}
	return t
}

// Poll returns whatever has been appended since the last call.
//
// A missing file is not an error: the agent may not have written anything yet,
// and a run whose notes file never appears is a reportable condition for the
// display, not a failure of the tailer.
func (t *Tailer) Poll() []Note {
	fi, err := os.Stat(t.path)
	if err != nil {
		return nil
	}

	// A file that shrank was truncated or replaced — an agent using `>` instead
	// of `>>`, most likely. Reading from the old offset would return garbage or
	// nothing at all, so start over. Re-emitting the new content is the right
	// call: it is genuinely new writing, and losing it is worse than repeating.
	if fi.Size() < t.offset {
		t.offset = 0
		t.partial = ""
	}
	if fi.Size() == t.offset {
		return nil
	}

	f, err := os.Open(t.path)
	if err != nil {
		return nil
	}
	defer f.Close()

	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		return nil
	}

	now := time.Now()
	var out []Note
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadString('\n')
		t.offset += int64(len(line))
		if err != nil {
			// No trailing newline: hold the fragment until the rest arrives.
			t.partial += line
			break
		}
		full := t.partial + line
		t.partial = ""
		if text := clean(full); text != "" {
			out = append(out, Note{At: now, Text: text})
		}
	}
	return out
}

// clean normalises one raw line into note text, or "" if it carries nothing.
//
// Agents write notes as list items, headings, or bare sentences depending on
// the model and the day. Stripping the markup means the display does not have
// to render markdown, and an empty result is how blank lines and separators
// are dropped.
func clean(line string) string {
	s := strings.TrimSpace(line)
	if s == "" {
		return ""
	}
	// A horizontal rule is a separator, not a note.
	if strings.Trim(s, "-*_ ") == "" {
		return ""
	}
	s = strings.TrimLeft(s, "#")
	s = strings.TrimSpace(s)
	for _, bullet := range []string{"- ", "* ", "+ "} {
		if rest, ok := strings.CutPrefix(s, bullet); ok {
			s = strings.TrimSpace(rest)
			break
		}
	}
	return s
}

// ReadLast returns up to n of the most recent notes in a file.
//
// Used by the dashboard, which has no continuity between polls and only ever
// wants the tail. Reading the whole file each time is fine: these files are a
// few kilobytes and the alternative is caching per-run offsets in a process
// that is meant to be stateless.
func ReadLast(path string, n int) []Note {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	var out []Note
	for i := len(lines) - 1; i >= 0 && len(out) < n; i-- {
		if text := clean(lines[i]); text != "" {
			out = append(out, Note{Text: text})
		}
	}
	// Collected newest-first; hand back in the order they were written.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}
