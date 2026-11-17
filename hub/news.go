package hub

import (
	"maps"
	"slices"
	"time"

	"github.com/MahammadAgayev/neoharness/api/model"
	"github.com/MahammadAgayev/neoharness/hub/prompts"
	"github.com/MahammadAgayev/neoharness/hub/workspace"
)

// The news log keeps what the threads of a workspace did, in order. Nobody
// empties it: each reader keeps where it is. The coordinator's board reads
// what came after BoardSeq, and the attention agent what came after the
// person last saw each thread. Rotation drops what both have read once it
// is old.

const (
	newsRecap    = "recap"
	newsDone     = "done"
	newsArtifact = "artifact"
	newsReply    = "reply"
	newsFailed   = "failed"
)

// newsKeep is how long read news stays, and newsMax the most a workspace
// keeps, read or not, so a log nobody reads does not grow without end.
const (
	newsKeep = 7 * 24 * time.Hour
	newsMax  = 2000
)

// addNews adds to a thread's news and wakes the coordinator for it.
// Callers hold h.mu.
func (h *Hub) addNews(ws *space, th *thread, kind, text string) {
	if th.t.Role != model.RoleThread {
		return
	}
	h.newsSeq++
	n := workspace.NewsItem{Seq: h.newsSeq, At: time.Now().UTC(), Thread: th.t.Name, Kind: kind, Text: text}
	ws.news = append(ws.news, n)
	_ = workspace.AppendNews(ws.p.Name, n)
	if len(ws.news) > newsMax+newsMax/10 {
		h.rotateNews(ws)
	}
	h.report(ws)
}

// loadNews reads a workspace's news log, and moves the news its threads
// kept before the log into it. Callers hold h.mu.
func (h *Hub) loadNews(ws *space) {
	ws.news, _ = workspace.ReadNews(ws.p.Name)
	h.newsSeq = max(h.newsSeq, ws.p.BoardSeq)
	if len(ws.news) > 0 {
		h.newsSeq = max(h.newsSeq, ws.news[len(ws.news)-1].Seq)
	}
	for _, n := range slices.Sorted(maps.Keys(ws.threads)) {
		th := ws.threads[n]
		old := th.t.News
		if old == nil {
			continue
		}
		th.t.News = nil
		if old.Recap {
			h.addNews(ws, th, newsRecap, th.t.Recap)
		}
		for _, d := range old.Done {
			h.addNews(ws, th, newsDone, d)
		}
		for _, a := range old.Artifacts {
			h.addNews(ws, th, newsArtifact, a)
		}
		if old.Reply != "" {
			h.addNews(ws, th, newsReply, old.Reply)
		}
		if old.Failed {
			h.addNews(ws, th, newsFailed, th.t.Failed)
		}
		_ = workspace.SaveThread(ws.p.Name, th.t)
	}
	h.rotateNews(ws)
}

// newsOf folds the news of a thread that new says a reader has not read
// into what the board shows: a newer reply stands for the older ones, and
// an artifact written twice is named once. A failure shows as the thread's
// state, not here.
func newsOf(ws *space, thread string, new func(workspace.NewsItem) bool) prompts.News {
	var out prompts.News
	for _, n := range ws.news {
		if n.Thread != thread || !new(n) {
			continue
		}
		switch n.Kind {
		case newsRecap:
			out.Recap = true
		case newsDone:
			out.Done = append(out.Done, n.Text)
		case newsArtifact:
			if !slices.Contains(out.Artifacts, n.Text) {
				out.Artifacts = append(out.Artifacts, n.Text)
			}
		case newsReply:
			out.Reply = n.Text
		}
	}
	return out
}

// forBoard says the coordinator's board has not shown an item yet.
func (ws *space) forBoard(n workspace.NewsItem) bool { return n.Seq > ws.p.BoardSeq }

// boardRead moves the coordinator's place in the log to its end. Callers
// hold h.mu.
func (h *Hub) boardRead(ws *space) {
	if len(ws.news) == 0 || ws.p.BoardSeq >= ws.news[len(ws.news)-1].Seq {
		return
	}
	ws.p.BoardSeq = ws.news[len(ws.news)-1].Seq
	_ = workspace.Save(ws.p)
	h.rotateNews(ws)
}

// rotateNews drops the news both readers have read and that is old, and
// writes the log when it changed. Callers hold h.mu.
func (h *Hub) rotateNews(ws *space) {
	kept := rotateNews(ws.news, ws.p.BoardSeq, func(name string) (time.Time, bool) {
		th, ok := ws.threads[name]
		if !ok || th.deleted || th.t.Resolved {
			return time.Time{}, false
		}
		return th.t.SeenAt, true
	}, time.Now(), newsMax)
	if len(kept) == len(ws.news) {
		return
	}
	ws.news = kept
	_ = workspace.SaveNews(ws.p.Name, kept)
}

// rotateNews keeps an item unless the board passed it, the person saw its
// thread after it, or the thread is gone, and it is older than newsKeep.
// seenAt says when the person last saw a thread, and false for a thread
// that is gone. At most max items stay, the newest.
func rotateNews(items []workspace.NewsItem, boardSeq int64, seenAt func(string) (time.Time, bool), now time.Time, max int) []workspace.NewsItem {
	var out []workspace.NewsItem
	for _, n := range items {
		at, open := seenAt(n.Thread)
		read := n.Seq <= boardSeq && (!open || !at.Before(n.At))
		if read && now.Sub(n.At) > newsKeep {
			continue
		}
		out = append(out, n)
	}
	if len(out) > max {
		out = out[len(out)-max:]
	}
	return out
}
