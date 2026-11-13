package workspace

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
	"time"

	"github.com/MahammadAgayev/neoharness/api/model"
)

func TestCreateListThreadsEvents(t *testing.T) {
	t.Setenv("NEOHARNESS_HOME", t.TempDir())

	if err := Create(Workspace{Name: "demo", Pin: "/tmp/x", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := Create(Workspace{Name: "demo"}); err == nil {
		t.Fatal("second create of the same name should fail")
	}
	if err := Create(Workspace{Name: "Bad Name"}); err == nil {
		t.Fatal("invalid name should fail")
	}
	ps, err := List()
	if err != nil || len(ps) != 1 || ps[0].Pin != "/tmp/x" {
		t.Fatalf("List = %+v, %v", ps, err)
	}

	th := &Thread{Name: "api-tests", Role: model.RoleThread, Token: "secret", CreatedAt: time.Now()}
	if err := SaveThread("demo", th); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(ThreadDir("demo", "api-tests"), "thread.json"))
	if err != nil || runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("thread.json must be 0600 (holds a token): %v %v", info.Mode(), err)
	}
	ths, err := Threads("demo")
	if err != nil || len(ths) != 1 || ths[0].Token != "secret" {
		t.Fatalf("Threads = %+v, %v", ths, err)
	}

	for i := range 5 {
		if err := AppendEvent("demo", "api-tests", model.Event{Kind: model.EventText, Text: string(rune('a' + i))}); err != nil {
			t.Fatal(err)
		}
	}
	// A crash mid-write leaves a torn last line; reading must survive it.
	f, _ := os.OpenFile(eventsFile("demo", "api-tests"), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(`{"kind":"te`)
	f.Close()

	evs, err := Events("demo", "api-tests", 2)
	if err != nil || len(evs) != 2 || evs[0].Text != "d" || evs[1].Text != "e" {
		t.Fatalf("Events tail = %+v, %v", evs, err)
	}
}

func TestNewIDIsUUIDv4(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if id := NewID(); !re.MatchString(id) {
		t.Fatalf("NewID = %q", id)
	}
}

func TestMigrateFromProjects(t *testing.T) {
	home := t.TempDir()
	t.Setenv("NEOHARNESS_HOME", home)
	old := filepath.Join(home, "projects")
	if err := os.MkdirAll(filepath.Join(old, "demo", "threads", "coordinator"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(old, "demo", "project.json"), []byte(`{"name":"demo","pin":"/src/demo"}`), 0o644)
	os.WriteFile(filepath.Join(old, "demo", "threads", "coordinator", "thread.json"), []byte(`{"name":"coordinator"}`), 0o600)

	if err := Migrate(); err != nil {
		t.Fatal(err)
	}
	ws, err := List()
	if err != nil || len(ws) != 1 || ws[0].Pin != "/src/demo" {
		t.Fatalf("List = %+v, %v", ws, err)
	}
	if ths, _ := Threads("demo"); len(ths) != 1 {
		t.Fatalf("threads = %+v", ths)
	}
	// The old path still reaches the data, for sessions that ran inside it.
	if _, err := os.Stat(filepath.Join(old, "demo", "workspace.json")); err != nil {
		t.Fatalf("old path: %v", err)
	}
	if err := Migrate(); err != nil {
		t.Fatalf("second run: %v", err)
	}
}
