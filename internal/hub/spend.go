package hub

import (
	"cmp"
	"slices"
	"time"

	"github.com/MahammadAgayev/neoharness/internal/model"
	"github.com/MahammadAgayev/neoharness/internal/workspace"
)

// spendDays is how many days Spend shows, today the last.
const spendDays = 14

// spendTop is how many of the turns that cost the most Spend lists.
const spendTop = 10

// Spend is what a workspace cost, or every workspace when the name is empty.
func (h *Hub) Spend(workspaceName string) (model.Spend, error) {
	h.mu.Lock()
	var spaces []*space
	if workspaceName == "" {
		for _, ws := range h.workspaces {
			spaces = append(spaces, ws)
		}
	} else {
		ws, err := h.workspace(workspaceName)
		if err != nil {
			h.mu.Unlock()
			return model.Spend{}, err
		}
		spaces = []*space{ws}
	}
	out := model.Spend{Threads: []model.SpendPart{}, Workspaces: []model.SpendPart{}, Models: []model.SpendPart{}, Top: []model.TurnCost{}}
	var names []string
	for _, ws := range spaces {
		names = append(names, ws.p.Name)
		total := 0.0
		for _, th := range ws.threads {
			if th.t.Cost == 0 {
				continue
			}
			total += th.t.Cost
			out.Threads = append(out.Threads, model.SpendPart{Workspace: ws.p.Name, Name: th.t.Name, Cost: th.t.Cost})
		}
		out.Total += total
		if workspaceName == "" && total > 0 {
			out.Workspaces = append(out.Workspaces, model.SpendPart{Name: ws.p.Name, Cost: total})
		}
	}
	h.mu.Unlock()

	// The logs are read outside the lock: they only grow, and a turn that
	// ends meanwhile shows next time.
	now := time.Now()
	day := func(t time.Time) string { return t.Local().Format("2006-01-02") }
	days := map[string]*model.SpendPart{}
	for i := spendDays - 1; i >= 0; i-- {
		d := day(now.AddDate(0, 0, -i))
		out.Days = append(out.Days, model.SpendPart{Name: d})
		days[d] = &out.Days[len(out.Days)-1]
	}
	models := map[string]*model.SpendPart{}
	turns := map[[2]string]int{}
	for _, name := range names {
		log, err := workspace.Spends(name)
		if err != nil {
			return model.Spend{}, err
		}
		for _, c := range log {
			c.Workspace = name
			if out.Since.IsZero() || c.At.Before(out.Since) {
				out.Since = c.At
			}
			if d, ok := days[day(c.At)]; ok {
				d.Cost += c.Cost
				d.Turns++
			}
			m := cmp.Or(c.Model, "unknown")
			if models[m] == nil {
				models[m] = &model.SpendPart{Name: m}
			}
			models[m].Cost += c.Cost
			models[m].Turns++
			turns[[2]string{name, c.Thread}]++
			out.Top = append(out.Top, c)
		}
	}
	out.Today = out.Days[len(out.Days)-1].Cost
	for _, m := range models {
		out.Models = append(out.Models, *m)
	}
	for i := range out.Threads {
		out.Threads[i].Turns = turns[[2]string{out.Threads[i].Workspace, out.Threads[i].Name}]
	}
	most := func(a, b model.SpendPart) int { return cmp.Compare(b.Cost, a.Cost) }
	slices.SortFunc(out.Threads, most)
	slices.SortFunc(out.Workspaces, most)
	slices.SortFunc(out.Models, most)
	slices.SortStableFunc(out.Top, func(a, b model.TurnCost) int { return cmp.Compare(b.Cost, a.Cost) })
	out.Top = out.Top[:min(len(out.Top), spendTop)]
	return out, nil
}
