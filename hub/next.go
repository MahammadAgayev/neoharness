package hub

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/MahammadAgayev/neoharness/api/model"
	"github.com/MahammadAgayev/neoharness/hub/agent"
	"github.com/MahammadAgayev/neoharness/hub/prompts"
	"github.com/MahammadAgayev/neoharness/hub/workspace"
)

// The attention agent orders the waits list by the person's focus and
// briefs each stop, so ]q goes to the best next thing. Each turn is a new
// conversation with no tools: it reads the focus and the waits and replies
// with the order. The hub's own order stands when it is off, fails, or has
// not answered yet.

// DefaultAttentionDelay gathers a burst of changes into one turn.
const DefaultAttentionDelay = 4 * time.Second

type attention struct {
	// next is the agent's order, by wait key, and why each is there.
	next []stop
	// briefs are by wait key and text: a brief stays while its wait is the
	// same, so the agent writes it once.
	briefs map[string]string
	// goes are the places the agent says to go, by wait key, and dropped
	// those the person dismissed since its last order.
	goes    map[string]model.Wait
	dropped map[string]bool
	// status is how all the work stands, in the agent's words.
	status model.Status
	// seen is what the agent last woke for: the waits, the threads and the
	// focus.
	seen    string
	timer   *time.Timer
	running bool
	again   bool
}

type stop struct{ key, why string }

func hubWaitKey(w model.Wait) string { return w.Workspace + "\x00" + w.Thread + "\x00" + w.ID }

func briefKey(w model.Wait) string { return hubWaitKey(w) + "\x00" + w.Text }

// waits is the waits list in the order to go through it: the attention
// agent's for what it ordered, then the rest in the hub's order. The places
// it says to go are in it too, so the list always holds a next thing.
// Callers hold h.mu.
func (h *Hub) waits() []model.Wait {
	out := h.hubWaits()
	waiting := map[string]bool{}
	for _, w := range out {
		waiting[w.Workspace+"\x00"+w.Thread] = true
	}
	for k, g := range h.attn.goes {
		// A thread's own wait says more than a place to go.
		if !h.attn.dropped[k] && !waiting[g.Workspace+"\x00"+g.Thread] && h.open(g.Workspace, g.Thread) {
			out = append(out, g)
		}
	}
	if len(out) == 0 {
		if g, ok := h.movedLast(); ok {
			out = append(out, g)
		}
	}
	if len(h.attn.next) == 0 {
		return out
	}
	at := map[string]int{}
	for i, s := range h.attn.next {
		at[s.key] = i
	}
	var ranked, rest []model.Wait
	for _, w := range out {
		i, ok := at[hubWaitKey(w)]
		if !ok {
			rest = append(rest, w)
			continue
		}
		w.Why, w.Brief = h.attn.next[i].why, h.attn.briefs[briefKey(w)]
		ranked = append(ranked, w)
	}
	slices.SortStableFunc(ranked, func(a, b model.Wait) int { return at[hubWaitKey(a)] - at[hubWaitKey(b)] })
	return append(ranked, rest...)
}

// open says a thread is there to go to.
func (h *Hub) open(workspaceName, name string) bool {
	ws, ok := h.workspaces[workspaceName]
	if !ok {
		return false
	}
	th, ok := ws.threads[name]
	return ok && !th.deleted && !th.t.Resolved
}

// movedLast is the coordinator of the workspace whose journal moved last,
// as the place to go when nothing else is. Callers hold h.mu.
func (h *Hub) movedLast() (model.Wait, bool) {
	var out model.Wait
	for name, ws := range h.workspaces {
		w := model.Wait{Workspace: name, Thread: model.Coordinator, Kind: model.WaitNext, ID: model.NextID, Text: "the work moved here last"}
		if n := len(ws.journal); n > 0 {
			w.Since = ws.journal[n-1].At
		}
		if !h.open(name, model.Coordinator) || h.attn.dropped[hubWaitKey(w)] {
			continue
		}
		if out.Workspace == "" || w.Since.After(out.Since) || w.Since.Equal(out.Since) && name < out.Workspace {
			out = w
		}
	}
	return out, out.Workspace != ""
}

