package collab

import (
	"context"
	"database/sql"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"agentpilot/backend/internal/domain"
	"agentpilot/backend/internal/store"
)

func TestSnapshotKeepsUpdatesAfterBaseSeqAndContinuesSeq(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	taskStore, err := store.NewSQLiteStore(db)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	service, err := NewService(db, taskStore)
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	now := time.Now()
	_, err = taskStore.Create(ctx, domain.Task{
		TaskID:          "task-1",
		Title:           "Doc",
		UserInstruction: "write",
		Source:          "test",
		Status:          domain.StatusCompleted,
		CurrentStep:     "completed",
		ProgressText:    "done",
		Version:         1,
		LastActor:       "test",
		CreatedAt:       now,
		UpdatedAt:       now,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	doc, err := service.EnsureMarkdownDocument(ctx, "task-1")
	if err != nil {
		t.Fatalf("ensure doc: %v", err)
	}
	empty, err := service.UpdatesSince(ctx, doc.DocKey, 0)
	if err != nil {
		t.Fatalf("empty updates: %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("expected empty non-nil updates slice, got %#v", empty)
	}

	first, err := service.appendUpdate(ctx, doc.DocKey, "a", base64.StdEncoding.EncodeToString([]byte("u1")))
	if err != nil {
		t.Fatalf("append first: %v", err)
	}
	second, err := service.appendUpdate(ctx, doc.DocKey, "b", base64.StdEncoding.EncodeToString([]byte("u2")))
	if err != nil {
		t.Fatalf("append second: %v", err)
	}
	if first.Seq != 1 || second.Seq != 2 {
		t.Fatalf("unexpected seqs: %d %d", first.Seq, second.Seq)
	}

	_, err = service.SaveSnapshot(ctx, doc.DocKey, SnapshotRequest{
		BaseSeq:              1,
		SnapshotUpdateBase64: base64.StdEncoding.EncodeToString([]byte("snapshot-1")),
		MarkdownCache:        "# cache",
		ClientID:             "a",
	})
	if err != nil {
		t.Fatalf("save snapshot: %v", err)
	}
	remaining, err := service.UpdatesSince(ctx, doc.DocKey, 1)
	if err != nil {
		t.Fatalf("updates since: %v", err)
	}
	if len(remaining) != 1 || remaining[0].Seq != 2 {
		t.Fatalf("expected only seq 2 to remain, got %#v", remaining)
	}

	third, err := service.appendUpdate(ctx, doc.DocKey, "c", base64.StdEncoding.EncodeToString([]byte("u3")))
	if err != nil {
		t.Fatalf("append third: %v", err)
	}
	if third.Seq != 3 {
		t.Fatalf("expected seq 3 after snapshot, got %d", third.Seq)
	}
}

func TestEnsureMarkdownDocumentCanReadArtifactURL(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	taskStore, err := store.NewSQLiteStore(db)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	service, err := NewService(db, taskStore)
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	dir := t.TempDir()
	service.SetArtifactDir(dir)
	if err := os.WriteFile(filepath.Join(dir, "doc.md"), []byte("# Hello"), 0644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}

	now := time.Now()
	_, err = taskStore.Create(ctx, domain.Task{
		TaskID:          "task-url",
		Title:           "Doc",
		UserInstruction: "write",
		Source:          "test",
		Status:          domain.StatusCompleted,
		CurrentStep:     "completed",
		ProgressText:    "done",
		DocURL:          "/artifacts/doc.md",
		Version:         1,
		LastActor:       "test",
		CreatedAt:       now,
		UpdatedAt:       now,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	doc, err := service.EnsureMarkdownDocument(ctx, "task-url")
	if err != nil {
		t.Fatalf("ensure doc: %v", err)
	}
	if doc.MarkdownCache != "# Hello" {
		t.Fatalf("expected markdown from artifact URL, got %q", doc.MarkdownCache)
	}
	if doc.SourcePath != filepath.Join(dir, "doc.md") {
		t.Fatalf("unexpected source path: %q", doc.SourcePath)
	}
}

func TestOlderSnapshotIsRejected(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	taskStore, err := store.NewSQLiteStore(db)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	service, err := NewService(db, taskStore)
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	now := time.Now()
	_, err = taskStore.Create(ctx, domain.Task{
		TaskID:          "task-2",
		Title:           "Doc",
		UserInstruction: "write",
		Source:          "test",
		Status:          domain.StatusCompleted,
		CurrentStep:     "completed",
		ProgressText:    "done",
		Version:         1,
		LastActor:       "test",
		CreatedAt:       now,
		UpdatedAt:       now,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	doc, err := service.EnsureMarkdownDocument(ctx, "task-2")
	if err != nil {
		t.Fatalf("ensure doc: %v", err)
	}
	payload := base64.StdEncoding.EncodeToString([]byte("snapshot"))
	if _, err := service.SaveSnapshot(ctx, doc.DocKey, SnapshotRequest{BaseSeq: 5, SnapshotUpdateBase64: payload}); err != nil {
		t.Fatalf("save fresh snapshot: %v", err)
	}
	if _, err := service.SaveSnapshot(ctx, doc.DocKey, SnapshotRequest{BaseSeq: 4, SnapshotUpdateBase64: payload}); err == nil {
		t.Fatal("expected older snapshot to be rejected")
	}
}
