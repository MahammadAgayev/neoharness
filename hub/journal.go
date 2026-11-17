package hub

import (
	"cmp"
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

// journalKeep is how long read entries stay, and journalMax the most a
// workspace keeps, read or not, so a journal nobody reads does not grow
// without end.
const (
	journalKeep = 7 * 24 * time.Hour
	journalMax  = 2000
)

// note adds an entry to the journal of a session's workspace, and wakes the
// coordinator when it is news for its board. Callers hold h.mu.
func (h *Hub) note(ws *space, th *thread, e model.JournalEntry) {
	if th.deleted {
		return
	}
	h.journalSeq++
	e.Seq, e.Thread = h.journalSeq, th.t.Name
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	switch e.Kind {
	case model.JournalPermission, model.JournalAsked, model.JournalFailed:
		e.Level = model.LevelNeedsYou
	case model.JournalNotice:
		e.Level = model.LevelNotice
		if e.NeedsYou {
			e.Level = model.LevelNeedsYou
		}
	case model.JournalArtifact:
		e.Level = model.LevelAmbient
	}
	ws.journal = append(ws.journal, e)
	th.f.apply(e)
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
	case model.JournalRecap, model.JournalDone, model.JournalArtifact, model.JournalTurn, model.JournalFailed:
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
	h.fold(ws)
	for _, n := range slices.Sorted(maps.Keys(ws.threads)) {
		h.reconcile(ws, ws.threads[n])
	}
	h.rotateJournal(ws)
}

// facts is what a session's journal says of it now: what waits on the
// person, why its queue holds, and when the person last saw it. It is a
// cache of the journal: fold builds it at load and note keeps it, both
// through apply, so the turn rules read it without a scan and it cannot
// say other than the journal.
type facts struct {
	perms   []model.JournalEntry
	ask     *model.JournalEntry
	failure *model.JournalEntry
	notices []model.JournalEntry
	// hold says the queue waits for the person, with or without a question.
	hold *model.JournalEntry
	// seen is the entry that says when the person last saw the session.
	seen model.JournalEntry
}

// apply is how an entry opens and closes what waits. A permission closes
// by its decision; a question by a message of the person or a skip; a
// failure by the next turn, a message or a dismiss; a notice by a message
// or a dismiss; a hold by a message or resumed; all of it by resolved.
func (f *facts) apply(e model.JournalEntry) {
	drop := func(es []model.JournalEntry, id string) []model.JournalEntry {
		return slices.DeleteFunc(es, func(e model.JournalEntry) bool { return e.ID == id })
	}
	switch e.Kind {
	case model.JournalPermission:
		f.perms = append(f.perms, e)
	case model.JournalDecided:
		f.perms = drop(f.perms, e.ID)
	case model.JournalAsked:
		f.ask = &e
	case model.JournalNotice:
		if e.NeedsYou {
			f.notices = append(f.notices, e)
		}
	case model.JournalFailed:
		f.failure = &e
	case model.JournalStarted:
		f.failure = nil
	case model.JournalPaused:
		f.hold = &e
	case model.JournalResumed:
		f.hold = nil
	case model.JournalWrote:
		f.ask, f.failure, f.notices, f.hold = nil, nil, nil, nil
	case model.JournalDismissed:
		switch {
		case e.ID == failedID:
			f.failure = nil
		case e.ID == model.ReviewID:
			f.see(e)
		case slices.ContainsFunc(f.notices, func(n model.JournalEntry) bool { return n.ID == e.ID }):
			f.notices = drop(f.notices, e.ID)
		default:
			f.ask = nil
		}
	case model.JournalSeen:
		f.see(e)
	case model.JournalResolved:
		f.perms, f.notices, f.ask, f.failure, f.hold = nil, nil, nil, nil, nil
	}
}

func (f *facts) see(e model.JournalEntry) {
	if e.At.After(f.seen.At) {
		f.seen = e
	}
}

// live are the entries the facts rest on, which rotation keeps however old.
func (f *facts) live() []int64 {
	var out []int64
	for _, es := range [][]model.JournalEntry{f.perms, f.notices} {
		for _, e := range es {
			out = append(out, e.Seq)
		}
	}
	for _, e := range []*model.JournalEntry{f.ask, f.failure, f.hold, &f.seen} {
		if e != nil && e.Seq > 0 {
			out = append(out, e.Seq)
		}
	}
	return out
}

// fold builds every session's facts from the journal. Callers hold h.mu.
func (h *Hub) fold(ws *space) {
	for _, th := range ws.threads {
		th.f = facts{}
	}
	for _, e := range ws.journal {
		if th, ok := ws.threads[e.Thread]; ok {
			th.f.apply(e)
		}
	}
}

// reconcile makes a thread's journal agree with the world at start, and
// moves into it what the thread kept in its own file before the journal.
// A permission never outlives the hub, as its turn died with it. Callers
// hold h.mu.
func (h *Hub) reconcile(ws *space, th *thread) {
	t, f := th.t, &th.f
	for _, p := range slices.Clone(f.perms) {
		h.note(ws, th, model.JournalEntry{Kind: model.JournalDecided, ID: p.ID, Text: "lost: neoharness stopped"})
	}
	_, unseen, _ := needsOf(ws, th)
	if t.Asking != "" && f.ask == nil {
		h.note(ws, th, model.JournalEntry{Kind: model.JournalAsked, Text: t.Asking, At: t.AskedAt})
	}
	if t.Paused && f.hold == nil {
		h.note(ws, th, model.JournalEntry{Kind: model.JournalPaused})
	}
	for _, n := range t.Notices {
		if !slices.ContainsFunc(f.notices, func(e model.JournalEntry) bool { return e.ID == n.ID }) {
			h.note(ws, th, model.JournalEntry{Kind: model.JournalNotice, ID: n.ID, Text: n.Text, NeedsYou: true, At: n.At})
		}
	}
	if t.Failed != "" && !t.FailedAt.IsZero() && !t.Resolved && f.failure == nil {
		h.note(ws, th, model.JournalEntry{Kind: model.JournalFailed, Text: t.Failed, At: t.FailedAt})
	}
	if t.SeenAt.After(f.seen.At) {
		h.note(ws, th, model.JournalEntry{Kind: model.JournalSeen, At: t.SeenAt})
	}
	if t.Unseen > unseen && !t.Resolved {
		h.note(ws, th, model.JournalEntry{Kind: model.JournalTurn, Text: t.Reply, Level: t.Unseen, At: t.UnseenAt})
	}
	old := *t
	t.Asking, t.AskedAt, t.Paused, t.Notices = "", time.Time{}, false, nil
	t.Failed, t.FailedAt, t.SeenAt, t.Unseen, t.UnseenAt = "", time.Time{}, time.Time{}, 0, time.Time{}
	if old.Asking != "" || old.Paused || old.Notices != nil || old.Failed != "" || !old.FailedAt.IsZero() || !old.SeenAt.IsZero() || old.Unseen > 0 {
		_ = workspace.SaveThread(ws.p.Name, t)
	}
}

// needsOf is what a session's journal says waits on the person: what
// opened and nothing closed since, in order. unseen is the highest level
// of 1 and 2 since they last saw it, and at when the last of those came.
func needsOf(ws *space, th *thread) (waits []model.Wait, unseen model.Level, at time.Time) {
	f := &th.f
	w := func(kind model.WaitKind, id string, e model.JournalEntry) model.Wait {
		return model.Wait{Workspace: ws.p.Name, Thread: th.t.Name, Kind: kind, ID: id, Text: e.Text, Since: e.At}
	}
	for _, p := range f.perms {
		waits = append(waits, w(model.WaitPermission, p.ID, p))
	}
	if f.ask != nil {
		waits = append(waits, w(model.WaitQuestion, f.ask.ID, *f.ask))
	}
	// While a turn runs or messages wait, the failure is behind it.
	if f.failure != nil && !th.busy && len(th.t.Queue) == 0 {
		waits = append(waits, w(model.WaitFailed, failedID, *f.failure))
	}
	for _, n := range f.notices {
		waits = append(waits, w(model.WaitNotice, n.ID, n))
	}
	for _, e := range ws.journal {
		if e.Thread == th.t.Name && e.At.After(f.seen.At) && (e.Level == model.LevelAmbient || e.Level == model.LevelNotice) {
			unseen, at = max(unseen, e.Level), e.At
		}
	}
	return waits, unseen, at
}

// migrate moves what came before the journal into it, once: the news log,
// and the news each thread kept in its thread.json. A failure in that news
// may be over; reconcile moves in the one that still stands. Callers hold
// h.mu.
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
			h.note(ws, th, model.JournalEntry{Kind: model.JournalRecap, Text: th.t.Recap})
		}
		for _, d := range old.Done {
			h.note(ws, th, model.JournalEntry{Kind: model.JournalDone, Text: d})
		}
		for _, a := range old.Artifacts {
			h.note(ws, th, model.JournalEntry{Kind: model.JournalArtifact, Text: a})
		}
		if old.Reply != "" {
			h.note(ws, th, model.JournalEntry{Kind: model.JournalTurn, Text: old.Reply, Tools: 1})
		}
		_ = workspace.SaveThread(ws.p.Name, th.t)
	}
}

