package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/MahammadAgayev/neoharness/api/model"
)

// costHub is the fake hub with a cost to add up.
type costHub struct {
	*fakeClient
	asked []string
}

func (s *costHub) Cost(workspace string) (model.CostReport, error) {
	s.asked = append(s.asked, workspace)
	days := make([]model.CostPart, 14)
	for i := range days {
		days[i] = model.CostPart{Name: time.Now().AddDate(0, 0, i-13).Format("2006-01-02")}
	}
	days[13].Cost, days[10].Cost = 1.5, 3
	sp := model.CostReport{
		Total: 24.75, Today: 1.5, Since: time.Now().AddDate(0, 0, -13), Days: days,
		Threads: []model.CostPart{{Workspace: "demo", Name: "api-tests", Cost: 12.5}, {Workspace: "demo", Name: model.Coordinator, Cost: 9.25}},
		Models:  []model.CostPart{{Name: "claude-opus-5-5", Cost: 19.5}},
		Top:     []model.TurnCost{{At: time.Now(), Workspace: "demo", Thread: "api-tests", Model: "claude-opus-5-5", Cost: 1.42}},
	}
	if workspace == "" {
		sp.Workspaces = []model.CostPart{{Name: "demo", Cost: 24.75}}
	}
	return sp, nil
}

func TestCostShowsTheCostAndOpensAThread(t *testing.T) {
	h := start(t)
	sh := &costHub{fakeClient: h.hub}
	h.a.c = sh
	h.press("<CR>")
	h.press(":cost", "<CR>")
	b := h.a.cur.Buf
	if b.Kind != KindCost || b.Workspace != "demo" || sh.asked[0] != "demo" {
		t.Fatalf("buffer %v %q, asked %q", b.Kind, b.Workspace, sh.asked)
	}
	screen := h.screen()
	for _, want := range []string{"$24.75 in all", "$1.50 today", "$4.50 last 14 days", "by thread", "api-tests", "$12.50", "opus-5-5", "turns that cost the most"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("screen lacks %q:\n%s", want, screen)
		}
	}
	if strings.Contains(screen, "by workspace") {
		t.Fatal("one workspace has no breakdown by workspace")
	}
	// <CR> on the costliest turn, the last row that opens a thread, opens it.
	for i, l := range b.Lines(120, h.a.th, 0) {
		if strings.HasPrefix(l.Item, costRow) {
			h.a.cur.Cur = i
		}
	}
	h.press("<CR>")
	if h.a.cur.Buf.Kind != KindChat || h.a.cur.Buf.Thread != "api-tests" {
		t.Fatalf("<CR> on a turn should open api-tests; got %q", h.a.cur.Buf.Name)
	}

	h.press(":cost all", "<CR>")
	if sh.asked[1] != "" || !strings.Contains(h.screen(), "by workspace") || !strings.Contains(h.screen(), "cost of every workspace") {
		t.Fatalf(":cost all should show every workspace:\n%s", h.screen())
	}
}
