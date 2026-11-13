package claude

import (
	"encoding/json"
	"net/url"
	"path"
	"strings"

	"github.com/MahammadAgayev/neoharness/hub/agent"
)

// HarnessTool reports whether a tool Claude asks about is one of the
// neoharness tools. They only reach the hub, and a session that had to ask
// before reporting progress would wait on the person for nothing.
func HarnessTool(name string) bool { return strings.HasPrefix(name, harnessTools) }

// Ask turns Claude's permission prompt for a tool call into the hub's terms.
// dir is the thread's folder: an "always" for a file edit covers only that
// folder.
func Ask(tool string, input json.RawMessage, dir string) agent.Permission {
	return agent.Permission{Tool: tool, Detail: inputDetail(input), Scope: scope(tool, input, dir)}
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

// scope is what an "allow always" remembers, and the card shows: a Bash
// command and its subcommand, the folder of a file edit, the domain of a
// fetch, else the whole tool. Empty when no rule should cover the call.
func scope(tool string, input json.RawMessage, dir string) string {
	var m struct {
		Command      string `json:"command"`
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
		URL          string `json:"url"`
	}
	_ = json.Unmarshal(input, &m)
	switch tool {
	case "Bash":
		return bashScope(m.Command)
	case "Write", "Edit", "MultiEdit", "NotebookEdit":
		p := path.Clean(m.FilePath + m.NotebookPath)
		d := strings.TrimSuffix(path.Clean(dir), "/") + "/"
		if dir == "" || !strings.HasPrefix(p, d) {
			return ""
		}
		return d
	case "WebFetch":
		u, err := url.Parse(m.URL)
		if err != nil {
			return ""
		}
		return u.Hostname()
	}
	return tool
}

// subcommands are tools whose second word says what they do: `git status`
// and `git push` are different requests.
var subcommands = map[string]bool{
	"git": true, "go": true, "npm": true, "pnpm": true, "yarn": true, "bun": true,
	"cargo": true, "docker": true, "kubectl": true, "gh": true, "brew": true,
	"pip": true, "pip3": true, "uv": true, "bazel": true, "terraform": true,
	"helm": true, "swift": true, "dotnet": true, "mvn": true, "gradle": true,
}

// neverAlways are commands that run whatever they are given or delete
// things; one approval of them says nothing about the next call.
var neverAlways = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "fish": true, "dash": true,
	"env": true, "xargs": true, "sudo": true, "doas": true, "su": true,
	"exec": true, "eval": true, "command": true, "nohup": true, "time": true,
	"timeout": true, "watch": true, "nice": true, "find": true,
	"python": true, "python3": true, "node": true, "deno": true, "ruby": true,
	"perl": true, "php": true, "osascript": true, "npx": true, "bunx": true,
	"rm": true, "dd": true, "ssh": true, "scp": true, "curl": true, "wget": true,
}

// destructive flags undo work or skip checks wherever they appear.
var destructive = map[string]bool{
	"--force": true, "-f": true, "--force-with-lease": true, "--hard": true,
	"-rf": true, "-fr": true, "-fd": true, "-fdx": true, "-D": true,
	"--delete": true, "--no-verify": true, "--mirror": true, "--prune": true,
}

// bashScope is the scope of a shell command. A command that does more than
// one thing, through `;`, `&`, `|`, `` ` ``, `<`, `>`, a newline or `$(`, has
// none: an allowed `go test` must not let `go test; rm -rf ~` through.
func bashScope(cmd string) string {
	if strings.ContainsAny(cmd, ";&|`<>\n") || strings.Contains(cmd, "$(") {
		return ""
	}
	f := strings.Fields(cmd)
	if len(f) == 0 || strings.Contains(f[0], "=") || neverAlways[path.Base(f[0])] {
		return ""
	}
	for _, w := range f[1:] {
		if destructive[w] {
			return ""
		}
	}
	if !subcommands[f[0]] || len(f) == 1 {
		return f[0]
	}
	// `git -c core.pager=… log` sets options before the subcommand.
	if strings.HasPrefix(f[1], "-") {
		return ""
	}
	return f[0] + " " + f[1]
}
