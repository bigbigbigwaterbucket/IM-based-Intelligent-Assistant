package larkbot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"agentpilot/backend/internal/domain"
	"agentpilot/backend/internal/orchestrator"
	"agentpilot/backend/internal/proactive"

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestHandleMessageStartsP2PTaskAndRepliesDone(t *testing.T) {
	t.Parallel()

	launcher := &fakeLauncher{
		createdTask: domain.Task{TaskID: "task-1", Status: domain.StatusExecuting},
		doneTask: domain.Task{
			TaskID:    "task-1",
			Status:    domain.StatusWaitingAction,
			Summary:   "done summary",
			DocURL:    "https://doc.example",
			SlidesURL: "https://slides.example",
		},
		continueErr: errors.New("no active task"),
	}
	messenger := &fakeMessenger{}
	handler := NewHandler(launcher, messenger, "https://dashboard.example")
	handler.doneTimeout = time.Second
	handler.doneInterval = time.Millisecond

	err := handler.HandleMessage(context.Background(), receiveEvent("p2p", "text", `{"text":"/assistant generate plan"}`, "user"))
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
	if launcher.lastInput.InitiatorOpenID != "ou_sender" {
		t.Fatalf("unexpected initiator open id: %s", launcher.lastInput.InitiatorOpenID)
	}
	replies := strings.Join(messenger.replies(), "\n")
	if !strings.Contains(replies, "https://dashboard.example/?taskId=task-1") {
		t.Fatalf("expected dashboard link in replies: %s", replies)
	}
	if !strings.Contains(replies, "Assistant任务待审核：task-1") {
		t.Fatalf("expected review reply: %s", replies)
	}
}

func TestNotifyWhenDoneSendsLocalSlidesAsPPTFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	mdPath := filepath.Join(dir, "slide_test.md")
	pptxPath := filepath.Join(dir, "slide_test.pptx")
	if err := os.WriteFile(mdPath, []byte("# Slides"), 0644); err != nil {
		t.Fatalf("write md: %v", err)
	}
	if err := os.WriteFile(pptxPath, []byte("pptx"), 0644); err != nil {
		t.Fatalf("write pptx: %v", err)
	}

	launcher := &fakeLauncher{
		doneTask: domain.Task{
			TaskID:             "task-ppt",
			Status:             domain.StatusWaitingAction,
			SlidesURL:          "/artifacts/slide_test.pptx",
			SlidesArtifactPath: mdPath,
		},
	}
	messenger := &fakeMessenger{}
	handler := NewHandler(launcher, messenger, "")
	handler.doneTimeout = time.Second
	handler.doneInterval = time.Millisecond

	handler.notifyWhenDone("om_test", "task-ppt")

	replies := strings.Join(messenger.replies(), "\n")
	if strings.Contains(replies, "/artifacts/slide_test.pptx") {
		t.Fatalf("did not expect local artifact link in text reply: %s", replies)
	}
	if !strings.Contains(replies, "幻灯片：见下方 PPT 文件") {
		t.Fatalf("expected PPT file hint in text reply: %s", replies)
	}
	files := messenger.files()
	if len(files) != 1 || files[0] != pptxPath {
		t.Fatalf("expected PPT file reply %q, got %#v", pptxPath, files)
	}
}

func TestHandleMessageStartsGroupTask(t *testing.T) {
	t.Parallel()

	launcher := &fakeLauncher{
		createdTask: domain.Task{TaskID: "task-group", Status: domain.StatusExecuting},
		doneTask:    domain.Task{TaskID: "task-group", Status: domain.StatusWaitingAction},
		continueErr: errors.New("no active task"),
	}
	messenger := &fakeMessenger{}
	handler := NewHandler(launcher, messenger, "")
	handler.doneTimeout = time.Second
	handler.doneInterval = time.Millisecond

	err := handler.HandleMessage(context.Background(), receiveEvent("group", "text", `{"text":"/assistant summarize group"}`, "user"))
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}

	waitForReplies(t, messenger, 2)
	if launcher.lastInput.Source != "feishu_group" {
		t.Fatalf("unexpected source: %s", launcher.lastInput.Source)
	}
}

