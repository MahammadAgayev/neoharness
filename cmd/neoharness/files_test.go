package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MahammadAgayev/neoharness/internal/model"
)

func TestListFilesUsesGitElseWalks(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"a b.go", "sub/c.go", ".hidden/d.go", "skip.log", "__pycache__/c.pyc", "venv/bin/python"} {
		p := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, _, err := listFiles(context.Background(), model.Location{Dir: dir}, "")
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	if want := []string{"a b.go", "skip.log", "sub/c.go"}; !slices.Equal(got, want) {
		t.Errorf("walk = %q, want %q", got, want)
	}

	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.log\n.hidden/\n__pycache__/\nvenv/\n"), 0o644)
	if err := exec.Command("git", "-C", dir, "init", "-q").Run(); err != nil {
		t.Skip("no git")
	}
	got, _, _ = listFiles(context.Background(), model.Location{Dir: dir}, "")
	slices.Sort(got)
	if want := []string{".gitignore", "a b.go", "sub/c.go"}; !slices.Equal(got, want) {
		t.Errorf("git = %q, want %q", got, want)
	}

	// A repository that tracks the limit or more says so, and is searched.
	exec.Command("git", "-C", dir, "add", ".").Run()
	if out, _ := runIn(context.Background(), model.Location{Dir: dir}, listScript, "2"); out != ":many\n" {
		t.Errorf("over the limit: %q, want :many", out)
	}
	got, many, err := listFiles(context.Background(), model.Location{Dir: dir}, "C.GO")
	if err != nil || many || !slices.Equal(got, []string{"sub/c.go"}) {
		t.Errorf("search = %q %v %v, want sub/c.go in any case", got, many, err)
	}
}

func TestGitBranch(t *testing.T) {
	dir := t.TempDir()
	if b, err := gitBranch(context.Background(), model.Location{Dir: dir}); err != nil || b != "" {
		t.Fatalf("outside a repository: %q, %v", b, err)
	}
	if err := exec.Command("git", "-C", dir, "init", "-q", "-b", "trunk").Run(); err != nil {
		t.Skip("no git")
	}
	if b, _ := gitBranch(context.Background(), model.Location{Dir: dir}); b != "trunk" {
		t.Fatalf("branch = %q, want trunk", b)
	}
}
