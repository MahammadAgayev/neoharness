package model

import (
	"fmt"
	"strings"
)

// Transcript renders events as plain text, one per line.
func Transcript(evs []Event) string {
	if len(evs) == 0 {
		return "No events."
	}
	var b strings.Builder
	tools := map[string]string{}
	for _, e := range evs {
		fmt.Fprintf(&b, "%s ", e.At.Format("15:04"))
		switch e.Kind {
		case EventMessage:
			fmt.Fprintf(&b, "message from %s: %s", e.From, e.Text)
		case EventText:
			fmt.Fprintf(&b, "reply: %s", e.Text)
		case EventTool:
			if e.Exit != 0 {
				b.WriteString("the tool call failed")
				if t := tools[e.ID]; t != "" {
					fmt.Fprintf(&b, ": %s", t)
				}
				if e.Text != "" {
					fmt.Fprintf(&b, ": %s", e.Text)
				}
				break
			}
			if e.ID != "" {
				tools[e.ID] = e.Tool
			}
			fmt.Fprintf(&b, "tool %s: %s", e.Tool, e.Detail)
		case EventPermission:
			fmt.Fprintf(&b, "permission asked for %s: %s", e.Tool, e.Detail)
		case EventAnswer:
			fmt.Fprintf(&b, "permission for %s: %s", e.Tool, e.Text)
		case EventTurnEnd:
			fmt.Fprintf(&b, "turn ended, %d tool calls", e.Tools)
		case EventArtifact:
			if e.From == User {
				fmt.Fprintf(&b, "the person edited artifact %s, version %d", e.Detail, e.Version)
			} else {
				fmt.Fprintf(&b, "wrote artifact %s (%q), version %d", e.Detail, e.Text, e.Version)
			}
		case EventShell:
			if e.Text == "" && e.Exit == 0 {
				fmt.Fprintf(&b, "the person started ! %s", e.Detail)
			} else {
				fmt.Fprintf(&b, "the person ran ! %s, exit %d", e.Detail, e.Exit)
			}
		default:
			fmt.Fprintf(&b, "%s: %s", e.Kind, e.Text)
		}
		b.WriteByte('\n')
	}
	return b.String()
}
