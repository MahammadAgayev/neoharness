// Package prompts holds the system prompts of hub sessions: the coordinator
// of a workspace and its threads.
package prompts

import (
	_ "embed"
	"fmt"
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

// Wake is the message a thread gets when the wake-up it asked for comes.
func Wake(note string) string {
	if note = strings.TrimSpace(note); note == "" {
		return "You asked to be woken now."
	}
	return "You asked to be woken now: " + note
}

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

// ArtifactEdited tells a session what the person changed in an artifact,
// from version from to version to. An empty diff means it was too long to
// send: the counts and sections stand in for it.
func ArtifactEdited(name string, from, to int, diff string, sections []string, added, removed int) string {
	head := fmt.Sprintf("The person edited the artifact %s (v%d → v%d).", name, from, to)
	if diff == "" {
		where := ""
		if len(sections) > 0 {
			where = " Under: " + strings.Join(sections, " · ") + "."
		}
		return fmt.Sprintf("%s %d lines added, %d removed.%s Read it with read_artifact before you change it.", head, added, removed, where)
	}
	return head + " The change, as a unified diff:\n\n```diff\n" + diff + "\n```\n\nKeep their changes when you write it again."
}

// replyLimit is how much of a thread's last reply ThreadEnded keeps.
const replyLimit = 300

// ThreadEnded is one line of ThreadsEnded: a thread, its state, its last
// progress report, and the start of its last reply. A report can be older
// than the reply, so both go in.
func ThreadEnded(name, state, progress, reply string) string {
	s := fmt.Sprintf("- %s: %s.", name, state)
	if progress != "" {
		s += " Last report: " + strings.TrimRight(progress, ".") + "."
	}
	if reply = strings.Join(strings.Fields(reply), " "); reply != "" {
		if r := []rune(reply); len(r) > replyLimit {
			reply = string(r[:replyLimit]) + "…"
		}
		s += " Last reply: " + reply
	}
	return s
}

// ThreadsEnded tells the coordinator which threads ended a turn since its
// last turn, as their reports do not wake it.
func ThreadsEnded(lines []string) string {
	return "Since your last turn, these threads ended a turn:\n" + strings.Join(lines, "\n")
}

func fill(tmpl string, in SessionInput) string {
	return strings.NewReplacer(
		"{{workspace}}", in.Workspace,
		"{{name}}", in.Name,
		"{{location}}", in.Location,
	).Replace(tmpl)
}
