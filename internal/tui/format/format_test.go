package format

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestDuration(t *testing.T) {
	for _, tc := range []struct {
		in   time.Duration
		want string
	}{
		{0, "0s"},
		{5 * time.Second, "5s"},
		{59 * time.Second, "59s"},
		{time.Minute, "1m 00s"},
		{90 * time.Second, "1m 30s"},
		{59*time.Minute + 59*time.Second, "59m 59s"},
		{time.Hour, "1h 00m"},
		{2*time.Hour + 14*time.Minute, "2h 14m"},
		{25 * time.Hour, "25h 00m"},
		// A clock that moved backwards must not render "-3s".
		{-3 * time.Second, "0s"},
	} {
		if got := Duration(tc.in); got != tc.want {
			t.Errorf("Duration(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestAgo(t *testing.T) {
	// A run that has not said anything yet is a real state and needs a word,
	// not "55 years ago".
	if got := Ago(time.Time{}); got != "never" {
		t.Errorf("Ago(zero) = %q, want never", got)
	}
	if got := Ago(time.Now().Add(-90 * time.Second)); !strings.HasSuffix(got, "ago") {
		t.Errorf("Ago = %q, want a trailing 'ago'", got)
	}
}

func TestTruncate(t *testing.T) {
	for _, tc := range []struct {
		in   string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"exactly-10", 10, "exactly-10"},
		{"this is far too long", 10, "this is f…"},
		{"abc", 1, "…"},
		{"abc", 0, ""},
		{"abc", -1, ""},
	} {
		got := Truncate(tc.in, tc.n)
		if got != tc.want {
			t.Errorf("Truncate(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
		if n := utf8.RuneCountInString(got); tc.n > 0 && n > tc.n {
			t.Errorf("Truncate(%q, %d) produced %d runes", tc.in, tc.n, n)
		}
	}
}

func TestTruncateCountsRunesNotBytes(t *testing.T) {
	// Notes routinely contain em-dashes and accented words. Counting bytes
	// would cut these short of the column width, or mid-rune into a
	// replacement character.
	s := "héllo wörld — dashes"
	got := Truncate(s, 10)
	if n := utf8.RuneCountInString(got); n != 10 {
		t.Errorf("got %d runes, want 10: %q", n, got)
	}
	if !utf8.ValidString(got) {
		t.Errorf("truncation produced invalid UTF-8: %q", got)
	}
}

func TestOneLine(t *testing.T) {
	// A bash heredoc in a tool call, or a multi-line note, must not turn one
	// display row into thirty.
	in := "cat <<EOF\n  line one\n\n\tline two\nEOF"
	want := "cat <<EOF line one line two EOF"
	if got := OneLine(in); got != want {
		t.Errorf("OneLine = %q, want %q", got, want)
	}
	if got := OneLine("   "); got != "" {
		t.Errorf("OneLine(spaces) = %q, want empty", got)
	}
}

func TestCellIsExactlyNCells(t *testing.T) {
	for _, tc := range []struct {
		in string
		n  int
	}{
		{"short", 12},
		{"a much longer string than fits", 12},
		{"", 8},
		{"héllo wörld", 8},
	} {
		got := Cell(tc.in, tc.n)
		if n := utf8.RuneCountInString(got); n != tc.n {
			t.Errorf("Cell(%q, %d) = %q (%d runes), want exactly %d", tc.in, tc.n, got, n, tc.n)
		}
	}
}

func TestCellCollapsesWhitespaceBeforePadding(t *testing.T) {
	got := Cell("two\nlines", 12)
	if strings.Contains(got, "\n") {
		t.Errorf("Cell must not contain a newline: %q", got)
	}
	if !strings.HasPrefix(got, "two lines") {
		t.Errorf("Cell = %q", got)
	}
}

func TestTokens(t *testing.T) {
	for _, tc := range []struct {
		in   int
		want string
	}{
		{0, "—"},
		{-1, "—"},
		{42, "42"},
		{999, "999"},
		{1000, "1.0k"},
		{3509, "3.5k"},
		{999_999, "1000.0k"},
		{1_500_000, "1.5M"},
	} {
		if got := Tokens(tc.in); got != tc.want {
			t.Errorf("Tokens(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
