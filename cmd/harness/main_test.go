package main

import (
	"flag"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestParseFlagsAcceptsFlagsAfterThePositional guards a bug that had real
// consequences: Go's flag package stops parsing at the first non-flag
// argument, so `harness init demo -C /tree` dropped the -C entirely and the
// run recorded whatever directory the shell happened to be in. Nothing failed;
// the run just worked on the wrong tree.
func TestParseFlagsAcceptsFlagsAfterThePositional(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		wantDir  string
		wantName string
	}{
		{"flags first", []string{"-C", "/tree", "demo"}, "/tree", "demo"},
		{"flags after", []string{"demo", "-C", "/tree"}, "/tree", "demo"},
		{"interleaved", []string{"-a", "pi", "demo", "-C", "/tree"}, "/tree", "demo"},
		{"equals form", []string{"demo", "-C=/tree"}, "/tree", "demo"},
		{"no flags", []string{"demo"}, "", "demo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("init", flag.ContinueOnError)
			dir := fs.String("C", "", "")
			fs.String("a", "pi", "")
			pos, err := parseFlags(fs, tc.args)
			if err != nil {
				t.Fatal(err)
			}
			if *dir != tc.wantDir {
				t.Errorf("-C = %q, want %q", *dir, tc.wantDir)
			}
			if got := first(pos); got != tc.wantName {
				t.Errorf("name = %q, want %q", got, tc.wantName)
			}
		})
	}
}

func TestParseFlagsHandlesBoolFlags(t *testing.T) {
	// A bool flag takes no value, so the argument after it is a positional and
	// must not be swallowed as if it were the flag's value.
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	attach := fs.Bool("attach", false, "")
	pos, err := parseFlags(fs, []string{"-attach", "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if !*attach {
		t.Error("-attach should be set")
	}
	if got := first(pos); got != "demo" {
		t.Errorf("name = %q, want demo — the bool flag ate the positional", got)
	}
}

func TestParseFlagsStopsAtDoubleDash(t *testing.T) {
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	fs.String("C", "", "")
	pos, err := parseFlags(fs, []string{"demo", "--", "-not-a-flag"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pos) != 2 || pos[1] != "-not-a-flag" {
		t.Errorf("positional = %v, want everything after -- taken literally", pos)
	}
}

func TestFirstOnEmpty(t *testing.T) {
	if got := first(nil); got != "" {
		t.Errorf("first(nil) = %q, want empty", got)
	}
}

func TestNewAgentKnowsBothAdapters(t *testing.T) {
	for _, name := range []string{"", "pi", "claude"} {
		a, err := newAgent(name, "")
		if err != nil {
			t.Errorf("newAgent(%q): %v", name, err)
			continue
		}
		if a == nil {
			t.Errorf("newAgent(%q) returned nil", name)
		}
	}
	if _, err := newAgent("gpt", ""); err == nil {
		t.Error("an unknown agent should be an error, not a silent default")
	}
}

// TestCommandForPrefersTheEnvironment pins the precedence the -cmd flag
// documents. A run records the command it was created with, but a machine
// where the CLI moved has to be fixable without rewriting every state.json,
// so the environment variable wins.
func TestCommandForPrefersTheEnvironment(t *testing.T) {
	const key = "HARNESS_TEST_COMMAND"
	for _, tc := range []struct {
		name string
		env  string
		cmd  string
		want []string
	}{
		{"neither", "", "", nil},
		{"run only", "", "authwrap agent run claude", []string{"authwrap", "agent", "run", "claude"}},
		{"env only", "wrapper claude", "", []string{"wrapper", "claude"}},
		{"env wins", "wrapper claude", "authwrap agent run claude", []string{"wrapper", "claude"}},
		{"blank env ignored", "   ", "authwrap claude", []string{"authwrap", "claude"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(key, tc.env)
			got := commandFor(key, tc.cmd)
			if len(got) != len(tc.want) {
				t.Fatalf("commandFor = %q, want %q", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("commandFor = %q, want %q", got, tc.want)
				}
			}
		})
	}
}

// TestTruncateCountsRunesNotBytes guards the display against a cut landing
// inside a multi-byte rune. Reviewer findings and tool arguments are full of
// em-dashes and curly quotes, and half a rune renders as a replacement
// character.
func TestTruncateCountsRunesNotBytes(t *testing.T) {
	// 10 runes, 30 bytes.
	s := strings.Repeat("—", 10)
	if got := truncate(s, 20); got != s {
		t.Errorf("truncate = %q, want it unchanged: 10 runes fits in 20", got)
	}
	got := truncate(s, 5)
	if utf8.RuneCountInString(got) != 5 {
		t.Errorf("truncate = %q, want 5 runes, got %d", got, utf8.RuneCountInString(got))
	}
	if !utf8.ValidString(got) {
		t.Errorf("truncate produced invalid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncate = %q, want an ellipsis", got)
	}
}

func TestTruncateFlattensWhitespace(t *testing.T) {
	// A bash heredoc must not become thirty log lines.
	if got := truncate("cat <<EOF\na\n  b\nEOF", 100); got != "cat <<EOF a b EOF" {
		t.Errorf("truncate = %q, want one flattened line", got)
	}
}

func TestFirstLineSkipsHeadingsAndBlanks(t *testing.T) {
	// Reviewer findings routinely open with a blank line or a markdown
	// heading; neither is the sentence worth showing.
	if got := firstLine("\n\n## Findings\n- the retry branch is never exercised\n"); got != "Findings" {
		t.Errorf("firstLine = %q", got)
	}
	if got := firstLine("   \n"); got != "" {
		t.Errorf("firstLine = %q, want empty", got)
	}
}
