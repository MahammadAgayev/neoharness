package main

// The import rules that keep the hub and TUI tests fast: they run in seconds
// without a model or a terminal. They are easy to break by accident, one
// convenient import is all it takes, so they live here as failing tests.
//
// The modules carry the large rules: api, hub and tui are Go modules of
// their own, and a module builds only against the modules its go.mod
// requires. The tests below keep those go.mod files honest and check the
// rules inside a module.

import (
	"go/build"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const repo = "github.com/MahammadAgayev/neoharness"

// repoRoot locates the repository from this file's own path, so the test
// does not depend on the working directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	// .../cmd/neoharness/architecture_test.go -> repository root
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

// imports returns the non-test imports of one package.
func imports(t *testing.T, pkg string) []string {
	t.Helper()
	p, err := build.ImportDir(filepath.Join(repoRoot(t), pkg), 0)
	if err != nil {
		t.Fatalf("reading %s: %v", pkg, err)
	}
	return p.Imports
}

// TestModulesRequireOnlyTheAPI: the TUI talks to the hub only through the
// API, and the hub knows no TUI. go.work would build an import that a go.mod
// does not require, so the go.mod files are what this checks.
func TestModulesRequireOnlyTheAPI(t *testing.T) {
	forbidden := map[string][]string{
		"api": {repo + "/hub", repo + "/tui"},
		"hub": {repo + "/tui"},
		"tui": {repo + "/hub"},
	}
	for mod, bad := range forbidden {
		b, err := os.ReadFile(filepath.Join(repoRoot(t), mod, "go.mod"))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			for _, f := range bad {
				if strings.Contains(line, f+" ") || strings.Contains(line, f+"\t") || strings.HasSuffix(line, f) {
					t.Errorf("%s/go.mod names %s: %s", mod, f, strings.TrimSpace(line))
				}
			}
		}
	}
}

// TestAgentInterfaceKnowsNoAdapters keeps the boundary a boundary. If the
// interface package imported an adapter, the adapters could not import it.
func TestAgentInterfaceKnowsNoAdapters(t *testing.T) {
	for _, imp := range imports(t, "hub/agent") {
		if strings.HasPrefix(imp, repo+"/") {
			t.Errorf("hub/agent should depend on nothing of ours, imports %s", imp)
		}
	}
}

// TestPromptsDependsOnlyOnItself keeps prompt text free of hub state, so what
// an agent is told can be checked without a hub.
func TestPromptsDependsOnlyOnItself(t *testing.T) {
	for _, imp := range imports(t, "hub/prompts") {
		if strings.HasPrefix(imp, repo+"/") {
			t.Errorf("hub/prompts imports %s — prompts should take plain inputs", imp)
		}
	}
}

// TestHubKnowsNoTransport keeps the hub testable with a fake agent: it must
// not know which CLI runs a turn, how MCP or the local API are served, or how
// remote hosts are reached.
func TestHubKnowsNoTransport(t *testing.T) {
	forbidden := []string{
		repo + "/hub/agent/claude",
		repo + "/hub/mcpserver",
		repo + "/hub/hubserver",
		repo + "/hub/tunnel",
		"github.com/modelcontextprotocol/go-sdk",
	}
	for _, imp := range imports(t, "hub") {
		for _, bad := range forbidden {
			if imp == bad || strings.HasPrefix(imp, bad+"/") {
				t.Errorf("hub imports %s", imp)
			}
		}
	}
}

// TestDataPackagesStayLeaves: model and linediff depend on nothing of ours,
// and workspace and hubapi only on model.
func TestDataPackagesStayLeaves(t *testing.T) {
	for _, pkg := range []string{"api/model", "api/linediff"} {
		for _, imp := range imports(t, pkg) {
			if strings.HasPrefix(imp, repo+"/") {
				t.Errorf("%s should be a leaf but imports %s", pkg, imp)
			}
		}
	}
	for _, pkg := range []string{"hub/workspace", "api/hubapi"} {
		for _, imp := range imports(t, pkg) {
			if strings.HasPrefix(imp, repo+"/") && imp != repo+"/api/model" {
				t.Errorf("%s may import only model, imports %s", pkg, imp)
			}
		}
	}
}
