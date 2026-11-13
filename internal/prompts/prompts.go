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

//go:embed reports.md
var reportsMD string

//go:embed reports_notify.md
var reportsNotifyMD string

// SessionInput is what a hub session's system prompt depends on.
type SessionInput struct {
	Workspace string
	Name      string
	Location  string
	// Notify says the coordinator is the one that notifies the person.
	Notify bool
}

// Coordinator is the system prompt for a workspace's coordinator.
func Coordinator(in SessionInput) string {
	acts, reports := "You act only when the person writes to you.", reportsMD
	if in.Notify {
		acts, reports = "You act when the person writes to you, and when threads end a turn.", reportsNotifyMD
	}
	s := strings.NewReplacer("{{acts}}", acts, "{{reports}}", strings.TrimSpace(reports)).Replace(coordinatorMD)
	return fill(s, in)
}

// Reports wakes the coordinator about the threads that ended a turn. The
// note on each comes before it, as in every coordinator turn.
func Reports(names []string) string {
	return strings.Join(names, ", ") + " ended a turn. Decide whether the person needs to hear of it now."
}

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

// ArtifactStarted tells the coordinator the person started a document: the
// plan they write for the workspace. text is empty when it is too long to
// send.
func ArtifactStarted(name, text string) string {
	head := fmt.Sprintf("The person started the artifact %s. It is their document for this workspace: work from it, and keep it true as the work moves, what is done, what is in progress, what is next. Keep their words when you write it again.", name)
	if text == "" {
		return head + " Read it with read_artifact."
	}
	return head + "\n\n```markdown\n" + text + "\n```"
}

// Limits keep the board short: it starts every coordinator turn.
const (
	reportLimit = 150
	replyLimit  = 200
)

// BoardLine is one thread on the board: its state and last progress report,
// and, when it ended a turn since the coordinator's last turn, the start of
// its reply. A report can be older than the reply, so both go in.
func BoardLine(name, state, progress, reply string, ended bool) string {
	s := "- " + name + ": " + state
	if ended {
		s += ", ended a turn"
	}
	s += "."
	if progress = clip(progress, reportLimit); progress != "" {
		s += " Report: " + strings.TrimRight(progress, ".") + "."
	}
	if reply = clip(reply, replyLimit); ended && reply != "" {
		s += " Reply: " + reply
	}
	return s
}

// Board starts a coordinator turn with every open thread.
func Board(lines []string) string {
	return "Threads:\n" + strings.Join(lines, "\n")
}

// clip puts s on one line and cuts it to n runes.
func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		s = string(r[:n]) + "…"
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
