package tui

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/MahammadAgayev/neoharness/api/model"
)

func TestAFailedToolHasAnAlertUndercurlAndLinksADottedLine(t *testing.T) {
	th := newTheme(256)
	b := newBuffer("c", KindChat)
	b.AddEvent(model.Event{Kind: model.EventTool, Tool: "Bash", Detail: "go test ./...", ID: "t1"})
	b.AddEvent(model.Event{Kind: model.EventTool, ID: "t1", Exit: 1, Text: "exit status 1"})
	b.AddEvent(model.Event{Kind: model.EventTurnEnd})
	for i, bl := range b.blocks {
		if bl.kind == bTools {
			b.Toggle(i)
		}
	}
	found := false
	for _, l := range b.Lines(80, th, 0) {
		for _, sp := range l.Spans {
			if sp.Text == "  go test ./..." {
				found = true
				if sp.Style.GetUnderlineStyle() != tcell.UnderlineStyleCurly || sp.Style.GetUnderlineColor() != fgOf(th.Alert) {
					t.Fatal("the failed call's command should have an alert undercurl")
				}
			}
		}
	}
	if !found {
		t.Fatal("the open fold should show the failed command")
	}
	if st := linkStyle(th, "https://x.dev"); st.GetUnderlineStyle() != tcell.UnderlineStyleDotted || st.GetUnderlineColor() != fgOf(th.Run) {
		t.Fatal("a link has a dotted underline in run")
	}
}