// newsOf folds the entries of a session that new says a reader has not
// read into what the board shows: a recap, todo items done, artifacts named
// once, and the reply of a turn that ran tools and brought no recap, the
// newest. A failure shows as the thread's state, not here. any says some
// entry was news.
func newsOf(ws *space, thread string, new func(model.JournalEntry) bool) (out prompts.News, any bool) {
	recapped := false
	for _, e := range ws.journal {
		if e.Thread != thread {
			continue
		}
		if e.Kind == model.JournalStarted {
			recapped = false
		}
		if e.Kind == model.JournalRecap {
			recapped = true
		}
		if !new(e) {
			continue
		}
		switch e.Kind {
		case model.JournalRecap:
			out.Recap, any = true, true
		case model.JournalDone:
			out.Done, any = append(out.Done, e.Text), true
		case model.JournalArtifact:
			if !slices.Contains(out.Artifacts, e.Text) {
				out.Artifacts = append(out.Artifacts, e.Text)
			}
			any = true
		case model.JournalTurn:
			if e.Tools > 0 && e.Text != "" && !recapped {
				out.Reply, any = e.Text, true
			}
		case model.JournalFailed:
			any = true
		}
	}
	return out, any
}

// Journal is the newest limit entries of every workspace's journal, newest
// first, each with its workspace.
func (h *Hub) Journal(limit int) []model.JournalEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []model.JournalEntry
	for name, ws := range h.workspaces {
		for _, e := range ws.journal {
			e.Workspace = name
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b model.JournalEntry) int { return cmp.Compare(b.Seq, a.Seq) })
	return out[:min(limit, len(out))]
}

