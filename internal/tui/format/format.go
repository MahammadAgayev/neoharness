// Package format holds the small display conversions both TUIs need.
//
// Separate from the views because these are the parts with actual edge cases —
// a duration that should read "2h 14m" rather than "8040s", a note that must
// not wrap and break the layout — and they are worth testing without a
// terminal.
package format

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Duration renders an elapsed time at a glance-readable precision.
//
// Runs last minutes to hours, so seconds stop mattering quickly; but a run
// that just started needs them, or the display reads "0m" for a minute.
func Duration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// Ago renders how long ago something happened. A zero time reads as "never",
// which is a real state: a run that has not yet said anything.
func Ago(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return Duration(time.Since(t)) + " ago"
}

// Truncate shortens s to at most n display cells, marking the cut with an
// ellipsis.
//
// Counted in runes rather than bytes: a note with an em-dash or an accented
// word would otherwise be cut short of the width, or worse, cut mid-rune and
// render as a replacement character.
func Truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	runes := []rune(s)
	return string(runes[:n-1]) + "…"
}

// OneLine collapses any run of whitespace into single spaces.
//
// A tool call carrying a bash heredoc, or a note an agent wrote with an
// embedded newline, would otherwise turn one display row into thirty and
// destroy the layout.
func OneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// Cell renders s in exactly n cells, padded or truncated. Used for table
// columns, where ragged widths are the whole problem.
func Cell(s string, n int) string {
	s = Truncate(OneLine(s), n)
	if pad := n - utf8.RuneCountInString(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

// Tokens abbreviates a token count. Exact counts are noise at a glance; the
// magnitude is the signal.
func Tokens(n int) string {
	switch {
	case n <= 0:
		return "—"
	case n < 1000:
		return fmt.Sprintf("%d", n)
	case n < 1_000_000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
}
