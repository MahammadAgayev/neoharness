// Package prompts holds the system prompts of hub sessions: the coordinator
// of a project and its threads.
package prompts

import (
	_ "embed"
	"strings"
)

//go:embed coordinator.md
var coordinatorMD string

//go:embed thread.md
var threadMD string

// SessionInput is what a hub session's system prompt depends on.
type SessionInput struct {
	Project  string
	Name     string
	Location string
}

// Coordinator is the system prompt for a project's coordinator.
func Coordinator(in SessionInput) string { return fill(coordinatorMD, in) }

// Thread is the system prompt for a thread.
func Thread(in SessionInput) string { return fill(threadMD, in) }

func fill(tmpl string, in SessionInput) string {
	return strings.NewReplacer(
		"{{project}}", in.Project,
		"{{name}}", in.Name,
		"{{location}}", in.Location,
	).Replace(tmpl)
}