// forBoard says the coordinator's board has not shown an entry yet.
func (ws *space) forBoard(e model.JournalEntry) bool { return e.Seq > ws.p.BoardSeq }

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
	live := map[int64]bool{}
	for _, th := range ws.threads {
		for _, seq := range th.f.live() {
			live[seq] = true
		}
	}
	kept := rotateJournal(ws.journal, ws.p.BoardSeq, func(name string) (time.Time, bool) {
		th, ok := ws.threads[name]
		if !ok || th.deleted || th.t.Resolved {
			return time.Time{}, false
		}
		return th.f.seen.At, true
	}, live, time.Now(), journalMax)
	if len(kept) == len(ws.journal) {
		return
	}
	ws.journal = kept
	_ = workspace.SaveJournal(ws.p.Name, kept)
}

// rotateJournal keeps an entry unless the board passed it, the person saw
// its session after it, or the session is gone, and it is older than
// journalKeep. seenAt says when the person last saw a session, and false
// for one that is gone. At most max entries stay, the newest. A live entry
// always stays: what is open now rests on it.
func rotateJournal(entries []model.JournalEntry, boardSeq int64, seenAt func(string) (time.Time, bool), live map[int64]bool, now time.Time, max int) []model.JournalEntry {
	var out []model.JournalEntry
	for _, e := range entries {
		at, open := seenAt(e.Thread)
		read := e.Seq <= boardSeq && (!open || !at.Before(e.At))
		if read && now.Sub(e.At) > journalKeep && !live[e.Seq] {
			continue
		}
		out = append(out, e)
	}
	for over := len(out) - max; over > 0; {
		i := slices.IndexFunc(out, func(e model.JournalEntry) bool { return !live[e.Seq] })
		if i < 0 {
			break
		}
		out = slices.Delete(out, i, i+1)
		over--
	}
	return out
}