func TestHandleCardActionPassesOperatorIdentity(t *testing.T) {
	t.Parallel()

	launcher := &fakeLauncher{}
	messenger := &fakeMessenger{}
	handler := NewHandler(launcher, messenger, "")

	resp, err := handler.HandleCardAction(context.Background(), cardActionEvent("task-card", "ou_sender", "u_sender"))
	if err != nil {
		t.Fatalf("handle card action: %v", err)
	}
	if resp == nil || resp.Toast == nil || resp.Toast.Type != "success" {
		t.Fatalf("unexpected card response: %#v", resp)
	}
	if launcher.submitTaskID != "task-card" {
		t.Fatalf("unexpected submitted task id: %s", launcher.submitTaskID)
	}
	if launcher.submitInput.ActorOpenID != "ou_sender" || launcher.submitInput.ActorUserID != "u_sender" {
		t.Fatalf("unexpected actor identity: %#v", launcher.submitInput)
	}
}

func TestHandleMessageSendsProactiveCandidateCard(t *testing.T) {
	t.Parallel()

	launcher := &fakeLauncher{
		proactiveCandidate: domain.ProactiveCandidate{
			CandidateID: "cand-1",
			ChatID:      "oc_test",
			Title:       "项目复盘汇报",
			ThemeKey:    "项目复盘汇报",
			Status:      domain.CandidatePending,
		},
	}
	messenger := &fakeMessenger{}
	handler := NewHandler(launcher, messenger, "")
	handler.SetProactiveDetector(proactive.Config{Enabled: true, CacheLimit: 30, RuleThreshold: 0.4, LLMConfidence: 0.55, Cooldown: time.Hour}, proactive.NewDetector(
		proactive.Config{RuleThreshold: 0.4, LLMConfidence: 0.55},
		fakeProactiveJudge{judgement: proactive.Judgement{
			IsTask:     true,
			Title:      "项目复盘汇报",
			Goal:       "整理项目复盘并生成汇报材料",
			TaskType:   "ppt",
			ThemeKey:   "项目复盘汇报",
			Confidence: 0.8,
			Reason:     "明确要求整理和汇报",
		}},
	))

	if err := handler.HandleMessage(context.Background(), receiveEvent("group", "text", `{"text":"下周要给老板做个复盘汇报，资料 https://example.com/doc"}`, "user")); err != nil {
		t.Fatalf("handle message: %v", err)
	}

	replies := strings.Join(messenger.replies(), "\n")
	if !strings.Contains(replies, "proactive_task_confirm") {
		t.Fatalf("expected proactive card, got %s", replies)
	}
	if launcher.proactiveInput.Title != "项目复盘汇报" {
		t.Fatalf("unexpected proactive input: %#v", launcher.proactiveInput)
	}
}

func TestHandleMessageIgnoresBotMessageBeforeProactiveDetection(t *testing.T) {
	t.Parallel()

	launcher := &fakeLauncher{}
	messenger := &fakeMessenger{}
	handler := NewHandler(launcher, messenger, "")
	handler.SetBotIdentity("cli_bot")
	handler.SetProactiveDetector(proactive.Config{Enabled: true, CacheLimit: 30, RuleThreshold: 0.4, LLMConfidence: 0.55, Cooldown: time.Hour}, proactive.NewDetector(
		proactive.Config{RuleThreshold: 0.4, LLMConfidence: 0.55},
		fakeProactiveJudge{judgement: proactive.Judgement{
			IsTask:     true,
			Title:      "项目复盘汇报",
			Goal:       "整理项目复盘并生成汇报材料",
			TaskType:   "ppt",
			ThemeKey:   "项目复盘汇报",
			Confidence: 0.8,
		}},
	))

	event := receiveEvent("group", "text", `{"text":"下周要给老板做个复盘汇报，资料 https://example.com/doc"}`, "app")
	event.Event.Sender.SenderId.UserId = stringPtr("cli_bot")
	if err := handler.HandleMessage(context.Background(), event); err != nil {
		t.Fatalf("handle message: %v", err)
	}

	if launcher.proactiveInput.Title != "" {
		t.Fatalf("did not expect proactive candidate from bot message: %#v", launcher.proactiveInput)
	}
	if len(launcher.chatMessages) != 0 {
		t.Fatalf("did not expect bot message to enter proactive cache: %#v", launcher.chatMessages)
	}
	if got := len(messenger.replies()); got != 0 {
		t.Fatalf("did not expect replies, got %d", got)
	}
}

