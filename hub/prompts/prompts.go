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

// Reports wakes the coordinator about the threads with news. The board,
// as in every coordinator turn, says what it is.
func Reports(names []string) string {
	verb := " has"
	if len(names) > 1 {
		verb = " have"
	}
	return strings.Join(names, ", ") + verb + " news. Decide whether the person needs to hear of it now."
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

// replyLimit keeps the board short: it starts every coordinator turn.
const replyLimit = 200

// Wait is what a waiting thread waits for. Permission is the tool and what
// it would do.
type Wait struct {
	Question, Permission string
	Paused               bool
}

// News is what changed in a thread since the coordinator's last board: a
// new recap, todo items done, artifacts written, and, for a turn that ran
// tools and wrote no recap, the start of its reply.
type News struct {
	Recap     bool
	Done      []string
	Artifacts []string
	Reply     string
}

// BoardThread is one thread on the board. Done and Total count its todo
// items, and Now is the one it works on.
type BoardThread struct {
	Name, State string
	Wait        Wait
	Recap       string
	Done, Total int
	Now         string
	Failed      string
	News        News
}

// boardDone is how many done items the board names; it counts the rest.
const boardDone = 10

// BoardLine is one thread on the board: its state, what it waits for, why
// its last turn failed, its recap and tasks, and then what is new since the
// coordinator's last board. A failure shows until the thread runs again,
// as the thread needs someone until then.
func BoardLine(t BoardThread) string {
	s := "- " + t.Name + ": " + t.State + "."
	part := func(label, text string) {
		if text = clip(text, replyLimit); text != "" {
			s += " " + label + ": " + sentence(text)
		}
	}
	switch {
	case t.Wait.Permission != "":
		part("Waits for permission", t.Wait.Permission)
	case t.Wait.Question != "":
		part("Asks the person", t.Wait.Question)
	case t.Wait.Paused:
		s += " Paused by the person."
	}
	part("Last turn failed", t.Failed)
	part("Recap", t.Recap)
	if t.Total > 0 {
		tasks := fmt.Sprintf("%d/%d done", t.Done, t.Total)
		if t.Now != "" {
			tasks += ", now: " + clip(t.Now, replyLimit)
		}
		part("Tasks", tasks)
	}
	var news []string
	if t.News.Recap {
		news = append(news, "recap")
	}
	if done := t.News.Done; len(done) > 0 {
		more := ""
		if len(done) > boardDone {
			more = fmt.Sprintf(" and %d more", len(done)-boardDone)
			done = done[:boardDone]
		}
		news = append(news, "done: "+strings.Join(done, ", ")+more)
	}
	if len(t.News.Artifacts) > 0 {
		news = append(news, "wrote "+strings.Join(t.News.Artifacts, ", "))
	}
	if r := clip(t.News.Reply, replyLimit); r != "" {
		news = append(news, "reply: "+r)
	}
	if len(news) > 0 {
		s += "\n  New: " + sentence(strings.Join(news, "; "))
	}
	return s
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
