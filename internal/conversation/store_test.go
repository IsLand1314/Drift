package conversation

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/IsLand1314/Drift/internal/layout"
	"github.com/IsLand1314/Drift/internal/llm"
	"github.com/IsLand1314/Drift/internal/tool"
)

func TestListIncludesLocalPreviewWithoutPersistingIt(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	snapshot, err := store.Create("")
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Messages = []llm.Message{{Role: "user", Content: "第一行\n第二行"}}
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	items, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Preview != "第一行 第二行" {
		t.Fatalf("items = %+v", items)
	}
	raw, err := os.ReadFile(filepath.Join(layout.DateDir(filepath.Join(root, ".drift", "sessions"), snapshot.CreatedAt), "session-"+layout.FileTimestamp(snapshot.CreatedAt)+"-"+snapshot.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(`"preview"`)) {
		t.Fatal("preview must not be persisted")
	}
}

func TestStorePreservesTaskState(t *testing.T) {
	store := NewStore(t.TempDir())
	snapshot, err := store.Create("")
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Tasks = []tool.TaskState{{ID: "task-1", Subject: "persist task", Description: "resume it", Status: "in_progress"}}
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(snapshot.ID)
	if err != nil || len(loaded.Tasks) != 1 || !reflect.DeepEqual(loaded.Tasks[0], snapshot.Tasks[0]) {
		t.Fatalf("loaded tasks=%+v err=%v", loaded.Tasks, err)
	}
}

func TestStoreSaveAndLoadPreservesToolMessages(t *testing.T) {
	store := NewStore(t.TempDir())
	snapshot, err := store.Create("README.md")
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Messages = []llm.Message{
		{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "call-1", Type: "function", Name: "ReadFile", Arguments: `{"path":"README.md"}`}}},
		{Role: "tool", ToolCallID: "call-1", Content: "# Drift"},
	}
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(snapshot.ID)
	if err != nil || loaded.Messages[1].Content != "# Drift" {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
}

func TestStoreDoesNotPersistReasoningContent(t *testing.T) {
	store := NewStore(t.TempDir())
	snapshot, err := store.Create("")
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Messages = []llm.Message{{Role: "assistant", Content: "answer", ReasoningContent: "private reasoning"}}
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(snapshot.ID)
	if err != nil || loaded.Messages[0].ReasoningContent != "" {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
}

func TestStorePreservesUsageCountersAndOldSnapshotsDefaultToZero(t *testing.T) {
	store := NewStore(t.TempDir())
	snapshot, err := store.Create("")
	if err != nil {
		t.Fatal(err)
	}
	snapshot.InputTokens, snapshot.OutputTokens = 17, 9
	snapshot.ReportedRequests, snapshot.UnreportedRequests = 2, 1
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.InputTokens != 17 || loaded.OutputTokens != 9 || loaded.ReportedRequests != 2 || loaded.UnreportedRequests != 1 {
		t.Fatalf("usage counters=%+v", loaded)
	}
}

func TestStoreRejectsInvalidIDs(t *testing.T) {
	store := NewStore(t.TempDir())
	for _, id := range []string{"", "../x", "x/y"} {
		if _, err := store.Load(id); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Load(%q) error = %v, want ErrInvalidID", id, err)
		}
	}
}