func TestHandleCardActionConfirmsProactiveCandidateWithClickerAsOwner(t *testing.T) {
	t.Parallel()

	launcher := &fakeLauncher{
		confirmedTask: domain.Task{TaskID: "task-proactive", ChatID: "oc_test", MessageID: "om_test", Status: domain.StatusExecuting},
		chatMessages: []domain.ChatMessage{
			{MessageID: "om_before", ChatID: "oc_test", Content: "前置消息"},
			{MessageID: "om_test", ChatID: "oc_test", Content: "触发消息"},
			{MessageID: "om_after", ChatID: "oc_test", Content: "点击前新消息"},
		},
	}
	messenger := &fakeMessenger{}
	handler := NewHandler(launcher, messenger, "")
	handler.SetProactiveDetector(proactive.Config{Enabled: true}, proactive.NewDetector(proactive.Config{}, fakeProactiveJudge{}))

	resp, err := handler.HandleCardAction(context.Background(), proactiveCardActionEvent("cand-1", "ou_clicker", "u_clicker"))
	if err != nil {
		t.Fatalf("handle card action: %v", err)
	}
	if resp == nil || resp.Toast == nil || resp.Toast.Type != "success" {
		t.Fatalf("unexpected response: %#v", resp)
	}
	if launcher.confirmInput.ActorOpenID != "ou_clicker" || launcher.confirmInput.ActorUserID != "u_clicker" {
		t.Fatalf("unexpected confirm actor: %#v", launcher.confirmInput)
	}
	if launcher.consumedChatID != "oc_test" || launcher.consumedThroughID != "om_test" {
		t.Fatalf("expected proactive cache consumption through source message, got chat=%q through=%q", launcher.consumedChatID, launcher.consumedThroughID)
	}
	if len(launcher.chatMessages) != 1 || launcher.chatMessages[0].MessageID != "om_after" {
		t.Fatalf("expected only messages after source to remain, got %#v", launcher.chatMessages)
	}
}

