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
	acts := "Act only when the person writes to you."
	threads := "Threads report directly to the person. Correct problems at their source rather than repeating reports."
	reports := reportsMD
	if in.Notify {
		acts = "Act when the person writes to you, and when threads end a turn."
		threads = "Threads do not notify the person; you decide what reaches them. Correct problems at their source rather than repeating reports."
		reports = reportsNotifyMD
	}
	s := strings.NewReplacer("{{acts}}", acts, "{{threads}}", threads, "{{reports}}", strings.TrimSpace(reports)).Replace(coordinatorMD)
	return fill(s, in)
}

// Reports wakes the coordinator about the threads that ended a turn. The
// note on each comes before it, as in every coordinator turn.
func Reports(names []string) string {
	return strings.Join(names, ", ") + " ended a turn. Decide whether the person needs to hear of it now."
}

// RecapDue wakes the coordinator to write the workspace recap.
func RecapDue() string {
	return "The threads that ran have ended their turns. Write your workspace recap with workspace_recap."
}

// RecapAsked is the person asking a thread for a fresh recap.
func RecapAsked() string {
	return "How does your work stand now? Say it with recap, and start no other work."
}

// WorkspaceRecapAsked is the person asking the coordinator for a fresh
// workspace recap.
func WorkspaceRecapAsked() string {
	return "How does the workspace stand now? Say it with workspace_recap, and start no other work."
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

// TakenBack tells a session that the person took back a message it began
// to work on.
func TakenBack(text string) string {
	return "The person stopped your turn and took back this message; do not act on it:\n> " + strings.ReplaceAll(text, "\n", "\n> ")
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
	boardFiles  = 5
)

// Recap is a thread's recap as the board shows it: its own words, then what
// the hub saw its last turn do.
type Recap struct {
	Asked, Outcome, Open, Error string
	// FromReply says Outcome is the first line of its reply.
	FromReply bool
	Ran       int
	// Failed are the commands that failed.
	Failed            []string
	Edited, Artifacts []string
}

// Wait is what a waiting thread waits for. Permission is the tool and what
// it would do.
type Wait struct {
	Question, Permission string
	Paused               bool
}

// BoardLine is one thread on the board: its state, what it waits for, and,
// when it ended a turn since the coordinator's last turn, its recap: its own
// words, then what the hub saw it do. Without a new recap, a failed last
// turn still shows, as the thread needs someone until it runs again.
func BoardLine(name, state string, w Wait, r *Recap, ended bool) string {
	s := "- " + name + ": " + state
	if ended {
		s += ", ended a turn"
	}
	s += "."
	part := func(label, text string) {
		if text = clip(text, replyLimit); text != "" {
			s += " " + label + ": " + sentence(text)
		}
	}
	switch {
	case w.Permission != "":
		part("Waits for permission", w.Permission)
	case w.Question != "":
		part("Asks the person", w.Question)
	case w.Paused:
		s += " Paused by the person."
	}
	if r == nil {
		return s
	}
	if !ended {
		part("Last turn failed", r.Error)
		return s
	}
	part("Asked", r.Asked)
	if r.FromReply {
		part("Reply", r.Outcome)
	} else {
		part("Outcome", r.Outcome)
	}
	part("Open", r.Open)
	part("Failed", r.Error)
	return s + " Seen by neoharness: " + seen(r) + "."
}

// seen is what the hub saw a turn do, in short: the failed commands by name,
// the others only counted, as they are what the coordinator acts on.
func seen(r *Recap) string {
	var parts []string
	switch r.Ran {
	case 0:
		parts = append(parts, "no commands")
	case 1:
		parts = append(parts, "1 command")
	default:
		parts = append(parts, fmt.Sprintf("%d commands", r.Ran))
	}
	if n := len(r.Failed); n > 0 {
		var failed []string
		for _, c := range r.Failed {
			failed = append(failed, clip(c, reportLimit))
		}
		parts[0] += fmt.Sprintf(", %d failed: %s", n, strings.Join(failed, ", "))
	}
	if n := len(r.Edited); n > 0 {
		files := strings.Join(r.Edited[:min(n, boardFiles)], ", ")
		if n > boardFiles {
			files += fmt.Sprintf(" and %d more", n-boardFiles)
		}
		parts = append(parts, "edited "+files)
	}
	for _, a := range r.Artifacts {
		parts = append(parts, "wrote the artifact "+a)
	}
	return strings.Join(parts, "; ")
}

// sentence ends s with one full stop.
func sentence(s string) string {
	if strings.HasSuffix(s, "…") || strings.HasSuffix(s, "?") || strings.HasSuffix(s, "!") {
		return s
	}
	return strings.TrimRight(s, ".") + "."
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