// attentionSees wakes the agent when what it reads changed: a wait came or
// went or changed, a thread's state or recap did, or the focus did. Its own order changes none of it, so it
// does not wake itself. Callers hold h.mu.
func (h *Hub) attentionSees() {
	if h.cfg.Attention == "" {
		return
	}
	var b strings.Builder
	for _, w := range h.hubWaits() {
		b.WriteString(briefKey(w) + "\x01")
	}
	// Todo progress is left out: it would wake the agent at every item.
	for _, t := range h.attentionBoard() {
		t.Done, t.Now, t.News.Done = 0, "", nil
		fmt.Fprintf(&b, "%v\x01", t)
	}
	fa, ftext, _ := h.readFocus()
	fmt.Fprintf(&b, "%d %v %d", fa.Version, fa.Off, len(ftext))
	if sig := b.String(); sig != h.attn.seen {
		h.attn.seen = sig
		h.wakeAttention()
	}
}

// wakeAttention starts the agent's turn after the delay, or after the turn
// that runs now. Callers hold h.mu.
func (h *Hub) wakeAttention() {
	switch {
	case h.attn.running:
		h.attn.again = true
	case h.attn.timer == nil:
		h.attn.timer = time.AfterFunc(cmpDuration(h.cfg.AttentionDelay, DefaultAttentionDelay), h.runAttention)
	}
}

func cmpDuration(d, or time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return or
}

// runAttention is one turn of the attention agent.
func (h *Hub) runAttention() {
	h.mu.Lock()
	h.attn.timer = nil
	if h.ctx.Err() != nil {
		h.mu.Unlock()
		return
	}
	h.attn.running = true
	ws := h.hubWaits()
	req := h.attentionRequest(ws)
	ag := h.cfg.Factory(h.cfg.Attention, workspace.Workspace{}, workspace.Thread{Name: "attention"})
	h.wg.Add(1)
	h.mu.Unlock()

	res, err := ag.Run(h.ctx, req)

	h.mu.Lock()
	defer h.mu.Unlock()
	defer h.wg.Done()
	h.attn.running = false
	if err == nil && res.ExitCode == 0 {
		if r, ok := parseNext(res.Text, ws); ok {
			if r.status != nil && !slices.EqualFunc(r.status, h.attn.status.Lines, sameLine) {
				h.attn.status = model.Status{Lines: r.status, At: time.Now().UTC()}
				s := h.attn.status
				h.publish(model.Update{Kind: model.UpdateStatus, Status: &s})
			}
			if r.edit != nil && h.cfg.FocusEdit != FocusEditOff {
				h.suggest(*r.edit)
			}
			h.attn.next, h.attn.goes, h.attn.dropped = r.next, r.goes, nil
			if h.attn.briefs == nil {
				h.attn.briefs = map[string]string{}
			}
			// Briefs of waits that are gone go too.
			keep := map[string]string{}
			for _, w := range append(ws, slices.Collect(maps.Values(r.goes))...) {
				k := briefKey(w)
				if b, ok := r.briefs[k]; ok {
					keep[k] = b
				} else if b, ok := h.attn.briefs[k]; ok {
					keep[k] = b
				}
			}
			h.attn.briefs = keep
			h.publishWaits()
		}
	}
	if h.attn.again {
		h.attn.again = false
		h.wakeAttention()
	}
}

// attentionRequest is the agent's turn for the waits ws. Callers hold h.mu.
func (h *Hub) attentionRequest(ws []model.Wait) agent.Request {
	focus, suggested := "", ""
	if fa, text, err := h.readFocus(); err == nil && !fa.Off && fa.Version > 0 {
		focus = text
	}
	if s := h.focus.Suggestion; s != nil {
		suggested = s.Why
	}
	in := make([]prompts.AttentionWait, len(ws))
	now := time.Now()
	for i, w := range ws {
		a := prompts.AttentionWait{Workspace: w.Workspace, Thread: w.Thread, Kind: string(w.Kind), Text: w.Text, Age: ageOf(now.Sub(w.Since))}
		_, a.New = h.attn.briefs[briefKey(w)]
		a.New = !a.New
		if sp, ok := h.workspaces[w.Workspace]; ok {
			if th, ok := sp.threads[w.Thread]; ok {
				a.State, a.Recap, a.Reply = string(th.state), th.t.Recap, th.t.Reply
			}
		}
		in[i] = a
	}
	home, _ := os.UserHomeDir()
	return agent.Request{
		Prompt:       prompts.Attention(focus, suggested, in, h.attentionBoard()),
		SystemPrompt: prompts.AttentionSystem(focus != "" && h.cfg.FocusEdit != FocusEditOff),
		Cwd:          home,
		NoTools:      true,
		Model:        h.cfg.Agents[h.cfg.Attention].Model,
		Effort:       h.cfg.Agents[h.cfg.Attention].Effort,
	}
}

