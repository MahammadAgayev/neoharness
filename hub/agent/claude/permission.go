package claude

import (
	"encoding/json"
	"strings"

	"github.com/MahammadAgayev/neoharness/hub/agent"
)

// HarnessTool reports whether a tool Claude asks about is one of the
// neoharness tools. They only reach the hub, and a session that had to ask
// before reporting progress would wait on the person for nothing.
func HarnessTool(name string) bool { return strings.HasPrefix(name, harnessTools) }

// Ask turns Claude's permission prompt for a tool call into the hub's terms.
func Ask(tool string, input json.RawMessage) agent.Permission {
	prefix, compound := rulePrefix(tool, input)
	return agent.Permission{Tool: tool, Detail: inputDetail(input), Prefix: prefix, Compound: compound}
}

// inputDetail picks the part of a tool's input worth showing in a permission
// prompt.
func inputDetail(input json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(input, &m) != nil {
		return ""
	}
	for _, k := range []string{"command", "file_path", "path", "url", "pattern"} {
		if s, ok := m[k].(string); ok && s != "" {
			return strings.Join(strings.Fields(s), " ")
		}
	}
	for _, v := range m {
		if s, ok := v.(string); ok && s != "" {
			return strings.Join(strings.Fields(s), " ")
		}
	}
	return ""
}

// rulePrefix is what an "allow always" remembers. For Bash it is the
// command's first word; for every other tool the whole tool is allowed.
//
// compound reports a Bash command that does more than one thing. Those are
// always asked about: an allowed `go` must not let `go test; rm -rf ~` through
// on the strength of its first word.
func rulePrefix(tool string, input json.RawMessage) (prefix string, compound bool) {
	if tool != "Bash" {
		return "", false
	}
	var m struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(input, &m)
	compound = strings.ContainsAny(m.Command, ";&|`<>\n") || strings.Contains(m.Command, "$(")
	if f := strings.Fields(m.Command); len(f) > 0 {
		return f[0], compound
	}
	return "", compound
}
