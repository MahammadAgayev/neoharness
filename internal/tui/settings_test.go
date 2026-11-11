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
	if s.MCPPort != 47821 || s.Claude.Model != "claude-opus-5-5[1m]" || s.Claude.Effort != "" || s.Claude.ContextWindow != 400000 ||
		!slices.Equal(s.Claude.Command, []string{"claude"}) || !slices.Equal(s.Claude.RemoteCommand, []string{"claude"}) {
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
	if s.MCPPort != 47999 || s.Claude.Model != "sonnet" || s.Claude.Effort != "high" ||
		!slices.Equal(s.Claude.Command, []string{"authwrap", "agent", "run", "claude"}) ||
		!slices.Equal(s.Claude.RemoteCommand, []string{"claude"}) {
		t.Fatalf("settings = %+v", s)
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
	h.a.runConfig(`require("neoharness.claude").setup({ model = "sonnet" })`, "init.lua", false)
	if h.a.notice != "" {
		t.Fatalf("the TUI should accept setup calls, got %q", h.a.notice)
	}
}
