// Package architecture asserts the import rules that keep this codebase
// testable, in the only way that survives contact with a future edit: as a
// failing test.
//
// The rules below are the reason the loop's 25 tests run in under a second
// without a model, a terminal, or tmux. They are easy to break by accident —
// one convenient import is all it takes — and nothing else would notice.
package architecture

import (
	"go/build"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const module = "github.com/MahammadAgayev/harness"

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

// TestLoopDependsOnNothingConcrete is the load-bearing one.
//
// The loop must not know which CLI runs a turn, that tmux exists, or how any
// of it is displayed. It takes an agent.Agent and an Observer and that is all.
// Break this and the loop's tests start needing a model, at which point they
// stop being run.
func TestLoopDependsOnNothingConcrete(t *testing.T) {
	root := repoRoot(t)
	forbidden := []string{
		module + "/internal/agent/pi",
		module + "/internal/agent/claude",
		module + "/internal/tmuxctl",
		module + "/internal/tui",
	}
	for _, imp := range imports(t, root, "internal/loop") {
		for _, bad := range forbidden {
			if imp == bad || strings.HasPrefix(imp, bad+"/") {
				t.Errorf("internal/loop imports %s — the loop must stay free of adapters, tmux, and UI", imp)
			}
		}
	}
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

// TestLeafPackagesStayLeaves guards the packages that everything else builds
// on. A cycle here is a refactor, not a fix, so it is worth catching early.
func TestLeafPackagesStayLeaves(t *testing.T) {
	root := repoRoot(t)
	for _, pkg := range []string{"internal/run", "internal/notes", "internal/tmuxctl"} {
		for _, imp := range imports(t, root, pkg) {
			if strings.HasPrefix(imp, module+"/internal/") {
				t.Errorf("%s should be a leaf but imports %s", pkg, imp)
			}
		}
	}
}

// TestPromptsDependsOnlyOnItself keeps prompt text free of run state. Prompts
// take explicit inputs so that what an agent is told can be asserted without
// constructing a run.
func TestPromptsDependsOnlyOnItself(t *testing.T) {
	for _, imp := range imports(t, repoRoot(t), "internal/prompts") {
		if strings.HasPrefix(imp, module+"/internal/") {
			t.Errorf("internal/prompts imports %s — prompts should take plain inputs", imp)
		}
	}
}
