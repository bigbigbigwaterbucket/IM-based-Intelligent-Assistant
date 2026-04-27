package larkbot

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"agentpilot/backend/internal/domain"
	"agentpilot/backend/internal/orchestrator"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type TaskLauncher interface {
	CreateTask(ctx context.Context, input orchestrator.CreateTaskInput) (domain.Task, error)
	WaitTaskDone(ctx context.Context, taskID string, timeout, interval time.Duration) (domain.Task, error)
}

type Handler struct {
	launcher      TaskLauncher
	messenger     TextMessenger
	publicBaseURL string
	doneTimeout   time.Duration
	doneInterval  time.Duration
}

func NewHandler(launcher TaskLauncher, messenger TextMessenger, publicBaseURL string) *Handler {
	return &Handler{
		launcher:      launcher,
		messenger:     messenger,
		publicBaseURL: strings.TrimRight(publicBaseURL, "/"),
		doneTimeout:   180 * time.Second,
		doneInterval:  3 * time.Second,
	}
}

func (h *Handler) HandleMessage(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
	in, ok := eventInput(event)
	if !ok {
		return nil
	}

	cmd, ok := ParseTextContent(in.messageType, in.content)
	if !ok {
		return nil
	}
	if cmd.Name != AssistantCommand {
		return nil
	}
	if cmd.Help {
		return h.messenger.ReplyText(ctx, in.messageID, "用法：/assistant <你的需求>\n例如：/assistant 把群聊消息总结成方案并生成演示稿")
	}

	task, err := h.launcher.CreateTask(ctx, orchestrator.CreateTaskInput{
		Title:       taskTitle(cmd.Intent),
		Instruction: cmd.Intent,
		Source:      sourceForChatType(in.chatType),
		ChatID:      in.chatID,
		ThreadID:    in.threadID,
		MessageID:   in.messageID,
	})
	if err != nil {
		return h.messenger.ReplyText(ctx, in.messageID, "Assistant 启动失败："+err.Error())
	}

	if err := h.messenger.ReplyText(ctx, in.messageID, h.startedText(task)); err != nil {
		return err
	}

	go h.notifyWhenDone(in.messageID, task.TaskID)
	return nil
}

func (h *Handler) notifyWhenDone(messageID, taskID string) {
	ctx, cancel := context.WithTimeout(context.Background(), h.doneTimeout+10*time.Second)
	defer cancel()

	task, err := h.launcher.WaitTaskDone(ctx, taskID, h.doneTimeout, h.doneInterval)
	if err != nil {
		_ = h.messenger.ReplyText(context.Background(), messageID, fmt.Sprintf("Assistant 任务 %s 暂未完成：%v", taskID, err))
		return
	}
	_ = h.messenger.ReplyText(context.Background(), messageID, h.doneText(task))
}

func (h *Handler) startedText(task domain.Task) string {
	lines := []string{
		fmt.Sprintf("Assistant 已启动：%s", task.TaskID),
		"我会在完成后回帖汇总结果。",
	}
	if link := h.taskLink(task.TaskID); link != "" {
		lines = append(lines, "实时进度："+link)
	}
	return strings.Join(lines, "\n")
}

func (h *Handler) taskLink(taskID string) string {
	if h.publicBaseURL == "" {
		return ""
	}
	return h.publicBaseURL + "/?taskId=" + url.QueryEscape(taskID)
}

func (h *Handler) doneText(task domain.Task) string {
	if task.Status == domain.StatusFailed {
		message := task.ErrorMessage
		if message == "" {
			message = task.ProgressText
		}
		return fmt.Sprintf("Assistant 任务失败：%s\n%s", task.TaskID, message)
	}

	lines := []string{
		fmt.Sprintf("Assistant 任务完成：%s", task.TaskID),
	}
	if task.Summary != "" {
		lines = append(lines, "摘要："+task.Summary)
	}
	if task.DocURL != "" {
		lines = append(lines, "文档："+h.publicLink(task.DocURL))
	}
	if task.SlidesURL != "" {
		lines = append(lines, "演示稿："+h.publicLink(task.SlidesURL))
	}
	return strings.Join(lines, "\n")
}

func (h *Handler) publicLink(raw string) string {
	if h.publicBaseURL == "" || strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw
	}
	if strings.HasPrefix(raw, "/") {
		return h.publicBaseURL + raw
	}
	return raw
}

type incomingMessage struct {
	messageID   string
	messageType string
	content     string
	chatType    string
	chatID      string
	threadID    string
}

func eventInput(event *larkim.P2MessageReceiveV1) (incomingMessage, bool) {
	if event == nil || event.Event == nil || event.Event.Sender == nil || event.Event.Message == nil {
		return incomingMessage{}, false
	}
	if stringValue(event.Event.Sender.SenderType) != "user" {
		return incomingMessage{}, false
	}

	message := event.Event.Message
	in := incomingMessage{
		messageID:   stringValue(message.MessageId),
		messageType: stringValue(message.MessageType),
		content:     stringValue(message.Content),
		chatType:    stringValue(message.ChatType),
		chatID:      stringValue(message.ChatId),
		threadID:    stringValue(message.ThreadId),
	}
	if in.messageID == "" {
		return incomingMessage{}, false
	}
	return in, true
}

func taskTitle(intent string) string {
	intent = strings.TrimSpace(intent)
	if intent == "" {
		return "Assistant 任务"
	}
	runes := []rune(intent)
	if len(runes) <= 48 {
		return intent
	}
	return string(runes[:48]) + "..."
}

func sourceForChatType(chatType string) string {
	switch chatType {
	case "p2p":
		return "feishu_p2p"
	case "group", "topic_group":
		return "feishu_group"
	default:
		return "feishu"
	}
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

var errMissingDependency = errors.New("missing larkbot dependency")

func (h *Handler) validate() error {
	if h.launcher == nil || h.messenger == nil {
		return errMissingDependency
	}
	return nil
}
