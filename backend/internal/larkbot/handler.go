package larkbot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"agentpilot/backend/internal/domain"
	"agentpilot/backend/internal/orchestrator"

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type TaskLauncher interface {
	CreateTask(ctx context.Context, input orchestrator.CreateTaskInput) (domain.Task, error)
	ContinueTask(ctx context.Context, input orchestrator.ContinueTaskInput) (domain.Task, error)
	EndActiveTask(ctx context.Context, sessionID, actorType string) (domain.Task, error)
	SubmitAction(ctx context.Context, taskID string, input orchestrator.ActionInput) (domain.Task, error)
	ListIdleWaitingTasks(ctx context.Context, idleFor time.Duration) ([]domain.Task, error)
	MarkIdlePrompted(ctx context.Context, taskID string) (domain.Task, error)
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
		return h.messenger.ReplyText(ctx, in.messageID, "用法：/assistant <需求>\n使用 /assistant new <需求> 开始一个新任务。")
	}

	sessionID := sessionIDForMessage(in)
	if cmd.New {
		_, _ = h.launcher.EndActiveTask(ctx, sessionID, "feishu")
		if strings.TrimSpace(cmd.Intent) == "" {
			return h.messenger.ReplyText(ctx, in.messageID, "当前Assistant任务已结束。")
		}
	} else {
		task, err := h.launcher.ContinueTask(ctx, orchestrator.ContinueTaskInput{
			SessionID:   sessionID,
			Instruction: cmd.Intent,
			MessageID:   in.messageID,
			ActorType:   "feishu",
		})
		if err == nil {
			if err := h.messenger.ReplyText(ctx, in.messageID, h.revisionStartedText(task)); err != nil {
				return err
			}
			go h.notifyWhenDone(in.messageID, task.TaskID)
			return nil
		}
		if strings.Contains(err.Error(), "still running") {
			return h.messenger.ReplyText(ctx, in.messageID, "当前Assistant任务仍在处理中，请等待完成后再发送修改要求。")
		}
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
		return h.messenger.ReplyText(ctx, in.messageID, "Assistant任务启动失败："+err.Error())
	}

	if err := h.messenger.ReplyText(ctx, in.messageID, h.startedText(task)); err != nil {
		return err
	}

	go h.notifyWhenDone(in.messageID, task.TaskID)
	return nil
}

func (h *Handler) HandleCardAction(ctx context.Context, event *callback.CardActionTriggerEvent) (*callback.CardActionTriggerResponse, error) {
	value := map[string]any{}
	if event != nil && event.Event != nil && event.Event.Action != nil {
		value = event.Event.Action.Value
	}
	action, _ := value["action"].(string)
	taskID, _ := value["task_id"].(string)
	if action != string(domain.ActionEndTask) || strings.TrimSpace(taskID) == "" {
		return cardToast("warning", "不支持的操作。"), nil
	}
	if _, err := h.launcher.SubmitAction(ctx, taskID, orchestrator.ActionInput{
		ActionType: string(domain.ActionEndTask),
		ActorType:  "feishu_card",
		ClientID:   "feishu_card",
	}); err != nil {
		return cardToast("error", err.Error()), nil
	}
	return cardToast("success", "已关闭当前Assistant任务。"), nil
}

func (h *Handler) PromptIdleTasks(ctx context.Context, idleFor time.Duration) {
	tasks, err := h.launcher.ListIdleWaitingTasks(ctx, idleFor)
	if err != nil {
		return
	}
	for _, task := range tasks {
		if task.ChatID == "" {
			continue
		}
		content, err := idleTaskCardContent(task)
		if err != nil {
			continue
		}
		if err := h.messenger.SendInteractive(ctx, task.ChatID, "chat_id", content); err != nil {
			continue
		}
		_, _ = h.launcher.MarkIdlePrompted(ctx, task.TaskID)
	}
}

func (h *Handler) notifyWhenDone(messageID, taskID string) {
	ctx, cancel := context.WithTimeout(context.Background(), h.doneTimeout+10*time.Second)
	defer cancel()

	task, err := h.launcher.WaitTaskDone(ctx, taskID, h.doneTimeout, h.doneInterval)
	if err != nil {
		_ = h.messenger.ReplyText(context.Background(), messageID, fmt.Sprintf("Assistant任务 %s 尚未完成：%v", taskID, err))
		return
	}
	_ = h.messenger.ReplyText(context.Background(), messageID, h.doneText(task))
}

func (h *Handler) startedText(task domain.Task) string {
	lines := []string{
		fmt.Sprintf("Assistant任务已启动：%s", task.TaskID),
		"我会在完成后回复汇总结果。",
	}
	if link := h.taskLink(task.TaskID); link != "" {
		lines = append(lines, "进度："+link)
	}
	return strings.Join(lines, "\n")
}

func (h *Handler) revisionStartedText(task domain.Task) string {
	lines := []string{
		fmt.Sprintf("Assistant任务更新：%s", task.TaskID),
		"我会更新现有产物，并在完成后回复。",
	}
	if link := h.taskLink(task.TaskID); link != "" {
		lines = append(lines, "进度："+link)
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
		return fmt.Sprintf("Assistant任务失败：%s\n%s", task.TaskID, message)
	}
	lines := []string{fmt.Sprintf("Assistant任务待审核：%s", task.TaskID)}
	if task.Summary != "" {
		lines = append(lines, "摘要："+task.Summary)
	}
	if task.DocURL != "" {
		lines = append(lines, "文档："+h.publicLink(task.DocURL))
	}
	if task.SlidesURL != "" {
		lines = append(lines, "幻灯片："+h.publicLink(task.SlidesURL))
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
		return "Assistant任务"
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

func sessionIDForMessage(in incomingMessage) string {
	if in.chatID != "" {
		return "chat:" + in.chatID
	}
	return "message:" + in.messageID
}

func idleTaskCardContent(task domain.Task) (string, error) {
	title := task.Title
	if strings.TrimSpace(title) == "" {
		title = "Assistant任务"
	}
	card := map[string]any{
		"config": map[string]any{"wide_screen_mode": true},
		"header": map[string]any{
			"title": map[string]any{"tag": "plain_text", "content": "Assistant任务正在等待审核"},
		},
		"elements": []any{
			map[string]any{
				"tag":  "div",
				"text": map[string]any{"tag": "lark_md", "content": fmt.Sprintf("任务 **%s** 正在等待审核。如无需继续修改，可以结束任务。", title)},
			},
			map[string]any{
				"tag": "action",
				"actions": []any{
					map[string]any{
						"tag":  "button",
						"text": map[string]any{"tag": "plain_text", "content": "结束任务"},
						"type": "primary",
						"value": map[string]any{
							"action":  string(domain.ActionEndTask),
							"task_id": task.TaskID,
						},
					},
				},
			},
		},
	}
	payload, err := json.Marshal(card)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func cardToast(toastType, content string) *callback.CardActionTriggerResponse {
	return &callback.CardActionTriggerResponse{
		Toast: &callback.Toast{
			Type:    toastType,
			Content: content,
		},
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
