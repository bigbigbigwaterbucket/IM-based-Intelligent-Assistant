package larkbot

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"agentpilot/backend/internal/domain"
	"agentpilot/backend/internal/orchestrator"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestHandleMessageStartsP2PTaskAndRepliesDone(t *testing.T) {
	t.Parallel()

	launcher := &fakeLauncher{
		createdTask: domain.Task{
			TaskID: "task-1",
			Status: domain.StatusExecuting,
		},
		doneTask: domain.Task{
			TaskID:    "task-1",
			Status:    domain.StatusCompleted,
			Summary:   "已完成摘要",
			DocURL:    "https://doc.example",
			SlidesURL: "https://slides.example",
		},
	}
	messenger := &fakeMessenger{}
	handler := NewHandler(launcher, messenger, "https://dashboard.example")
	handler.doneTimeout = time.Second
	handler.doneInterval = time.Millisecond

	err := handler.HandleMessage(context.Background(), receiveEvent("p2p", "text", `{"text":"/assistant 生成方案"}`, "user"))
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}

	waitForReplies(t, messenger, 2)
	if launcher.createCalls != 1 {
		t.Fatalf("expected one create call, got %d", launcher.createCalls)
	}
	if launcher.lastInput.Source != "feishu_p2p" {
		t.Fatalf("unexpected source: %s", launcher.lastInput.Source)
	}
	if launcher.lastInput.ChatID != "oc_test" {
		t.Fatalf("unexpected chat id: %s", launcher.lastInput.ChatID)
	}
	if launcher.lastInput.MessageID != "om_test" {
		t.Fatalf("unexpected message id: %s", launcher.lastInput.MessageID)
	}
	replies := messenger.replies()
	if !strings.Contains(strings.Join(replies, "\n"), "实时进度：https://dashboard.example/?taskId=task-1") {
		t.Fatalf("expected dashboard link in replies: %#v", replies)
	}
	if !strings.Contains(strings.Join(replies, "\n"), "Assistant 任务完成：task-1") {
		t.Fatalf("expected completion reply: %#v", replies)
	}
}

func TestHandleMessageStartsGroupTask(t *testing.T) {
	t.Parallel()

	launcher := &fakeLauncher{
		createdTask: domain.Task{TaskID: "task-group", Status: domain.StatusExecuting},
		doneTask:    domain.Task{TaskID: "task-group", Status: domain.StatusCompleted},
	}
	messenger := &fakeMessenger{}
	handler := NewHandler(launcher, messenger, "")
	handler.doneTimeout = time.Second
	handler.doneInterval = time.Millisecond

	err := handler.HandleMessage(context.Background(), receiveEvent("group", "text", `{"text":"/assistant 总结群聊"}`, "user"))
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}

	waitForReplies(t, messenger, 2)
	if launcher.lastInput.Source != "feishu_group" {
		t.Fatalf("unexpected source: %s", launcher.lastInput.Source)
	}
}

func TestHandleMessageHelpDoesNotCreateTask(t *testing.T) {
	t.Parallel()

	launcher := &fakeLauncher{}
	messenger := &fakeMessenger{}
	handler := NewHandler(launcher, messenger, "")

	err := handler.HandleMessage(context.Background(), receiveEvent("p2p", "text", `{"text":"/assistant"}`, "user"))
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}

	if launcher.createCalls != 0 {
		t.Fatalf("did not expect create call, got %d", launcher.createCalls)
	}
	replies := messenger.replies()
	if len(replies) != 1 || !strings.Contains(replies[0], "用法：/assistant") {
		t.Fatalf("unexpected replies: %#v", replies)
	}
}

func TestHandleMessageIgnoresInvalidEvents(t *testing.T) {
	t.Parallel()

	launcher := &fakeLauncher{}
	messenger := &fakeMessenger{}
	handler := NewHandler(launcher, messenger, "")

	cases := []*larkim.P2MessageReceiveV1{
		receiveEvent("p2p", "text", `{"text":"/pilot 旧入口"}`, "user"),
		receiveEvent("p2p", "image", `{"text":"/assistant 生成方案"}`, "user"),
		receiveEvent("p2p", "text", `{"text":"/assistant 生成方案"}`, "app"),
	}
	for _, event := range cases {
		if err := handler.HandleMessage(context.Background(), event); err != nil {
			t.Fatalf("handle message: %v", err)
		}
	}

	if launcher.createCalls != 0 {
		t.Fatalf("did not expect create call, got %d", launcher.createCalls)
	}
	if got := len(messenger.replies()); got != 0 {
		t.Fatalf("did not expect replies, got %d", got)
	}
}

type fakeLauncher struct {
	mu          sync.Mutex
	createdTask domain.Task
	doneTask    domain.Task
	lastInput   orchestrator.CreateTaskInput
	createCalls int
	waitCalls   int
	createErr   error
}

func (f *fakeLauncher) CreateTask(_ context.Context, input orchestrator.CreateTaskInput) (domain.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls++
	f.lastInput = input
	return f.createdTask, f.createErr
}

func (f *fakeLauncher) WaitTaskDone(_ context.Context, _ string, _, _ time.Duration) (domain.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.waitCalls++
	return f.doneTask, nil
}

type fakeMessenger struct {
	mu       sync.Mutex
	messages []string
}

func (f *fakeMessenger) ReplyText(_ context.Context, _ string, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, text)
	return nil
}

func (f *fakeMessenger) SendText(_ context.Context, _, _, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, text)
	return nil
}

func (f *fakeMessenger) replies() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.messages))
	copy(out, f.messages)
	return out
}

func receiveEvent(chatType, messageType, content, senderType string) *larkim.P2MessageReceiveV1 {
	messageID := "om_test"
	chatID := "oc_test"
	threadID := "omt_test"
	return &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Sender: &larkim.EventSender{SenderType: &senderType},
			Message: &larkim.EventMessage{
				MessageId:   &messageID,
				ChatId:      &chatID,
				ThreadId:    &threadID,
				ChatType:    &chatType,
				MessageType: &messageType,
				Content:     &content,
			},
		},
	}
}

func waitForReplies(t *testing.T, messenger *fakeMessenger, count int) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(messenger.replies()) >= count {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("expected at least %d replies, got %d", count, len(messenger.replies()))
}
