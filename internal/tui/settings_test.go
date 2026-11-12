package tui

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestHubSettingsDefaultToOpus(t *testing.T) {
	s, err := ReadHubSettings(filepath.Join(t.TempDir(), "init.lua"))
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Selected()
	if err != nil {
		t.Fatal(err)
	}
	if s.MCPPort != 47821 || a.Kind != "claude" || a.Model != "claude-opus-5-5[1m]" || a.Effort != "" || a.ContextWindow != 400000 ||
		!slices.Equal(a.Command, []string{"claude"}) || !slices.Equal(a.RemoteCommand, []string{"claude"}) {
		t.Fatalf("settings = %+v", s)
	}
}

func TestInitLuaSetupChangesOnlyItsKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "init.lua")
	src := `
neoharness.o.leader = ","
neoharness.keymap.set("n", "Q", ":help<CR>")
neoharness.cmd("workspace demo")
require("neoharness").setup({ mcp_port = 47999 })
require("neoharness.claude").setup({ command = "authwrap agent run claude", model = "sonnet", effort = "high" })
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := ReadHubSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	a := s.Agents["claude"]
	if s.MCPPort != 47999 || a.Model != "sonnet" || a.Effort != "high" ||
		!slices.Equal(a.Command, []string{"authwrap", "agent", "run", "claude"}) ||
		!slices.Equal(a.RemoteCommand, []string{"claude"}) {
		t.Fatalf("settings = %+v", s)
	}
}

func TestInitLuaAddsAndSelectsAnAgent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "init.lua")
	src := `
require("neoharness.agents").setup({ fast = { kind = "claude", model = "sonnet" } })
require("neoharness").setup({ agent = "fast" })
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := ReadHubSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Selected()
	if err != nil {
		t.Fatal(err)
	}
	if a.Kind != "claude" || a.Model != "sonnet" || a.Command != nil {
		t.Fatalf("selected = %+v", a)
	}
	if s.Agents["claude"].Model != "claude-opus-5-5[1m]" {
		t.Fatalf("the default agent changed: %+v", s.Agents["claude"])
	}
}

func TestUnknownAgentIsAnError(t *testing.T) {
	s := HubSettings{Agent: "codex", Agents: map[string]AgentSettings{"claude": {}}}
	if _, err := s.Selected(); err == nil {
		t.Fatal("want an error for an agent that is not set up")
	}
}

func TestTheAPIIsNeoharnessNotVim(t *testing.T) {
	h := start(t)
	h.a.runConfig(`assert(require("neoharness") == neoharness)`, "init.lua", false)
	if h.a.notice != "" {
		t.Fatalf("require(\"neoharness\") should be the neoharness table, got %q", h.a.notice)
	}
	h.a.runConfig(`vim.o.equalalways = false`, "init.lua", false)
	if h.a.notice == "" {
		t.Fatal("vim should not exist")
	}
}

func TestTUIAcceptsTheSetupCalls(t *testing.T) {
	h := start(t)
	h.a.runConfig(`require("neoharness.claude").setup({ model = "sonnet" })
require("neoharness.agents").setup({ claude = { model = "sonnet" } })`, "init.lua", false)
	if h.a.notice != "" {
		t.Fatalf("the TUI should accept setup calls, got %q", h.a.notice)
	}
}
