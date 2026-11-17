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
	switch e.Kind {
	case jPermission, jAsked, jFailed:
		e.Level = model.LevelNeedsYou
	case jNotice:
		e.Level = model.LevelNotice
		if e.NeedsYou {
			e.Level = model.LevelNeedsYou
		}
	case jArtifact:
		e.Level = model.LevelAmbient
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

// loadJournal reads a workspace's journal, moves into it what came before
// it, and makes it agree with what the threads kept. Callers hold h.mu.
func (h *Hub) loadJournal(ws *space) {
	ws.journal, _ = workspace.Journal(ws.p.Name)
	h.journalSeq = max(h.journalSeq, ws.p.BoardSeq)
	h.migrate(ws)
	for _, n := range slices.Sorted(maps.Keys(ws.threads)) {
		h.reconcile(ws, ws.threads[n])
	}
	h.rotateJournal(ws)
}

// reconcile makes a thread's journal agree with what the thread kept at
// start. A permission never outlives the hub, as its turn died with it. A
// question, a notice, a failure or unseen news a thread kept from before
// the journal goes into it. Callers hold h.mu.
func (h *Hub) reconcile(ws *space, th *thread) {
	t := th.t
	ws0, unseen, _ := needsOf(ws, th)
	open := map[model.WaitKind]bool{}
	for _, w := range ws0 {
		open[w.Kind] = true
		if w.Kind == model.WaitPermission {
			h.note(ws, th, workspace.Entry{Kind: jDecided, ID: w.ID, Text: "lost: neoharness stopped"})
		}
	}
	switch {
	case t.Asking != "" && !open[model.WaitQuestion]:
		h.note(ws, th, workspace.Entry{Kind: jAsked, Text: t.Asking, At: t.AskedAt})
	case t.Asking == "" && open[model.WaitQuestion]:
		h.note(ws, th, workspace.Entry{Kind: jDismissed})
	}
	for _, n := range t.Notices {
		if !slices.ContainsFunc(ws0, func(w model.Wait) bool { return w.ID == n.ID }) {
			h.note(ws, th, workspace.Entry{Kind: jNotice, ID: n.ID, Text: n.Text, NeedsYou: true, At: n.At})
		}
	}
	if failed(th) && !open[model.WaitFailed] {
		h.note(ws, th, workspace.Entry{Kind: jFailed, Text: t.Failed, At: t.FailedAt})
	}
	if t.Unseen > 0 {
		if t.Unseen > unseen && !t.Resolved {
			h.note(ws, th, workspace.Entry{Kind: jTurn, Text: t.Reply, Level: t.Unseen, At: t.UnseenAt})
		}
		t.Unseen, t.UnseenAt = 0, time.Time{}
		_ = workspace.SaveThread(ws.p.Name, t)
	}
}

// needsOf is what a session's journal says waits on the person: what
// opened and nothing closed since, in order. unseen is the highest level
// of 1 and 2 since they last saw it, and at when the last of those came.
func needsOf(ws *space, th *thread) (waits []model.Wait, unseen model.Level, at time.Time) {
	var perms, notices []workspace.Entry
	var ask, failure *workspace.Entry
	var seen time.Time
	drop := func(es []workspace.Entry, id string) []workspace.Entry {
		return slices.DeleteFunc(es, func(e workspace.Entry) bool { return e.ID == id })
	}
	for _, e := range ws.journal {
		if e.Thread != th.t.Name {
			continue
		}
		switch e.Kind {
		case jPermission:
			perms = append(perms, e)
		case jDecided:
			perms = drop(perms, e.ID)
		case jAsked:
			ask = &e
		case jNotice:
			if e.NeedsYou {
				notices = append(notices, e)
			}
		case jFailed:
			failure = &e
		case jStarted:
			failure = nil
		case jWrote:
			ask, failure, notices = nil, nil, nil
		case jDismissed:
			switch {
			case e.ID == failedID:
				failure = nil
			case e.ID == model.ReviewID:
				seen = later(seen, e.At)
			case slices.ContainsFunc(notices, func(n workspace.Entry) bool { return n.ID == e.ID }):
				notices = drop(notices, e.ID)
			default:
				ask = nil
			}
		case jSeen:
			seen = later(seen, e.At)
		case jResolved:
			perms, notices, ask, failure = nil, nil, nil, nil
		}
	}
	w := func(kind model.WaitKind, id string, e workspace.Entry) model.Wait {
		return model.Wait{Workspace: ws.p.Name, Thread: th.t.Name, Kind: kind, ID: id, Text: e.Text, Since: e.At}
	}
	for _, p := range perms {
		waits = append(waits, w(model.WaitPermission, p.ID, p))
	}
	if ask != nil {
		waits = append(waits, w(model.WaitQuestion, ask.ID, *ask))
	}
	// While a turn runs or messages wait, the failure is behind it.
	if failure != nil && !th.busy && len(th.t.Queue) == 0 {
		waits = append(waits, w(model.WaitFailed, failedID, *failure))
	}
	for _, n := range notices {
		waits = append(waits, w(model.WaitNotice, n.ID, n))
	}
	for _, e := range ws.journal {
		if e.Thread == th.t.Name && e.At.After(seen) && (e.Level == model.LevelAmbient || e.Level == model.LevelNotice) {
			unseen, at = max(unseen, e.Level), e.At
		}
	}
	return waits, unseen, at
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
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
