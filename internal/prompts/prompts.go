// Package prompts holds the system prompts of hub sessions: the coordinator
// of a workspace and its threads.
package prompts

import (
	_ "embed"
	"strconv"
	"strings"
)

//go:embed coordinator.md
var coordinatorMD string

//go:embed thread.md
var threadMD string

//go:embed compact.md
var compactMD string

// SessionInput is what a hub session's system prompt depends on.
type SessionInput struct {
	Workspace string
	Name      string
	Location  string
}

// Coordinator is the system prompt for a workspace's coordinator.
func Coordinator(in SessionInput) string { return fill(coordinatorMD, in) }

// Thread is the system prompt for a thread.
func Thread(in SessionInput) string { return fill(threadMD, in) }

// Compact asks a session for the summary a new session starts from, as
// /compact. focus is what the person typed after /compact.
func Compact(focus string) string {
	if focus = strings.TrimSpace(focus); focus != "" {
		focus = "\nFocus on: " + focus + "\n"
	}
	return strings.Replace(compactMD, "{{focus}}", focus, 1)
}

// Compacted starts the first message of the session after /compact.
func Compacted(summary string) string {
	return "This conversation continues an earlier session that was compacted. Its summary:\n\n" + summary
}

// Shell is a command the user ran with `!` and its output, for the next
// message. bash-input and bash-stdout are the tags of Claude Code's own
// shell mode.
func Shell(command, out string, exit int) string {
	s := "<bash-input>" + command + "</bash-input>\n<bash-stdout>" + out + "</bash-stdout>"
	if exit != 0 {
		s += "\n<bash-exit-code>" + strconv.Itoa(exit) + "</bash-exit-code>"
	}
	return s
}

func fill(tmpl string, in SessionInput) string {
	return strings.NewReplacer(
		"{{workspace}}", in.Workspace,
		"{{name}}", in.Name,
		"{{location}}", in.Location,
	).Replace(tmpl)
}