func TestHandleMessageContinuesActiveTask(t *testing.T) {
	t.Parallel()

	launcher := &fakeLauncher{
		continuedTask: domain.Task{TaskID: "task-active", Status: domain.StatusExecuting},
		doneTask:      domain.Task{TaskID: "task-active", Status: domain.StatusWaitingAction},
	}
	messenger := &fakeMessenger{}
	handler := NewHandler(launcher, messenger, "")
	handler.doneTimeout = time.Second
	handler.doneInterval = time.Millisecond

	err := handler.HandleMessage(context.Background(), receiveEvent("group", "text", `{"text":"@_user_1 /assistant revise title"}`, "user"))
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}

	waitForReplies(t, messenger, 2)
	if launcher.createCalls != 0 {
		t.Fatalf("did not expect create call, got %d", launcher.createCalls)
	}
	if launcher.continueCalls != 1 {
		t.Fatalf("expected continue call, got %d", launcher.continueCalls)
	}
	if launcher.continueInput.SessionID != "chat:oc_test" {
		t.Fatalf("unexpected session id: %s", launcher.continueInput.SessionID)
	}
	if launcher.continueInput.Instruction != "revise title" {
		t.Fatalf("unexpected instruction: %s", launcher.continueInput.Instruction)
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
		receiveEvent("p2p", "text", `{"text":"/pilot legacy"}`, "user"),
		receiveEvent("p2p", "image", `{"text":"/assistant generate"}`, "user"),
		receiveEvent("p2p", "text", `{"text":"/assistant generate"}`, "app"),
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
	mu                 sync.Mutex
	createdTask        domain.Task
	continuedTask      domain.Task
	doneTask           domain.Task
	lastInput          orchestrator.CreateTaskInput
	continueInput      orchestrator.ContinueTaskInput
	submitInput        orchestrator.ActionInput
	confirmInput       orchestrator.ActionInput
	proactiveInput     orchestrator.CreateProactiveCandidateInput
	submitTaskID       string
	confirmCandidateID string
	createCalls        int
	continueCalls      int
	waitCalls          int
	createErr          error
	continueErr        error
	proactiveCandidate domain.ProactiveCandidate
	confirmedTask      domain.Task
	cooling            bool
	chatMessages       []domain.ChatMessage
	latestThemeKey     string
	consumedChatID     string
	consumedThroughID  string
}

func (f *fakeLauncher) CreateTask(_ context.Context, input orchestrator.CreateTaskInput) (domain.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls++
	f.lastInput = input
	return f.createdTask, f.createErr
}

func (f *fakeLauncher) ContinueTask(_ context.Context, input orchestrator.ContinueTaskInput) (domain.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.continueCalls++
	f.continueInput = input
	if f.continueErr != nil {
		return domain.Task{}, f.continueErr
	}
	if f.continuedTask.TaskID == "" {
		return domain.Task{}, errors.New("no active task")
	}
	return f.continuedTask, nil
}

func (f *fakeLauncher) EndActiveTask(_ context.Context, _, _ string) (domain.Task, error) {
	return domain.Task{}, nil
}

func (f *fakeLauncher) SubmitAction(_ context.Context, taskID string, input orchestrator.ActionInput) (domain.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.submitTaskID = taskID
	f.submitInput = input
	return domain.Task{}, nil
}

func (f *fakeLauncher) CreateProactiveCandidate(_ context.Context, input orchestrator.CreateProactiveCandidateInput) (domain.ProactiveCandidate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.proactiveInput = input
	if f.proactiveCandidate.CandidateID == "" {
		f.proactiveCandidate = domain.ProactiveCandidate{
			CandidateID: "cand-1",
			ChatID:      input.ChatID,
			Title:       input.Title,
			ThemeKey:    input.ThemeKey,
			Status:      domain.CandidatePending,
		}
	}
	return f.proactiveCandidate, nil
}

func (f *fakeLauncher) HasRecentProactiveCandidate(_ context.Context, _, _ string, _ time.Duration) (bool, error) {
	return f.cooling, nil
}

func (f *fakeLauncher) LatestProactiveThemeKey(_ context.Context, _ string) (string, error) {
	return f.latestThemeKey, nil
}

func (f *fakeLauncher) ConfirmProactiveCandidate(_ context.Context, candidateID string, input orchestrator.ActionInput) (domain.Task, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.confirmCandidateID = candidateID
	f.confirmInput = input
	if f.confirmedTask.TaskID == "" {
		f.confirmedTask = domain.Task{TaskID: "task-proactive", Status: domain.StatusExecuting}
	}
	return f.confirmedTask, nil
}

func (f *fakeLauncher) IgnoreProactiveCandidate(_ context.Context, candidateID string) (domain.ProactiveCandidate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.confirmCandidateID = candidateID
	return domain.ProactiveCandidate{CandidateID: candidateID, Status: domain.CandidateIgnored}, nil
}

