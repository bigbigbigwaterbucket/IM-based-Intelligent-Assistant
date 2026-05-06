package larkbot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agentpilot/backend/internal/domain"
	"agentpilot/backend/internal/orchestrator"
	"agentpilot/backend/internal/proactive"
	"agentpilot/backend/internal/store"

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type TaskLauncher interface {
	CreateTask(ctx context.Context, input orchestrator.CreateTaskInput) (domain.Task, error)
	ContinueTask(ctx context.Context, input orchestrator.ContinueTaskInput) (domain.Task, error)
	EndActiveTask(ctx context.Context, sessionID, actorType string) (domain.Task, error)
	SubmitAction(ctx context.Context, taskID string, input orchestrator.ActionInput) (domain.Task, error)
	CreateProactiveCandidate(ctx context.Context, input orchestrator.CreateProactiveCandidateInput) (domain.ProactiveCandidate, error)
	HasRecentProactiveCandidate(ctx context.Context, chatID, themeKey string, cooldown time.Duration) (bool, error)
	LatestProactiveThemeKey(ctx context.Context, chatID string) (string, error)
	ConfirmProactiveCandidate(ctx context.Context, candidateID string, input orchestrator.ActionInput) (domain.Task, error)
	IgnoreProactiveCandidate(ctx context.Context, candidateID string) (domain.ProactiveCandidate, error)
	ListIdleWaitingTasks(ctx context.Context, idleFor time.Duration) ([]domain.Task, error)
	MarkIdlePrompted(ctx context.Context, taskID string) (domain.Task, error)
	WaitTaskDone(ctx context.Context, taskID string, timeout, interval time.Duration) (domain.Task, error)
}