func TestStoreRejectsMalformedJSON(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	if err := os.MkdirAll(layout.DateDir(filepath.Join(root, ".drift", "sessions"), time.Now()), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(layout.DateDir(filepath.Join(root, ".drift", "sessions"), time.Now()), "session-2026-10-01T00-00-00Z-conv-invalid1.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load("conv-invalid1"); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("Load malformed error = %v, want ErrInvalidSnapshot", err)
	}
}

func TestStoreDoesNotReadLegacyConversationPath(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	legacyDir := filepath.Join(root, ".drift", "conversations")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(legacyDir, "conv-legacy12345678.json")
	if err := os.WriteFile(legacyPath, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load("conv-legacy12345678"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("legacy load error=%v, want ErrNotFound", err)
	}
}

func TestStoreLatestOrdersByUpdatedAt(t *testing.T) {
	store := NewStore(t.TempDir())
	first, err := store.Create("")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Create("")
	if err != nil {
		t.Fatal(err)
	}
	first.UpdatedAt = time.Now().Add(time.Minute)
	if err := store.Save(first); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(second); err != nil {
		t.Fatal(err)
	}
	latest, err := store.Latest()
	if err != nil || latest.ID != first.ID {
		t.Fatalf("latest=%+v err=%v, want %s", latest, err, first.ID)
	}
}

func TestStoreListMetadataDoesNotContainMessageBody(t *testing.T) {
	store := NewStore(t.TempDir())
	snapshot, err := store.Create("")
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Messages = []llm.Message{{Role: "user", Content: "secret prompt"}}
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	metadata, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata) != 1 || metadata[0].MessageCount != 1 {
		t.Fatalf("metadata=%+v", metadata)
	}
	if strings.Contains(strings.Join([]string{metadata[0].ID, metadata[0].Focus}, " "), "secret prompt") {
		t.Fatal("metadata leaked message body")
	}
}

func TestStoreSearchReturnsBoundedMatchLocationsWithoutBodies(t *testing.T) {
	store := NewStore(t.TempDir())
	snapshot, err := store.Create("")
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Messages = []llm.Message{
		{Role: "user", Content: "inspect alpha"},
		{Role: "assistant", Content: "alpha found"},
		{Role: "tool", Content: "secret alpha output"},
	}
	if err := store.Save(snapshot); err != nil {
		t.Fatal(err)
	}
	hits, err := store.Search("ALPHA", 10)
	if err != nil || len(hits) != 1 {
		t.Fatalf("hits=%+v err=%v", hits, err)
	}
	if hits[0].ID != snapshot.ID || len(hits[0].Matches) != 3 || hits[0].Matches[0].Role != "user" {
		t.Fatalf("hit=%+v", hits[0])
	}
	if strings.Contains(fmt.Sprint(hits[0]), "secret alpha output") {
		t.Fatal("search result leaked message body")
	}
}

func TestStoreDeleteRemovesExactSnapshot(t *testing.T) {
	store := NewStore(t.TempDir())
	snapshot, err := store.Create("")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(snapshot.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(snapshot.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load deleted error = %v, want ErrNotFound", err)
	}
}

func TestStoreRenameAndLoadTitle(t *testing.T) {
	store := NewStore(t.TempDir())
	snapshot, err := store.Create("")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Rename(snapshot.ID, "FoxCode 分析"); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(snapshot.ID)
	if err != nil || loaded.Title != "FoxCode 分析" {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
}

func TestStoreRejectsInvalidTitle(t *testing.T) {
	store := NewStore(t.TempDir())
	snapshot, err := store.Create("")
	if err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"line\nbreak", strings.Repeat("x", 121)} {
		if err := store.Rename(snapshot.ID, title); !errors.Is(err, ErrInvalidSnapshot) {
			t.Errorf("Rename(%q) error=%v, want ErrInvalidSnapshot", title, err)
		}
	}
}

func TestStoreBeforeAndPruneOnlyRemoveExpired(t *testing.T) {
	store := NewStore(t.TempDir())
	oldSnapshot, err := store.Create("")
	if err != nil {
		t.Fatal(err)
	}
	newSnapshot, err := store.Create("")
	if err != nil {
		t.Fatal(err)
	}
	threshold := time.Now().UTC()
	oldSnapshot.UpdatedAt = threshold.Add(-time.Hour)
	if err := store.Save(oldSnapshot); err != nil {
		t.Fatal(err)
	}
	newSnapshot.UpdatedAt = threshold.Add(time.Hour)
	if err := store.Save(newSnapshot); err != nil {
		t.Fatal(err)
	}
	candidates, err := store.Before(threshold)
	if err != nil || len(candidates) != 1 || candidates[0].ID != oldSnapshot.ID {
		t.Fatalf("candidates=%+v err=%v", candidates, err)
	}
	removed, err := store.Prune(threshold)
	if err != nil || len(removed) != 1 || removed[0].ID != oldSnapshot.ID {
		t.Fatalf("removed=%+v err=%v", removed, err)
	}
	if _, err := store.Load(newSnapshot.ID); err != nil {
		t.Fatalf("new snapshot removed: %v", err)
	}
}
