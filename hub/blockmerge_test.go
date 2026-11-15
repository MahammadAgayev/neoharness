package hub

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MahammadAgayev/neoharness/api/model"
)

func TestBlocksKeepCodeListsAndTablesWhole(t *testing.T) {
	text := "\n# Title\n\nA paragraph\non two lines.\n\n```go\nfunc a() {\n\n}\n```\n\n- one\n\n- two\n  more\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\nlast"
	got := blocks(text)
	want := []string{
		"\n",
		"# Title\n\n",
		"A paragraph\non two lines.\n\n",
		"```go\nfunc a() {\n\n}\n```\n\n",
		"- one\n\n- two\n  more\n\n",
		"| a | b |\n|---|---|\n| 1 | 2 |\n\n",
		"last",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("blocks =\n%q\nwant\n%q", got, want)
	}
	if strings.Join(got, "") != text {
		t.Fatal("blocks lose bytes")
	}
}

// TestBlockMergeKeepsUntouchedBlocksByteForByte runs on the docs of this
// repository: what neither side touched comes out as it went in.
func TestBlockMergeKeepsUntouchedBlocksByteForByte(t *testing.T) {
	docs, _ := filepath.Glob("../docs/*.md")
	if len(docs) == 0 {
		t.Fatal("no docs to merge")
	}
	for _, p := range docs {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		doc := string(raw)
		if m := mergeBlocks(doc, doc, doc); !m.Clean || m.Merged != doc {
			t.Fatalf("%s merged with itself changed", p)
		}
		bs := blocks(doc)
		if len(bs) < 4 {
			continue
		}
		edit := func(k int, word string) string {
			out := slices.Clone(bs)
			out[k] = strings.Replace(out[k], "\n", " "+word+"\n", 1)
			return strings.Join(out, "")
		}
		mine, theirs := edit(1, "mine"), edit(len(bs)-2, "theirs")
		want := slices.Clone(bs)
		want[1] = strings.Replace(want[1], "\n", " mine\n", 1)
		want[len(bs)-2] = strings.Replace(want[len(bs)-2], "\n", " theirs\n", 1)
		if m := mergeBlocks(doc, mine, doc); !m.Clean || m.Merged != mine {
			t.Fatalf("%s: one side's edit did not come out as it was", p)
		}
		if m := mergeBlocks(doc, mine, theirs); !m.Clean || m.Merged != strings.Join(want, "") {
			t.Fatalf("%s: edits of two blocks did not both stay, byte for byte", p)
		}
	}
}

func TestBlockMergeReportsWholeBlocksWithLineRanges(t *testing.T) {
	base := "# T\n\npara one\nline two\n\npara three\n"
	// Different lines of one block still merge, as the line merge did.
	if m := mergeBlocks(base, "# T\n\nPARA ONE\nline two\n\npara three\n", "# T\n\npara one\nline 2\n\npara three\n"); !m.Clean ||
		m.Merged != "# T\n\nPARA ONE\nline 2\n\npara three\n" {
		t.Fatalf("merge = %+v", m)
	}
	m := mergeBlocks(base, "# T\n\npara one\nLINE TWO\n\npara three\n", "# T\n\npara one\nline 2\n\npara three\n")
	if m.Clean || len(m.Conflicts) != 1 {
		t.Fatalf("merge = %+v, want one conflict", m)
	}
	// The person's side stays in the merged text until they take the other.
	if m.Merged != "# T\n\npara one\nLINE TWO\n\npara three\n" {
		t.Fatalf("merged = %q", m.Merged)
	}
	want := model.Conflict{Base: [2]int{2, 4}, Ours: [2]int{2, 4}, Theirs: [2]int{2, 4}, Merged: [2]int{2, 4}}
	if m.Conflicts[0] != want {
		t.Fatalf("conflict = %+v, want %+v", m.Conflicts[0], want)
	}
	// A block added at the end moves the blank line after the last block;
	// an edit of that block on the other side still merges.
	if m := mergeBlocks(base, base+"\nadded\n", "# T\n\npara one\nline two\n\npara 3\n"); !m.Clean ||
		m.Merged != "# T\n\npara one\nline two\n\npara 3\n\nadded\n" {
		t.Fatalf("merge = %+v", m)
	}
	// An insertion on one side and a deletion of the next block on the
	// other both stay.
	if m := mergeBlocks(base, "# T\n\nnew\n\npara one\nline two\n\npara three\n", "# T\n\npara one\nline two\n\n"); !m.Clean ||
		m.Merged != "# T\n\nnew\n\npara one\nline two\n\n" {
		t.Fatalf("merge = %+v", m)
	}
}

// TestMergeArtifactIsADryRun: the merge an edit would get, without
// writing it, so the app can show the newer version inside an edit.
func TestMergeArtifactIsADryRun(t *testing.T) {
	h := newHub(t, &fake{})
	pinned(t, h, "demo")
	v1 := "# Plan\n\nstep one\n\nstep two\n"
	if _, err := h.WriteArtifact("demo", model.Coordinator, "plan", v1); err != nil {
		t.Fatal(err)
	}
	if m, err := h.MergeArtifact("demo", "plan", v1+"\nstep three\n", v1, 1); err != nil || !m.Clean || m.Version != 1 || m.Merged != v1+"\nstep three\n" {
		t.Fatalf("merge on the same version = %+v, %v", m, err)
	}
	v2 := "# Plan\n\nstep ONE\n\nstep two\n"
	if _, err := h.WriteArtifact("demo", model.Coordinator, "plan", v2); err != nil {
		t.Fatal(err)
	}
	m, err := h.MergeArtifact("demo", "plan", "# Plan\n\nstep one\n\nstep TWO\n", v1, 1)
	if err != nil || !m.Clean || m.Version != 2 || m.Merged != "# Plan\n\nstep ONE\n\nstep TWO\n" {
		t.Fatalf("merge = %+v, %v", m, err)
	}
	m, _ = h.MergeArtifact("demo", "plan", "# Plan\n\nstep 1\n\nstep two\n", v1, 1)
	if m.Clean || len(m.Conflicts) != 1 || m.Merged != "# Plan\n\nstep 1\n\nstep two\n" {
		t.Fatalf("conflict merge = %+v", m)
	}
	if _, text, _ := h.ReadArtifact("demo", "plan"); text != v2 {
		t.Fatalf("a dry run wrote %q", text)
	}
	if _, err := h.MergeArtifact("demo", "nothing", "x", "", 1); err == nil {
		t.Fatal("merge of an artifact that does not exist")
	}
	// A save merges the same way.
	if _, text, err := h.EditArtifact("demo", "plan", "# Plan\n\nstep one\n\nstep TWO\n", v1, 1, false); err != nil || text != "# Plan\n\nstep ONE\n\nstep TWO\n" {
		t.Fatalf("edit = %q, %v", text, err)
	}
}