type Handler struct {
	launcher      TaskLauncher
	messenger     TextMessenger
	publicBaseURL string
	botAppID      string
	doneTimeout   time.Duration
	doneInterval  time.Duration
	proactiveCfg  proactive.Config
	detector      *proactive.Detector
	chatHistory   store.ChatMessageRepository
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

func (h *Handler) SetBotIdentity(appID string) {
	h.botAppID = strings.TrimSpace(appID)
}

func (h *Handler) SetProactiveDetector(config proactive.Config, detector *proactive.Detector) {
	h.proactiveCfg = config
	h.detector = detector
	if history, ok := h.launcher.(store.ChatMessageRepository); ok {
		h.chatHistory = history
	}
}

func (h *Handler) HandleMessage(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
	if h.isBotMessage(event) {
		return nil
	}
	in, ok := eventInput(event)
	if !ok {
		return nil
	}
	println(in.content)
	cmd, ok := ParseTextContent(in.messageType, in.content)
	if !ok {
		return h.handleProactiveMessage(ctx, in)
	}
	if cmd.Name != AssistantCommand {
		return h.handleProactiveMessage(ctx, in)
	}
	if cmd.Help {
		return h.messenger.ReplyText(ctx, in.messageID, "用法：/assistant <需求>\n使用 /assistant new <需求> 开始一个新任务。")
	}
	return h.handleAssistantCommand(ctx, in, cmd)
}

func (h *Handler) handleAssistantCommand(ctx context.Context, in incomingMessage, cmd Command) error {
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
			println("", err.Error())
			return h.messenger.ReplyText(ctx, in.messageID, "当前Assistant任务仍在处理中，请等待完成后再发送修改要求。")
		}
	}

	task, err := h.launcher.CreateTask(ctx, orchestrator.CreateTaskInput{
		Title:            taskTitle(cmd.Intent),
		Instruction:      cmd.Intent,
		Source:           sourceForChatType(in.chatType),
		ChatID:           in.chatID,
		ThreadID:         in.threadID,
		MessageID:        in.messageID,
		InitiatorUserID:  in.senderUserID,
		InitiatorOpenID:  in.senderOpenID,
		InitiatorUnionID: in.senderUnionID,
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
	if action == "proactive_task_confirm" {
		return h.handleProactiveConfirm(ctx, event, value)
	}
	if action == "proactive_task_ignore" {
		return h.handleProactiveIgnore(ctx, value)
	}
	taskID, _ := value["task_id"].(string)
	if action != string(domain.ActionEndTask) || strings.TrimSpace(taskID) == "" {
		return cardToast("warning", "不支持的操作。"), nil
	}
	if _, err := h.launcher.SubmitAction(ctx, taskID, orchestrator.ActionInput{
		ActionType:   string(domain.ActionEndTask),
		ActorType:    "feishu_card",
		ClientID:     "feishu_card",
		ActorUserID:  cardOperatorUserID(event),
		ActorOpenID:  cardOperatorOpenID(event),
		ActorUnionID: cardOperatorUnionID(event),
	}); err != nil {
		return cardToast("error", err.Error()), nil
	}
	return cardToast("success", "已关闭当前Assistant任务。"), nil
}

func (h *Handler) handleProactiveMessage(ctx context.Context, in incomingMessage) error {
	if !h.proactiveCfg.Enabled || h.detector == nil || h.chatHistory == nil {
		return nil
	}
	// 非群聊消息不会主动发起总结
	if in.messageType != "text" || in.chatID == "" || !isGroupChat(in.chatType) {
		return nil
	}
	text := strings.TrimSpace(in.text())
	if text == "" {
		return nil
	}
	message := domain.ChatMessage{
		MessageID:     in.messageID,
		ChatID:        in.chatID,
		ThreadID:      in.threadID,
		SenderUserID:  in.senderUserID,
		SenderOpenID:  in.senderOpenID,
		SenderUnionID: in.senderUnionID,
		Content:       text,
		ChatType:      in.chatType,
		CreatedAt:     time.Now(),
	}
	if err := h.chatHistory.AppendChatMessage(ctx, message, h.proactiveCfg.CacheLimit); err != nil {
		return nil
	}
	messages, err := h.chatHistory.ListRecentChatMessages(ctx, in.chatID, h.proactiveCfg.CacheLimit)
	if err != nil {
		return nil
	}
	previousThemeKey, _ := h.launcher.LatestProactiveThemeKey(ctx, in.chatID)
	candidate, err := h.detector.DetectWithPreviousThemeKey(ctx, messages, previousThemeKey)
	if err != nil || !candidate.Ready {
		return nil
	}
	cooling, err := h.launcher.HasRecentProactiveCandidate(ctx, in.chatID, candidate.ThemeKey, h.proactiveCfg.Cooldown)
	if err != nil || cooling {
		return nil
	}
	saved, err := h.launcher.CreateProactiveCandidate(ctx, orchestrator.CreateProactiveCandidateInput{
		ChatID:          in.chatID,
		ThreadID:        in.threadID,
		SourceMessageID: in.messageID,
		Title:           candidate.Title,
		Instruction:     candidate.Instruction,
		ContextJSON:     candidate.ContextJSON,
		ThemeKey:        candidate.ThemeKey,
		TTL:             24 * time.Hour,
	})
	if err != nil {
		return nil
	}
	content, err := proactiveTaskCardContent(saved, candidate, len(messages))
	if err != nil {
		return nil
	}
	_ = h.messenger.SendInteractive(ctx, in.chatID, "chat_id", content)
	return nil
}

func (h *Handler) handleProactiveConfirm(ctx context.Context, event *callback.CardActionTriggerEvent, value map[string]any) (*callback.CardActionTriggerResponse, error) {
	candidateID, _ := value["candidate_id"].(string)
	if strings.TrimSpace(candidateID) == "" {
		return cardToast("warning", "缺少候选任务。"), nil
	}
	task, err := h.launcher.ConfirmProactiveCandidate(ctx, candidateID, orchestrator.ActionInput{
		ActionType:   "proactive_task_confirm",
		ActorType:    "feishu_card",
		ClientID:     "feishu_card",
		ActorUserID:  cardOperatorUserID(event),
		ActorOpenID:  cardOperatorOpenID(event),
		ActorUnionID: cardOperatorUnionID(event),
	})
	if err != nil {
		return cardToast("error", err.Error()), nil
	}
	if task.ChatID != "" {
		if h.chatHistory != nil {
			_ = h.chatHistory.ConsumeChatMessages(ctx, task.ChatID, task.MessageID)
		}
		_ = h.messenger.SendText(ctx, task.ChatID, "chat_id", h.startedText(task))
	}
	go h.notifyWhenDone(task.MessageID, task.TaskID)
	return cardToast("success", "已启动Assistant任务。"), nil
}

func (h *Handler) handleProactiveIgnore(ctx context.Context, value map[string]any) (*callback.CardActionTriggerResponse, error) {
	candidateID, _ := value["candidate_id"].(string)
	if strings.TrimSpace(candidateID) == "" {
		return cardToast("warning", "缺少候选任务。"), nil
	}
	if _, err := h.launcher.IgnoreProactiveCandidate(ctx, candidateID); err != nil {
		return cardToast("error", err.Error()), nil
	}
	return cardToast("success", "已忽略该候选任务。"), nil
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
	if pptxPath := localSlidesPPTXPath(task); pptxPath != "" {
		_ = h.messenger.ReplyFile(context.Background(), messageID, pptxPath)
	}
}

func (h *Handler) startedText(task domain.Task) string {
	lines := []string{
		fmt.Sprintf("Assistant任务已启动：%s", task.TaskID),
		"实时查看任务进度/在线编辑文档: http://localhost:5173",
		"我会在完成后回复汇总结果。",
	}
	if owner := ownerMention(task); owner != "" {
		lines = append(lines, "归属："+owner)
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
	if owner := ownerMention(task); owner != "" {
		lines = append(lines, "归属："+owner)
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

func ownerMention(task domain.Task) string {
	id := strings.TrimSpace(task.InitiatorOpenID)
	if id == "" {
		id = strings.TrimSpace(task.InitiatorUserID)
	}
	if id == "" {
		id = strings.TrimSpace(task.InitiatorUnionID)
	}
	if id == "" {
		return ""
	}
	return fmt.Sprintf(`<at user_id="%s">Owner</at>`, escapeAtUserID(id))
}

func escapeAtUserID(value string) string {
	return strings.NewReplacer("&", "&amp;", `"`, "&quot;", "<", "&lt;", ">", "&gt;").Replace(value)
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
		if localSlidesPPTXPath(task) != "" {
			lines = append(lines, "幻灯片：见下方 PPT 文件")
		} else {
			lines = append(lines, "幻灯片："+h.publicLink(task.SlidesURL))
		}
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
	messageID     string
	messageType   string
	content       string
	chatType      string
	chatID        string
	threadID      string
	senderUserID  string
	senderOpenID  string
	senderUnionID string
}

func (in incomingMessage) text() string {
	cmd, ok := ParseTextContent(in.messageType, in.content)
	if ok {
		return cmd.Intent
	}
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(in.content), &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Text)
}

func (h *Handler) isBotMessage(event *larkim.P2MessageReceiveV1) bool {
	if event == nil || event.Event == nil || event.Event.Sender == nil {
		return false
	}
	sender := event.Event.Sender
	if strings.EqualFold(strings.TrimSpace(stringValue(sender.SenderType)), "app") {
		return true
	}
	if strings.TrimSpace(h.botAppID) == "" || sender.SenderId == nil {
		return false
	}
	return stringValue(sender.SenderId.UserId) == h.botAppID ||
		stringValue(sender.SenderId.OpenId) == h.botAppID ||
		stringValue(sender.SenderId.UnionId) == h.botAppID
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
		messageID:     stringValue(message.MessageId),
		messageType:   stringValue(message.MessageType),
		content:       stringValue(message.Content),
		chatType:      stringValue(message.ChatType),
		chatID:        stringValue(message.ChatId),
		threadID:      stringValue(message.ThreadId),
		senderUserID:  messageSenderUserID(event.Event.Sender),
		senderOpenID:  messageSenderOpenID(event.Event.Sender),
		senderUnionID: messageSenderUnionID(event.Event.Sender),
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

func isGroupChat(chatType string) bool {
	return chatType == "group" || chatType == "topic_group"
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

func proactiveTaskCardContent(candidate domain.ProactiveCandidate, detected proactive.Candidate, messageCount int) (string, error) {
	title := candidate.Title
	if strings.TrimSpace(title) == "" {
		title = "可能的Assistant任务"
	}
	reason := strings.TrimSpace(detected.Reason)
	if reason == "" {
		reason = "群聊中出现了办公任务信号"
	}
	card := map[string]any{
		"config": map[string]any{"wide_screen_mode": true},
		"header": map[string]any{
			"title": map[string]any{"tag": "plain_text", "content": "检测到可能需要启动Assistant任务"},
		},
		"elements": []any{
			map[string]any{
				"tag":  "div",
				"text": map[string]any{"tag": "lark_md", "content": fmt.Sprintf("**%s**\n%s\n已参考最近 %d 条群聊消息。", title, reason, messageCount)},
			},
			map[string]any{
				"tag": "action",
				"actions": []any{
					map[string]any{
						"tag":  "button",
						"text": map[string]any{"tag": "plain_text", "content": "启动任务"},
						"type": "primary",
						"value": map[string]any{
							"action":       "proactive_task_confirm",
							"candidate_id": candidate.CandidateID,
						},
					},
					map[string]any{
						"tag":  "button",
						"text": map[string]any{"tag": "plain_text", "content": "忽略"},
						"type": "default",
						"value": map[string]any{
							"action":       "proactive_task_ignore",
							"candidate_id": candidate.CandidateID,
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

func messageSenderUserID(sender *larkim.EventSender) string {
	if sender == nil || sender.SenderId == nil {
		return ""
	}
	return stringValue(sender.SenderId.UserId)
}

func messageSenderOpenID(sender *larkim.EventSender) string {
	if sender == nil || sender.SenderId == nil {
		return ""
	}
	return stringValue(sender.SenderId.OpenId)
}

func messageSenderUnionID(sender *larkim.EventSender) string {
	if sender == nil || sender.SenderId == nil {
		return ""
	}
	return stringValue(sender.SenderId.UnionId)
}

func cardOperatorUserID(event *callback.CardActionTriggerEvent) string {
	if event == nil || event.Event == nil || event.Event.Operator == nil {
		return ""
	}
	return stringValue(event.Event.Operator.UserID)
}

func cardOperatorOpenID(event *callback.CardActionTriggerEvent) string {
	if event == nil || event.Event == nil || event.Event.Operator == nil {
		return ""
	}
	return strings.TrimSpace(event.Event.Operator.OpenID)
}

func cardOperatorUnionID(_ *callback.CardActionTriggerEvent) string {
	return ""
}

func localSlidesPPTXPath(task domain.Task) string {
	path := strings.TrimSpace(task.SlidesArtifactPath)
	if path == "" {
		return ""
	}
	if isUsableLocalFile(path) && strings.EqualFold(filepath.Ext(path), ".pptx") {
		return path
	}

	candidates := make([]string, 0, 2)
	if strings.EqualFold(filepath.Ext(path), ".md") {
		candidates = append(candidates, strings.TrimSuffix(path, filepath.Ext(path))+".pptx")
	}
	if strings.HasPrefix(task.SlidesURL, "/artifacts/") {
		candidates = append(candidates, filepath.Join(filepath.Dir(path), filepath.Base(task.SlidesURL)))
	}
	for _, candidate := range candidates {
		if isUsableLocalFile(candidate) {
			return candidate
		}
	}
	return ""
}

func isUsableLocalFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Size() > 0
}

var errMissingDependency = errors.New("missing larkbot dependency")

func (h *Handler) validate() error {
	if h.launcher == nil || h.messenger == nil {
		return errMissingDependency
	}
	return nil
}