func (f *fakeLauncher) AppendChatMessage(_ context.Context, message domain.ChatMessage, keepLimit int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.chatMessages = append(f.chatMessages, message)
	if keepLimit > 0 && len(f.chatMessages) > keepLimit {
		f.chatMessages = f.chatMessages[len(f.chatMessages)-keepLimit:]
	}
	return nil
}

func (f *fakeLauncher) ListRecentChatMessages(_ context.Context, _ string, limit int) ([]domain.ChatMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	messages := f.chatMessages
	if limit > 0 && len(messages) > limit {
		messages = messages[len(messages)-limit:]
	}
	out := make([]domain.ChatMessage, len(messages))
	copy(out, messages)
	return out, nil
}

func (f *fakeLauncher) ConsumeChatMessages(_ context.Context, chatID, throughMessageID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.consumedChatID = chatID
	f.consumedThroughID = throughMessageID
	if throughMessageID == "" {
		f.chatMessages = nil
		return nil
	}
	keep := f.chatMessages[:0]
	consume := true
	for _, message := range f.chatMessages {
		if !consume {
			keep = append(keep, message)
			continue
		}
		if message.MessageID == throughMessageID {
			consume = false
		}
	}
	f.chatMessages = append([]domain.ChatMessage(nil), keep...)
	return nil
}

func (f *fakeLauncher) ListIdleWaitingTasks(_ context.Context, _ time.Duration) ([]domain.Task, error) {
	return nil, nil
}

func (f *fakeLauncher) MarkIdlePrompted(_ context.Context, _ string) (domain.Task, error) {
	return domain.Task{}, nil
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
	fileList []string
}

type fakeProactiveJudge struct {
	judgement        proactive.Judgement
	err              error
	previousThemeKey string
}

func (f fakeProactiveJudge) Judge(_ context.Context, _ []domain.ChatMessage, previousThemeKey string) (proactive.Judgement, error) {
	f.previousThemeKey = previousThemeKey
	return f.judgement, f.err
}

func (f *fakeMessenger) ReplyText(_ context.Context, _ string, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, text)
	return nil
}

func (f *fakeMessenger) ReplyFile(_ context.Context, _ string, filePath string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fileList = append(f.fileList, filePath)
	return nil
}

func (f *fakeMessenger) SendText(_ context.Context, _, _, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, text)
	return nil
}

func (f *fakeMessenger) SendInteractive(_ context.Context, _, _, text string) error {
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

func (f *fakeMessenger) files() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.fileList))
	copy(out, f.fileList)
	return out
}

func receiveEvent(chatType, messageType, content, senderType string) *larkim.P2MessageReceiveV1 {
	messageID := "om_test"
	chatID := "oc_test"
	threadID := ""
	userID := "u_sender"
	openID := "ou_sender"
	unionID := "on_sender"
	return &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Sender: &larkim.EventSender{
				SenderType: &senderType,
				SenderId: &larkim.UserId{
					UserId:  &userID,
					OpenId:  &openID,
					UnionId: &unionID,
				},
			},
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

func cardActionEvent(taskID, openID, userID string) *callback.CardActionTriggerEvent {
	return &callback.CardActionTriggerEvent{
		Event: &callback.CardActionTriggerRequest{
			Operator: &callback.Operator{
				OpenID: openID,
				UserID: &userID,
			},
			Action: &callback.CallBackAction{
				Value: map[string]interface{}{
					"action":  string(domain.ActionEndTask),
					"task_id": taskID,
				},
			},
		},
	}
}

func proactiveCardActionEvent(candidateID, openID, userID string) *callback.CardActionTriggerEvent {
	return &callback.CardActionTriggerEvent{
		Event: &callback.CardActionTriggerRequest{
			Operator: &callback.Operator{
				OpenID: openID,
				UserID: &userID,
			},
			Action: &callback.CallBackAction{
				Value: map[string]interface{}{
					"action":       "proactive_task_confirm",
					"candidate_id": candidateID,
				},
			},
		},
	}
}

func stringPtr(value string) *string {
	return &value
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
