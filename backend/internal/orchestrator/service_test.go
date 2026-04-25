package orchestrator

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"agentpilot/backend/internal/domain"
	"agentpilot/backend/internal/planner"
	"agentpilot/backend/internal/statehub"
	"agentpilot/backend/internal/store"
	"agentpilot/backend/internal/tools"
)

func TestCreateTaskCompletesPipeline(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	taskStore, err := store.NewSQLiteStore(db)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	service := New(
		taskStore,
		statehub.NewHub(),
		planner.NewService(),
		tools.NewRunner(tools.Config{}),
	)

	task, err := service.CreateTask(context.Background(), CreateTaskInput{
		Title:       "测试汇报",
		Instruction: "生成一份文档和演示稿",
		Source:      "desktop",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	waitFor(t, 3*time.Second, func() bool {
		latest, err := taskStore.Get(context.Background(), task.TaskID)
		if err != nil {
			return false
		}
		return latest.Status == domain.StatusCompleted
	})

	latest, err := taskStore.Get(context.Background(), task.TaskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}

	if latest.DocURL == "" {
		t.Fatal("expected doc url to be set")
	}
	if latest.SlidesURL == "" {
		t.Fatal("expected slides url to be set")
	}
	if latest.Version < 4 {
		t.Fatalf("expected version to advance, got %d", latest.Version)
	}
}

func waitFor(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}
