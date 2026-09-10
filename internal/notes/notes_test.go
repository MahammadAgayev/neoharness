package notes

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendTo(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
}

func texts(ns []Note) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n.Text
	}
	return out
}

func eq(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d notes %q, want %d %q", len(got), got, len(want), want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("note %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestTailerReadsAppendedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.md")
	write(t, path, "first thing\n")
	tail := NewTailer(path)

	eq(t, texts(tail.Poll()), []string{"first thing"})
	// Nothing new between polls must produce nothing, or the display repeats
	// the last note forever.
	eq(t, texts(tail.Poll()), nil)

	appendTo(t, path, "second thing\nthird thing\n")
	eq(t, texts(tail.Poll()), []string{"second thing", "third thing"})
	eq(t, texts(tail.Poll()), nil)
}

func TestTailerHoldsBackAPartialLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.md")
	write(t, path, "complete line\n")
	tail := NewTailer(path)
	eq(t, texts(tail.Poll()), []string{"complete line"})

	// An agent appending with a shell redirect can be observed mid-write.
	// Emitting the fragment would leave a truncated sentence in the display
	// permanently, since the rest arrives as a separate note.
	appendTo(t, path, "half a sen")
	eq(t, texts(tail.Poll()), nil)

	appendTo(t, path, "tence now finished\n")
	eq(t, texts(tail.Poll()), []string{"half a sentence now finished"})
}

func TestTailerOnMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "never-created.md")
	tail := NewTailer(path)
	// The agent may simply not have written yet. That is a thing for the
	// display to report, not an error here.
	if got := tail.Poll(); got != nil {
		t.Fatalf("expected nothing, got %q", texts(got))
	}
	write(t, path, "it exists now\n")
	eq(t, texts(tail.Poll()), []string{"it exists now"})
}

func TestTailerHandlesTruncation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.md")
	write(t, path, "one\ntwo\nthree\n")
	tail := NewTailer(path)
	eq(t, texts(tail.Poll()), []string{"one", "two", "three"})

	// An agent using `>` instead of `>>` replaces the file. Reading on from the
	// old offset would return nothing at all, silencing the run for good.
	write(t, path, "fresh start\n")
	eq(t, texts(tail.Poll()), []string{"fresh start"})
}

func TestTailerFromEndSkipsExistingContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.md")
	write(t, path, "old note one\nold note two\n")
	tail := NewTailerFromEnd(path)
	// Attaching to a run in progress must not replay its whole history.
	if got := tail.Poll(); got != nil {
		t.Fatalf("expected no backlog, got %q", texts(got))
	}
	appendTo(t, path, "brand new\n")
	eq(t, texts(tail.Poll()), []string{"brand new"})
}

func TestTailerFromEndOnMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.md")
	tail := NewTailerFromEnd(path)
	write(t, path, "created after\n")
	eq(t, texts(tail.Poll()), []string{"created after"})
}

func TestCleanStripsMarkup(t *testing.T) {
	// Agents write notes as bullets, headings, or bare prose depending on the
	// model and the day; the display should not have to render markdown.
	for _, tc := range []struct{ in, want string }{
		{"plain sentence", "plain sentence"},
		{"- bulleted", "bulleted"},
		{"* starred", "starred"},
		{"+ plussed", "plussed"},
		{"  - indented bullet", "indented bullet"},
		{"## heading", "heading"},
		{"### deeper heading", "deeper heading"},
		{"", ""},
		{"   \t  ", ""},
		{"---", ""},
		{"***", ""},
		{"- ", ""},
		{"a - b", "a - b"},                               // an inner dash is content
		{"-no space after dash", "-no space after dash"}, // not a bullet
	} {
		if got := clean(tc.in); got != tc.want {
			t.Errorf("clean(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestTailerDropsEmptyLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.md")
	write(t, path, "real note\n\n\n---\n\nanother real note\n")
	eq(t, texts(NewTailer(path).Poll()), []string{"real note", "another real note"})
}

func TestTailerStampsNotes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.md")
	write(t, path, "a note\n")
	got := NewTailer(path).Poll()
	if len(got) != 1 {
		t.Fatalf("want 1 note, got %d", len(got))
	}
	// The stamp is when the harness saw it. An agent asked to write its own
	// timestamps writes plausible ones, which is worse than none on a stuck run.
	if got[0].At.IsZero() {
		t.Error("a note should be stamped with the time it was read")
	}
}

func TestReadLastReturnsTheTailInWrittenOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.md")
	write(t, path, "one\ntwo\nthree\nfour\nfive\n")
	eq(t, texts(ReadLast(path, 2)), []string{"four", "five"})
	eq(t, texts(ReadLast(path, 10)), []string{"one", "two", "three", "four", "five"})
}

func TestReadLastSkipsBlanksWhenCounting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.md")
	write(t, path, "real one\n\n\n\n\nreal two\n\n\n")
	// Asking for two notes must give two notes, not two lines that happen to
	// be empty.
	eq(t, texts(ReadLast(path, 2)), []string{"real one", "real two"})
}

func TestReadLastOnMissingFile(t *testing.T) {
	if got := ReadLast(filepath.Join(t.TempDir(), "nope.md"), 5); got != nil {
		t.Errorf("expected nothing, got %q", texts(got))
	}
}

func TestReadLastOnEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.md")
	write(t, path, "")
	if got := ReadLast(path, 5); len(got) != 0 {
		t.Errorf("expected nothing, got %q", texts(got))
	}
}

func TestReadLastHandlesNoTrailingNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.md")
	write(t, path, "one\ntwo without newline")
	eq(t, texts(ReadLast(path, 5)), []string{"one", "two without newline"})
}