// attentionBoard is every open thread of every workspace, as the
// coordinator's board shows them, with the news the person has not seen.
// Callers hold h.mu.
func (h *Hub) attentionBoard() []prompts.BoardThread {
	var out []prompts.BoardThread
	for _, name := range slices.Sorted(maps.Keys(h.workspaces)) {
		ws := h.workspaces[name]
		for _, n := range slices.Sorted(maps.Keys(ws.threads)) {
			th := ws.threads[n]
			if th.deleted || th.t.Resolved {
				continue
			}
			b := boardThread(th)
			b.Name = name + " · " + n
			b.News, _ = newsOf(ws, n, func(e model.JournalEntry) bool { return e.At.After(th.f.seen.At) })
			if b.News.Reply == "" {
				b.Reply = th.t.Reply
			}
			out = append(out, b)
		}
	}
	return out
}

// Status is how all the work stands, as the attention agent last wrote it.
func (h *Hub) Status() model.Status {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.attn.status
	s.Lines = slices.Clone(s.Lines)
	for i := range s.Lines {
		s.Lines[i].Parts = slices.Clone(s.Lines[i].Parts)
	}
	return s
}

func sameLine(a, b model.StatusLine) bool {
	return a.Where == b.Where && slices.Equal(a.Parts, b.Parts)
}

// ageOf is how long a wait has waited, as the agent reads it.
func ageOf(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("waited %dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("waited %dh", int(d.Hours()))
	}
	return fmt.Sprintf("waited %dd", int(d.Hours()/24))
}

// reply is what the attention agent said in one turn.
type reply struct {
	next   []stop
	briefs map[string]string
	goes   map[string]model.Wait
	edit   *model.Suggestion
	status []model.StatusLine
}

// parseNext reads the agent's reply: the JSON object in it, which numbers
// the waits ws from 1 and names the places to go as "workspace · thread",
// or "workspace" for its coordinator. A number it does not know, or a
// reply with no object, is no order. The status is nil when the reply has
// none.
func parseNext(text string, ws []model.Wait) (reply, bool) {
	var r reply
	i, j := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if i < 0 || j < i {
		return r, false
	}
	var out struct {
		Status json.RawMessage `json:"status"`
		Next   []struct {
			I     int    `json:"i"`
			Go    string `json:"go"`
			Why   string `json:"why"`
			Brief string `json:"brief"`
		} `json:"next"`
		Edit *struct {
			Text string `json:"text"`
			Why  string `json:"why"`
		} `json:"edit"`
	}
	if json.Unmarshal([]byte(text[i:j+1]), &out) != nil {
		return r, false
	}
	r.status = parseStatus(out.Status)
	if e := out.Edit; e != nil && strings.TrimSpace(e.Text) != "" {
		r.edit = &model.Suggestion{Text: e.Text, Why: oneLine(e.Why, replyLimit)}
	}
	r.briefs, r.goes = map[string]string{}, map[string]model.Wait{}
	now := time.Now().UTC()
	seen := map[string]bool{}
	for _, n := range out.Next {
		var w model.Wait
		switch brief := oneLine(n.Brief, replyLimit); {
		case n.I >= 1 && n.I <= len(ws):
			w = ws[n.I-1]
		case strings.TrimSpace(n.Go) != "" && brief != "":
			name, thread, _ := strings.Cut(n.Go, "·")
			if thread = strings.TrimSpace(thread); thread == "" {
				thread = model.Coordinator
			}
			w = model.Wait{Workspace: strings.TrimSpace(name), Thread: thread, Kind: model.WaitNext, ID: model.NextID, Text: brief, Since: now}
		default:
			continue
		}
		k := hubWaitKey(w)
		if seen[k] {
			continue
		}
		seen[k] = true
		if w.Kind == model.WaitNext {
			r.goes[k] = w
		}
		r.next = append(r.next, stop{key: k, why: oneLine(n.Why, replyLimit)})
		if b := oneLine(n.Brief, replyLimit); b != "" {
			r.briefs[briefKey(w)] = b
		}
	}
	return r, true
}

