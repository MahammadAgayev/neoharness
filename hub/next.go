package hub

import (
	"encoding/json"
	"fmt"
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
	// seen is what the agent last woke for: the waits and the focus.
	seen    string
	timer   *time.Timer
	running bool
	again   bool
}

type stop struct{ key, why string }

func hubWaitKey(w model.Wait) string { return w.Workspace + "\x00" + w.Thread + "\x00" + w.ID }

func briefKey(w model.Wait) string { return hubWaitKey(w) + "\x00" + w.Text }

// waits is the waits list in the order to go through it: the attention
// agent's for what it ordered, then the rest in the hub's order. Callers
// hold h.mu.
func (h *Hub) waits() []model.Wait {
	out := h.hubWaits()
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

// attentionSees wakes the agent when what it reads changed: a wait came or
// went or changed, or the focus did. Its own order changes none of it, so it
// does not wake itself. Callers hold h.mu.
func (h *Hub) attentionSees() {
	if h.cfg.Attention == "" {
		return
	}
	var b strings.Builder
	for _, w := range h.hubWaits() {
		b.WriteString(briefKey(w) + "\x01")
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
		if next, briefs, ok := parseNext(res.Text, ws); ok {
			h.attn.next = next
			if h.attn.briefs == nil {
				h.attn.briefs = map[string]string{}
			}
			// Briefs of waits that are gone go too.
			keep := map[string]string{}
			for _, w := range ws {
				k := briefKey(w)
				if b, ok := briefs[k]; ok {
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
	focus := ""
	if fa, text, err := h.readFocus(); err == nil && !fa.Off && fa.Version > 0 {
		focus = text
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
		Prompt:       prompts.Attention(focus, in),
		SystemPrompt: prompts.AttentionSystem(),
		Cwd:          home,
		NoTools:      true,
		Model:        h.cfg.Agents[h.cfg.Attention].Model,
		Effort:       h.cfg.Agents[h.cfg.Attention].Effort,
	}
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

// parseNext reads the agent's reply: the JSON object in it, which numbers
// the waits ws from 1. A number it does not know, or a reply with no
// object, is no order.
func parseNext(text string, ws []model.Wait) ([]stop, map[string]string, bool) {
	i, j := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if i < 0 || j < i {
		return nil, nil, false
	}
	var out struct {
		Next []struct {
			I     int    `json:"i"`
			Why   string `json:"why"`
			Brief string `json:"brief"`
		} `json:"next"`
	}
	if json.Unmarshal([]byte(text[i:j+1]), &out) != nil {
		return nil, nil, false
	}
	var next []stop
	briefs := map[string]string{}
	seen := map[int]bool{}
	for _, n := range out.Next {
		if n.I < 1 || n.I > len(ws) || seen[n.I] {
			continue
		}
		seen[n.I] = true
		w := ws[n.I-1]
		next = append(next, stop{key: hubWaitKey(w), why: oneLine(n.Why, replyLimit)})
		if b := strings.TrimSpace(n.Brief); b != "" {
			briefs[briefKey(w)] = oneLine(b, replyLimit)
		}
	}
	return next, briefs, true
}
