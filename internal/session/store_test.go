package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maguroid/llmx/internal/chat"
)

func fixedNow() time.Time {
	return time.Date(2026, 6, 25, 12, 34, 56, 0, time.UTC)
}

func TestStoreSaveAtomicPermissionsAndLast(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root, fixedNow)
	loaded, err := store.OpenNamed("work", "p", "m", false, ptr("sys"))
	if err != nil {
		t.Fatal(err)
	}
	messages := append(loaded.Session.Messages,
		chat.Message{Role: chat.RoleUser, Content: "hi"},
		chat.Message{Role: chat.RoleAssistant, Content: "ok"},
	)
	if err := store.Save(loaded, messages); err != nil {
		t.Fatal(err)
	}
	assertPerm(t, filepath.Join(root, "sessions"), 0o700)
	assertPerm(t, filepath.Join(root, "sessions", "work.json"), 0o600)
	assertPerm(t, filepath.Join(root, "sessions", "last"), 0o600)
	last, err := os.ReadFile(filepath.Join(root, "sessions", "last"))
	if err != nil {
		t.Fatal(err)
	}
	if string(last) != "work\n" {
		t.Fatalf("last = %q", last)
	}
	entries, err := os.ReadDir(filepath.Join(root, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".tmp-") {
			t.Fatalf("left temp file %s", entry.Name())
		}
	}
	var saved Session
	data, err := os.ReadFile(filepath.Join(root, "sessions", "work.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.SchemaVersion != 1 || len(saved.Messages) != 3 {
		t.Fatalf("bad saved session: %+v", saved)
	}
}

func TestStoreStatePathsAndDanglingLast(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root, fixedNow)
	loaded, err := store.Start("p", "m", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(loaded, []chat.Message{{Role: chat.RoleUser, Content: "u"}, {Role: chat.RoleAssistant, Content: "a"}}); err != nil {
		t.Fatal(err)
	}
	continued, dangling, err := store.ContinueLast("p", "m", nil)
	if err != nil {
		t.Fatal(err)
	}
	if dangling || continued.ID != loaded.ID {
		t.Fatalf("continued id=%s dangling=%v want %s false", continued.ID, dangling, loaded.ID)
	}
	if err := os.Remove(filepath.Join(root, "sessions", loaded.ID+".json")); err != nil {
		t.Fatal(err)
	}
	newLoaded, dangling, err := store.ContinueLast("p", "m", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !dangling || newLoaded.ID == loaded.ID {
		t.Fatalf("dangling=%v new=%s old=%s", dangling, newLoaded.ID, loaded.ID)
	}
	named, err := store.OpenNamed("named", "p", "m", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if named.ID != "named" || named.Existed {
		t.Fatalf("bad named start: %+v", named)
	}
	if err := store.Save(named, []chat.Message{{Role: chat.RoleUser, Content: "u"}, {Role: chat.RoleAssistant, Content: "a"}}); err != nil {
		t.Fatal(err)
	}
	reset, err := store.OpenNamed("named", "p2", "m2", true, ptr("new sys"))
	if err != nil {
		t.Fatal(err)
	}
	if reset.Existed || len(reset.Session.Messages) != 1 || reset.Session.Messages[0].Content != "new sys" {
		t.Fatalf("reset did not create fresh session: %+v", reset)
	}
}

func assertPerm(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s perm = %o, want %o", path, got, want)
	}
}

func ptr(s string) *string {
	return &s
}