// parseStatus reads the agent's status: one line, as a string or as the
// first of a list of lines. It is nil when the reply has none.
func parseStatus(raw json.RawMessage) []model.StatusLine {
	if raw == nil {
		return nil
	}
	var lines []json.RawMessage
	if json.Unmarshal(raw, &lines) != nil {
		lines = []json.RawMessage{raw}
	}
	out := []model.StatusLine{}
	for _, l := range lines {
		if l, ok := statusLine(l); ok && len(out) < statusLines {
			out = append(out, l)
		}
	}
	return out
}

// statusLines is the most lines of status the person reads: one, above
// the next things to do.
const statusLines = 1

// statusLine reads one line of the agent's status. A plain string, as a
// model may still write, is one part with no where.
func statusLine(raw json.RawMessage) (model.StatusLine, bool) {
	var l model.StatusLine
	if json.Unmarshal(raw, &l) != nil {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return l, false
		}
		l = model.StatusLine{Parts: []string{s}}
	}
	l.Where = oneLine(l.Where, replyLimit)
	var parts []string
	for _, p := range l.Parts {
		if p = oneLine(p, replyLimit); p != "" {
			parts = append(parts, p)
		}
	}
	l.Parts = parts
	return l, len(parts) > 0
}

// suggest takes the agent's edit of the focus: it waits for the person, or
// with focus_edit = "allow" it is saved at once as the agent's version. An
// edit that changes nothing, or that the person dismissed, is dropped.
// Callers hold h.mu.
func (h *Hub) suggest(s model.Suggestion) {
	a, text, err := h.readFocus()
	if err != nil || a.Version == 0 || s.Text == text || s.Text == h.focus.Dismissed {
		return
	}
	if s.Text != "" && !strings.HasSuffix(s.Text, "\n") && strings.HasSuffix(text, "\n") {
		s.Text += "\n"
		if s.Text == text {
			return
		}
	}
	if h.cfg.FocusEdit == FocusEditAllow {
		a.Off, a.Suggestion = false, nil
		a.Version++
		a.Title, a.UpdatedAt, a.UserEdit, a.Thread = title(s.Text, model.FocusDoc), time.Now().UTC(), false, "attention"
		if workspace.SaveArtifact(model.HubScope, a, s.Text) == nil {
			_ = h.focusChanged()
		}
		return
	}
	if cur := h.focus.Suggestion; cur != nil && cur.Text == s.Text {
		return
	}
	s.Base, s.At = a.Version, time.Now().UTC()
	h.focus.Suggestion = &s
	_ = h.focusChanged()
	h.publishWaits()
}

// dismissSuggestion is x on the focus's suggested edit: it goes, and the
// agent does not suggest it again. Callers hold h.mu.
func (h *Hub) dismissSuggestion(id string) error {
	s := h.focus.Suggestion
	if id != model.SuggestionID || s == nil {
		return fmt.Errorf("nothing waits as %q in the hub", id)
	}
	h.focus.Suggestion, h.focus.Dismissed = nil, s.Text
	if err := h.focusChanged(); err != nil {
		return err
	}
	h.publishWaits()
	return nil
}

// dismissNext is x on a place to go: it goes until the agent's next order.
// Callers hold h.mu.
func (h *Hub) dismissNext(workspaceName, name string) error {
	k := hubWaitKey(model.Wait{Workspace: workspaceName, Thread: name, ID: model.NextID})
	if !slices.ContainsFunc(h.waits(), func(w model.Wait) bool { return hubWaitKey(w) == k }) {
		return fmt.Errorf("nothing waits as %q on %s", model.NextID, name)
	}
	if h.attn.dropped == nil {
		h.attn.dropped = map[string]bool{}
	}
	h.attn.dropped[k] = true
	h.publishWaits()
	return nil
}
