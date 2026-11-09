// Package architecture asserts the import rules that keep this codebase
// testable, in the only way that survives contact with a future edit: as a
// failing test.
//
// The rules below are the reason the hub and TUI tests run in seconds without
// a model or a terminal. They are easy to break by accident — one convenient
// import is all it takes — and nothing else would notice.
package architecture

import (
	"go/build"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const module = "github.com/MahammadAgayev/neoharness"

// repoRoot locates the module root from this test file's own path, so the test
// does not depend on the working directory it is run from.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	// .../internal/architecture/architecture_test.go -> module root
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

// imports returns the non-test imports of one package.
func imports(t *testing.T, root, pkg string) []string {
	t.Helper()
	p, err := build.ImportDir(filepath.Join(root, pkg), 0)
	if err != nil {
		t.Fatalf("reading %s: %v", pkg, err)
	}
	return p.Imports
}

// TestAgentInterfaceKnowsNoAdapters keeps the boundary a boundary. If the
// interface package imported an adapter, the adapters could not import it, and
// the dependency would have to invert.
func TestAgentInterfaceKnowsNoAdapters(t *testing.T) {
	for _, imp := range imports(t, repoRoot(t), "internal/agent") {
		if strings.HasPrefix(imp, module+"/internal/agent/") {
			t.Errorf("internal/agent imports its own adapter %s", imp)
		}
		if strings.HasPrefix(imp, module+"/internal/") {
			t.Errorf("internal/agent should depend on nothing internal, imports %s", imp)
		}
	}
}

// TestPromptsDependsOnlyOnItself keeps prompt text free of hub state. Prompts
// take explicit inputs so that what an agent is told can be asserted without
// constructing a hub.
func TestPromptsDependsOnlyOnItself(t *testing.T) {
	for _, imp := range imports(t, repoRoot(t), "internal/prompts") {
		if strings.HasPrefix(imp, module+"/internal/") {
			t.Errorf("internal/prompts imports %s — prompts should take plain inputs", imp)
		}
	}
}

// TestHubKnowsNoTransport keeps the hub testable with a fake agent: it must
// not know which CLI runs a turn, how MCP or the local API are served, how
// remote hosts are reached, or that a TUI exists.
func TestHubKnowsNoTransport(t *testing.T) {
	forbidden := []string{
		module + "/internal/agent/claude",
		module + "/internal/tui",
		module + "/internal/mcpserver",
		module + "/internal/hubserver",
		module + "/internal/tunnel",
		"github.com/modelcontextprotocol/go-sdk",
	}
	for _, imp := range imports(t, repoRoot(t), "internal/hub") {
		for _, bad := range forbidden {
			if imp == bad || strings.HasPrefix(imp, bad+"/") {
				t.Errorf("internal/hub imports %s", imp)
			}
		}
	}
}

// TestTUIGoesThroughTheAPI keeps the TUI from reaching past the hub's API
// into state it does not own.
func TestTUIGoesThroughTheAPI(t *testing.T) {
	allowed := map[string]bool{
		module + "/internal/hubapi": true,
		module + "/internal/model":  true,
	}
	for _, imp := range imports(t, repoRoot(t), "internal/tui") {
		if strings.HasPrefix(imp, module+"/") && !allowed[imp] {
			t.Errorf("internal/tui imports %s — it may use only hubapi and model", imp)
		}
	}
}

// TestDataPackagesStayLeaves: model depends on nothing internal, and project
// only on model.
func TestDataPackagesStayLeaves(t *testing.T) {
	root := repoRoot(t)
	for _, imp := range imports(t, root, "internal/model") {
		if strings.HasPrefix(imp, module+"/") {
			t.Errorf("internal/model should be a leaf but imports %s", imp)
		}
	}
	for _, pkg := range []string{"internal/project", "internal/hubapi"} {
		for _, imp := range imports(t, root, pkg) {
			if strings.HasPrefix(imp, module+"/") && imp != module+"/internal/model" {
				t.Errorf("%s may import only model, imports %s", pkg, imp)
			}
		}
	}
}
