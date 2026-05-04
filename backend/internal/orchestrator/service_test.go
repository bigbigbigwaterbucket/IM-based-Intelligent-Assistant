package orchestrator

import (
	"context"
	"database/sql"
	"strings"
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
		planner.NewServiceWithLLM(nil),
		tools.NewRunner(tools.Config{ArtifactDir: t.TempDir()}),
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
		return latest.Status == domain.StatusWaitingAction
	})

	latest, err := taskStore.Get(context.Background(), task.TaskID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}

	if latest.DocURL == "" {
		t.Fatal("expected doc url to be set")
	}
	if latest.Status != domain.StatusWaitingAction {
		t.Fatalf("expected waiting action status, got %s", latest.Status)
	}
	if !latest.RequiresAction {
		t.Fatal("expected task to require action while awaiting feedback")
	}
	if latest.SlidesURL == "" {
		t.Fatal("expected slides url to be set")
	}
	if latest.DocURL == "https://placeholder.local" || latest.SlidesURL == "https://placeholder.local" {
		t.Fatal("expected real artifact urls, got placeholder urls")
	}
	if !strings.HasPrefix(latest.DocURL, "/artifacts/") {
		t.Fatalf("expected local doc artifact url, got %s", latest.DocURL)
	}
	if !strings.HasPrefix(latest.SlidesURL, "/artifacts/") {
		t.Fatalf("expected local slides artifact url, got %s", latest.SlidesURL)
	}
	if latest.Version < 4 {
		t.Fatalf("expected version to advance, got %d", latest.Version)
	}

	messages, err := taskStore.ListMessages(context.Background(), "task:"+task.TaskID, 10)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(messages) < 2 {
		t.Fatalf("expected persisted session messages, got %d", len(messages))
	}

	var invocationCount int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM tool_invocations WHERE task_id = ?`, task.TaskID).Scan(&invocationCount); err != nil {
		t.Fatalf("count tool invocations: %v", err)
	}
	if invocationCount == 0 {
		t.Fatal("expected persisted tool invocations")
	}
}

func TestGreetingTaskDoesNotCreateDocOrSlides(t *testing.T) {
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
		planner.NewServiceWithLLM(nil),
		tools.NewRunner(tools.Config{ArtifactDir: t.TempDir()}),
	)

	task, err := service.CreateTask(context.Background(), CreateTaskInput{
		Title:       "你好",
		Instruction: "你好",
		Source:      "feishu_p2p",
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
	if latest.DocURL != "" {
		t.Fatalf("did not expect doc url for greeting, got %s", latest.DocURL)
	}
	if latest.SlidesURL != "" {
		t.Fatalf("did not expect slides url for greeting, got %s", latest.SlidesURL)
	}
}

func TestWaitTaskDoneReturnsCompletedTask(t *testing.T) {
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
		planner.NewServiceWithLLM(nil),
		tools.NewRunner(tools.Config{ArtifactDir: t.TempDir()}),
	)

	now := time.Now()
	task := domain.Task{
		TaskID:          "wait-completed",
		Title:           "等待完成",
		UserInstruction: "测试",
		Source:          "test",
		Status:          domain.StatusExecuting,
		CurrentStep:     "executing",
		ProgressText:    "执行中",
		Version:         1,
		LastActor:       "test",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if _, err := taskStore.Create(context.Background(), task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	go func() {
		time.Sleep(50 * time.Millisecond)
		task.Status = domain.StatusCompleted
		task.CurrentStep = "completed"
		task.ProgressText = "完成"
		task.Version++
		task.UpdatedAt = time.Now()
		_, _ = taskStore.Update(context.Background(), task)
	}()

	done, err := service.WaitTaskDone(context.Background(), task.TaskID, time.Second, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("wait task done: %v", err)
	}
	if done.Status != domain.StatusCompleted {
		t.Fatalf("expected completed task, got %s", done.Status)
	}
}

func TestWaitTaskDoneTimesOut(t *testing.T) {
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
		planner.NewServiceWithLLM(nil),
		tools.NewRunner(tools.Config{ArtifactDir: t.TempDir()}),
	)

	now := time.Now()
	task := domain.Task{
		TaskID:          "wait-timeout",
		Title:           "等待超时",
		UserInstruction: "测试",
		Source:          "test",
		Status:          domain.StatusExecuting,
		CurrentStep:     "executing",
		ProgressText:    "执行中",
		Version:         1,
		LastActor:       "test",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if _, err := taskStore.Create(context.Background(), task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	if _, err := service.WaitTaskDone(context.Background(), task.TaskID, 50*time.Millisecond, 10*time.Millisecond); err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestSubmitEndTaskRequiresFeishuInitiator(t *testing.T) {
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
		planner.NewServiceWithLLM(nil),
		tools.NewRunner(tools.Config{ArtifactDir: t.TempDir()}),
	)

	now := time.Now()
	task := domain.Task{
		TaskID:          "waiting-feishu",
		Title:           "待结束任务",
		UserInstruction: "测试",
		Source:          "feishu_group",
		ChatID:          "oc_test",
		InitiatorOpenID: "ou_owner",
		Status:          domain.StatusWaitingAction,
		CurrentStep:     "awaiting_feedback",
		ProgressText:    "等待审核",
		RequiresAction:  true,
		Version:         1,
		LastActor:       "agent",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if _, err := taskStore.Create(context.Background(), task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	if _, err := service.SubmitAction(context.Background(), task.TaskID, ActionInput{
		ActionType:  string(domain.ActionEndTask),
		ActorType:   "feishu_card",
		ActorOpenID: "ou_other",
	}); err == nil {
		t.Fatal("expected non-initiator end task to be rejected")
	}

	ended, err := service.SubmitAction(context.Background(), task.TaskID, ActionInput{
		ActionType:  string(domain.ActionEndTask),
		ActorType:   "feishu_card",
		ActorOpenID: "ou_owner",
	})
	if err != nil {
		t.Fatalf("end task as initiator: %v", err)
	}
	if ended.Status != domain.StatusCompleted {
		t.Fatalf("expected completed task, got %s", ended.Status)
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
