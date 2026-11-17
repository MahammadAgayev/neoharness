package hub

import (
	"maps"
	"slices"
	"time"

	"github.com/MahammadAgayev/neoharness/api/model"
	"github.com/MahammadAgayev/neoharness/hub/prompts"
	"github.com/MahammadAgayev/neoharness/hub/workspace"
)

// The journal keeps the facts of a workspace's sessions that matter outside
// their chat, in order: the transcript is the chat, the journal what
// happened. Nobody empties it: each reader keeps its place. The
// coordinator's board reads what came after BoardSeq, and the attention
// agent what came after the person last saw each session. Rotation drops
// what both have read once it is old.

// The kinds of journal entry.
const (
	jOpened     = "opened"
	jStarted    = "started" // a turn starts
	jTurn       = "turn"    // a turn ended well: Text is its reply, Tools its tool calls
	jFailed     = "failed"
	jRecap      = "recap"
	jDone       = "done" // a todo item
	jArtifact   = "artifact"
	jAsked      = "asked"
	jPermission = "permission"
	jDecided    = "decided" // a permission's answer
	jNotice     = "notice"
	jWrote      = "wrote"     // the person wrote to the session
	jDismissed  = "dismissed" // the person skipped a question, or dismissed a notice, a failure or news
	jSeen       = "seen"
	jResolved   = "resolved"
	jReopened   = "reopened"
)

// journalKeep is how long read entries stay, and journalMax the most a
// workspace keeps, read or not, so a journal nobody reads does not grow
// without end.
const (
	journalKeep = 7 * 24 * time.Hour
	journalMax  = 2000
)

// note adds an entry to the journal of a session's workspace, and wakes the
// coordinator when it is news for its board. Callers hold h.mu.
func (h *Hub) note(ws *space, th *thread, e workspace.Entry) {
	if th.deleted {
		return
	}
	h.journalSeq++
	e.Seq, e.Thread = h.journalSeq, th.t.Name
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	ws.journal = append(ws.journal, e)
	_ = workspace.AppendJournal(ws.p.Name, e)
	if len(ws.journal) > journalMax+journalMax/10 {
		h.rotateJournal(ws)
	}
	if boardNews(e.Kind) && th.t.Role == model.RoleThread {
		h.report(ws)
	}
}

// boardNews says an entry of a kind may be news on the coordinator's board.
// A turn is only when it ran tools and brought no recap; newsOf decides.
func boardNews(kind string) bool {
	switch kind {
	case jRecap, jDone, jArtifact, jTurn, jFailed:
		return true
	}
	return false
}

// loadJournal reads a workspace's journal, after migrate. Callers hold
// h.mu.
func (h *Hub) loadJournal(ws *space) {
	ws.journal, _ = workspace.Journal(ws.p.Name)
	h.journalSeq = max(h.journalSeq, ws.p.BoardSeq)
	h.migrate(ws)
	if len(ws.journal) > 0 {
		h.journalSeq = max(h.journalSeq, ws.journal[len(ws.journal)-1].Seq)
	}
	h.rotateJournal(ws)
}

// migrate moves what came before the journal into it, once: the news log,
// and the news each thread kept in its thread.json. Callers hold h.mu.
func (h *Hub) migrate(ws *space) {
	if old, _ := workspace.OldNews(ws.p.Name); len(old) > 0 {
		ws.journal = append(old, ws.journal...)
		_ = workspace.SaveJournal(ws.p.Name, ws.journal)
	}
	if len(ws.journal) > 0 {
		h.journalSeq = max(h.journalSeq, ws.journal[len(ws.journal)-1].Seq)
	}
	for _, n := range slices.Sorted(maps.Keys(ws.threads)) {
		th := ws.threads[n]
		old := th.t.News
		if old == nil {
			continue
		}
		th.t.News = nil
		if old.Recap {
			h.note(ws, th, workspace.Entry{Kind: jRecap, Text: th.t.Recap})
		}
		for _, d := range old.Done {
			h.note(ws, th, workspace.Entry{Kind: jDone, Text: d})
		}
		for _, a := range old.Artifacts {
			h.note(ws, th, workspace.Entry{Kind: jArtifact, Text: a})
		}
		if old.Reply != "" {
			h.note(ws, th, workspace.Entry{Kind: jTurn, Text: old.Reply, Tools: 1})
		}
		if old.Failed {
			h.note(ws, th, workspace.Entry{Kind: jFailed, Text: th.t.Failed})
		}
		_ = workspace.SaveThread(ws.p.Name, th.t)
	}
}

// newsOf folds the entries of a session that new says a reader has not
// read into what the board shows: a recap, todo items done, artifacts named
// once, and the reply of a turn that ran tools and brought no recap, the
// newest. A failure shows as the thread's state, not here. any says some
// entry was news.
func newsOf(ws *space, thread string, new func(workspace.Entry) bool) (out prompts.News, any bool) {
	recapped := false
	for _, e := range ws.journal {
		if e.Thread != thread {
			continue
		}
		if e.Kind == jStarted {
			recapped = false
		}
		if e.Kind == jRecap {
			recapped = true
		}
		if !new(e) {
			continue
		}
		switch e.Kind {
		case jRecap:
			out.Recap, any = true, true
		case jDone:
			out.Done, any = append(out.Done, e.Text), true
		case jArtifact:
			if !slices.Contains(out.Artifacts, e.Text) {
				out.Artifacts = append(out.Artifacts, e.Text)
			}
			any = true
		case jTurn:
			if e.Tools > 0 && e.Text != "" && !recapped {
				out.Reply, any = e.Text, true
			}
		case jFailed:
			any = true
		}
	}
	return out, any
}

// forBoard says the coordinator's board has not shown an entry yet.
func (ws *space) forBoard(e workspace.Entry) bool { return e.Seq > ws.p.BoardSeq }

// boardRead moves the coordinator's place in the journal to its end.
// Callers hold h.mu.
func (h *Hub) boardRead(ws *space) {
	if len(ws.journal) == 0 || ws.p.BoardSeq >= ws.journal[len(ws.journal)-1].Seq {
		return
	}
	ws.p.BoardSeq = ws.journal[len(ws.journal)-1].Seq
	_ = workspace.Save(ws.p)
	h.rotateJournal(ws)
}

// rotateJournal drops the entries both readers have read and that are
// old, and writes the journal when it changed. Callers hold h.mu.
func (h *Hub) rotateJournal(ws *space) {
	kept := rotateJournal(ws.journal, ws.p.BoardSeq, func(name string) (time.Time, bool) {
		th, ok := ws.threads[name]
		if !ok || th.deleted || th.t.Resolved {
			return time.Time{}, false
		}
		return th.t.SeenAt, true
	}, time.Now(), journalMax)
	if len(kept) == len(ws.journal) {
		return
	}
	ws.journal = kept
	_ = workspace.SaveJournal(ws.p.Name, kept)
}

// rotateJournal keeps an entry unless the board passed it, the person saw
// its session after it, or the session is gone, and it is older than
// journalKeep. seenAt says when the person last saw a session, and false
// for one that is gone. At most max entries stay, the newest.
func rotateJournal(entries []workspace.Entry, boardSeq int64, seenAt func(string) (time.Time, bool), now time.Time, max int) []workspace.Entry {
	var out []workspace.Entry
	for _, e := range entries {
		at, open := seenAt(e.Thread)
		read := e.Seq <= boardSeq && (!open || !at.Before(e.At))
		if read && now.Sub(e.At) > journalKeep {
			continue
		}
		out = append(out, e)
	}
	if len(out) > max {
		out = out[len(out)-max:]
	}
	return out
}
