package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkdocx "github.com/larksuite/oapi-sdk-go/v3/service/docx/v1"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	"agentpilot/backend/internal/domain"
)

const defaultArtifactDir = "data/pilot_artifacts"

type Config struct {
	FeishuAppID       string
	FeishuAppSecret   string
	EnableFeishuTools bool
	FeishuDocBaseURL  string
	ArtifactDir       string
}

type Runner struct {
	config Config
	client *lark.Client
}

type Result struct {
	Success        bool
	StepName       string
	PayloadSummary string
	Retryable      bool
	ErrorMessage   string
	ArtifactURL    string
	ArtifactPath   string
	Data           map[string]string
}

func NewRunner(config Config) *Runner {
	if config.ArtifactDir == "" {
		config.ArtifactDir = defaultArtifactDir
	}
	config.FeishuDocBaseURL = strings.TrimRight(config.FeishuDocBaseURL, "/")

	var client *lark.Client
	if config.EnableFeishuTools && config.FeishuAppID != "" && config.FeishuAppSecret != "" {
		client = lark.NewClient(config.FeishuAppID, config.FeishuAppSecret)
	}
	return &Runner{config: config, client: client}
}

func ArtifactDir() string {
	return defaultArtifactDir
}

func (r *Runner) FetchThread(ctx context.Context, task domain.Task, step domain.PlanStep) Result {
	limit := limitFromStep(step, 20)
	data := map[string]string{
		"source":     "feishu_im",
		"limit":      strconv.Itoa(limit),
		"chat_id":    task.ChatID,
		"thread_id":  task.ThreadID,
		"message_id": task.MessageID,
	}

	if task.ChatID == "" {
		data["source"] = "missing_chat_id"
		return Result{
			Success:        true,
			StepName:       "im.fetch_thread",
			PayloadSummary: "当前任务没有飞书 chat_id，跳过 IM 历史读取。",
			Data:           data,
		}
	}
	if r.client == nil {
		data["source"] = "feishu_sdk_disabled"
		return Result{
			Success:        true,
			StepName:       "im.fetch_thread",
			PayloadSummary: "飞书 SDK 工具未启用或缺少应用凭据，已记录 IM 上下文读取需求。",
			Data:           data,
		}
	}

	req := larkim.NewListMessageReqBuilder().
		ContainerIdType("chat").
		ContainerId(task.ChatID).
		SortType(larkim.SortTypeListMessageByCreateTimeDesc).
		PageSize(limit).
		Build()
	resp, err := r.client.Im.V1.Message.List(ctx, req)
	if err != nil {
		return failed("im.fetch_thread", err)
	}
	if resp == nil {
		return failed("im.fetch_thread", errors.New("empty Feishu IM response"))
	}
	if !resp.Success() {
		return failed("im.fetch_thread", fmt.Errorf("Feishu IM list failed: code=%d msg=%s", resp.Code, resp.Msg))
	}

	messages := normalizeMessages(resp.Data, task.ThreadID)
	if len(messages) > 0 {
		data["messages"] = strings.Join(messages, "\n")
	}
	data["count"] = strconv.Itoa(len(messages))
	return Result{
		Success:        true,
		StepName:       "im.fetch_thread",
		PayloadSummary: fmt.Sprintf("已读取飞书会话最近 %d 条消息。", len(messages)),
		Data:           data,
	}
}

func (r *Runner) CompleteStep(step domain.PlanStep) Result {
	return Result{
		Success:        true,
		StepName:       step.Tool,
		PayloadSummary: step.Description,
		Data:           map[string]string{"source": "logical_step"},
	}
}

func (r *Runner) CreateDoc(ctx context.Context, plan domain.Plan, instruction string) Result {
	if err := os.MkdirAll(r.config.ArtifactDir, 0755); err != nil {
		return failed("doc.generate", err)
	}

	fileName := fmt.Sprintf("doc_%s.md", artifactID())
	path := filepath.Join(r.config.ArtifactDir, fileName)
	content := renderDocument(plan, instruction)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return failed("doc.generate", err)
	}

	result := Result{
		Success:        true,
		StepName:       "doc.generate",
		PayloadSummary: fmt.Sprintf("已生成结构化 Markdown 文档：%s", path),
		ArtifactURL:    "/artifacts/" + fileName,
		ArtifactPath:   path,
		Data:           map[string]string{"source": "local_markdown"},
	}

	if r.config.EnableFeishuTools {
		url, docID, err := r.createFeishuDoc(ctx, plan.DocTitle, content)
		if err == nil && docID != "" {
			result.Data["source"] = "feishu_docx"
			result.Data["feishu_document_id"] = docID
			result.Data["local_path"] = path
			result.PayloadSummary = "已通过 Go SDK 创建飞书 Docx：" + docID
			if url != "" {
				result.ArtifactURL = url
				result.PayloadSummary = "已通过 Go SDK 创建飞书 Docx：" + url
			}
		} else if err != nil {
			result.Data["feishu_error"] = err.Error()
		}
	}
	return result
}

func (r *Runner) CreateSlides(ctx context.Context, plan domain.Plan) Result {
	if err := os.MkdirAll(r.config.ArtifactDir, 0755); err != nil {
		return failed("slide.generate", err)
	}

	slideID := "slide_" + artifactID()
	fileName := slideID + ".md"
	path := filepath.Join(r.config.ArtifactDir, fileName)
	content := renderSlidev(plan)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return failed("slide.generate", err)
	}

	notesName := slideID + "_speaker_notes.md"
	notesPath := filepath.Join(r.config.ArtifactDir, notesName)
	if err := os.WriteFile(notesPath, []byte(renderSpeakerNotes(plan)), 0644); err != nil {
		return failed("slide.rehearse", err)
	}

	select {
	case <-ctx.Done():
		return failed("slide.generate", ctx.Err())
	default:
	}

	data := map[string]string{
		"source":        "slidev_markdown",
		"speaker_notes": "/artifacts/" + notesName,
	}
	if r.config.EnableFeishuTools {
		data["feishu_slides"] = "not_created"
		data["feishu_slides_reason"] = "github.com/larksuite/oapi-sdk-go/v3 v3.6.1 does not expose a direct Slides create service"
	}
	return Result{
		Success:        true,
		StepName:       "slide.generate",
		PayloadSummary: fmt.Sprintf("已生成 Slidev 演示稿：%s", path),
		ArtifactURL:    "/artifacts/" + fileName,
		ArtifactPath:   path,
		Data:           data,
	}
}

func (r *Runner) Bundle(ctx context.Context, task domain.Task, plan domain.Plan, docResult, slidesResult Result) Result {
	if err := os.MkdirAll(r.config.ArtifactDir, 0755); err != nil {
		return failed("archive.bundle", err)
	}

	fileName := fmt.Sprintf("manifest_%s.json", artifactID())
	path := filepath.Join(r.config.ArtifactDir, fileName)
	manifest := map[string]any{
		"taskId":         task.TaskID,
		"title":          task.Title,
		"instruction":    task.UserInstruction,
		"source":         task.Source,
		"chatId":         task.ChatID,
		"threadId":       task.ThreadID,
		"messageId":      task.MessageID,
		"summary":        plan.Summary,
		"plannerSource":  plan.PlannerSource,
		"plannerError":   plan.PlannerError,
		"createdAt":      time.Now().Format(time.RFC3339),
		"docUrl":         docResult.ArtifactURL,
		"docPath":        docResult.ArtifactPath,
		"slidesUrl":      slidesResult.ArtifactURL,
		"slidesPath":     slidesResult.ArtifactPath,
		"speakerNotes":   slidesResult.Data["speaker_notes"],
		"planSteps":      plan.Steps,
		"intentAnalysis": plan.Analysis,
	}
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return failed("archive.bundle", err)
	}
	if err := os.WriteFile(path, payload, 0644); err != nil {
		return failed("archive.bundle", err)
	}

	select {
	case <-ctx.Done():
		return failed("archive.bundle", ctx.Err())
	default:
	}
	return Result{
		Success:        true,
		StepName:       "archive.bundle",
		PayloadSummary: fmt.Sprintf("已汇总产物 manifest：%s", path),
		ArtifactURL:    "/artifacts/" + fileName,
		ArtifactPath:   path,
		Data:           map[string]string{"source": "local_manifest"},
	}
}

func (r *Runner) createFeishuDoc(ctx context.Context, title, markdown string) (string, string, error) {
	if r.client == nil {
		return "", "", errors.New("Feishu SDK client is not configured")
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "IM-based Assistant Document"
	}

	createReq := larkdocx.NewCreateDocumentReqBuilder().
		Body(larkdocx.NewCreateDocumentReqBodyBuilder().Title(title).Build()).
		Build()
	createResp, err := r.client.Docx.V1.Document.Create(ctx, createReq)
	if err != nil {
		return "", "", err
	}
	if createResp == nil {
		return "", "", errors.New("empty Feishu Docx create response")
	}
	if !createResp.Success() {
		return "", "", fmt.Errorf("Feishu Docx create failed: code=%d msg=%s", createResp.Code, createResp.Msg)
	}
	if createResp.Data == nil || createResp.Data.Document == nil || createResp.Data.Document.DocumentId == nil {
		return "", "", errors.New("Feishu Docx create response missing document_id")
	}

	docID := *createResp.Data.Document.DocumentId
	firstLevelBlockIDs, descendants, err := r.convertMarkdownToDocxBlocks(ctx, markdown)
	if err != nil {
		return "", docID, err
	}
	if len(firstLevelBlockIDs) > 0 && len(descendants) > 0 {
		appendReq := larkdocx.NewCreateDocumentBlockDescendantReqBuilder().
			DocumentId(docID).
			BlockId(docID).
			DocumentRevisionId(-1).
			Body(larkdocx.NewCreateDocumentBlockDescendantReqBodyBuilder().
				ChildrenId(firstLevelBlockIDs).
				Descendants(descendants).
				Index(0).
				Build()).
			Build()
		appendResp, err := r.client.Docx.V1.DocumentBlockDescendant.Create(ctx, appendReq)
		if err != nil {
			return "", docID, err
		}
		if appendResp == nil {
			return "", docID, errors.New("empty Feishu Docx append response")
		}
		if !appendResp.Success() {
			return "", docID, fmt.Errorf("Feishu Docx append failed: code=%d msg=%s", appendResp.Code, appendResp.Msg)
		}
	}
	return r.documentURL(docID), docID, nil
}

func (r *Runner) convertMarkdownToDocxBlocks(ctx context.Context, markdown string) ([]string, []*larkdocx.Block, error) {
	markdown = strings.TrimSpace(markdown)
	if markdown == "" {
		return nil, nil, nil
	}

	convertReq := larkdocx.NewConvertDocumentReqBuilder().
		Body(larkdocx.NewConvertDocumentReqBodyBuilder().
			ContentType(larkdocx.ContentTypeMarkdown).
			Content(markdown).
			Build()).
		Build()
	convertResp, err := r.client.Docx.V1.Document.Convert(ctx, convertReq)
	if err != nil {
		return nil, nil, err
	}
	if convertResp == nil {
		return nil, nil, errors.New("empty Feishu Docx convert response")
	}
	if !convertResp.Success() {
		return nil, nil, fmt.Errorf("Feishu Docx convert failed: code=%d msg=%s", convertResp.Code, convertResp.Msg)
	}
	if convertResp.Data == nil {
		return nil, nil, errors.New("Feishu Docx convert response missing data")
	}
	if len(convertResp.Data.FirstLevelBlockIds) == 0 || len(convertResp.Data.Blocks) == 0 {
		return nil, nil, nil
	}
	return convertResp.Data.FirstLevelBlockIds, convertResp.Data.Blocks, nil
}

func renderDocument(plan domain.Plan, instruction string) string {
	var b strings.Builder
	b.WriteString("# " + fallbackText(plan.DocTitle, "任务文档") + "\n\n")
	b.WriteString("_由 IM-based Intelligent Assistant 自动生成_\n\n")
	b.WriteString("## 意图分析\n\n")
	if plan.PlannerSource != "" {
		b.WriteString("- 规划来源：" + plan.PlannerSource + "\n")
	}
	if plan.PlannerError != "" {
		b.WriteString("- LLM 规划失败原因：" + plan.PlannerError + "\n")
	}
	b.WriteString("- 目标：" + fallbackText(plan.Analysis.Objective, "未明确") + "\n")
	b.WriteString("- 受众：" + fallbackText(plan.Analysis.Audience, "未明确") + "\n")
	if len(plan.Analysis.Deliverables) > 0 {
		b.WriteString("- 交付物：" + strings.Join(plan.Analysis.Deliverables, "、") + "\n")
	}
	if plan.Analysis.ContextNeeded {
		b.WriteString("- 上下文：需要结合群聊或对话记录进一步核对\n")
	}
	b.WriteString("- 原始需求：" + instruction + "\n\n")

	b.WriteString("## 执行计划\n\n")
	for _, step := range plan.Steps {
		b.WriteString(fmt.Sprintf("- `%s` %s：%s\n", step.ID, step.Tool, step.Description))
	}
	b.WriteString("\n")

	for _, section := range plan.DocumentSections {
		b.WriteString("## " + section.Heading + "\n\n")
		for _, bullet := range section.Bullets {
			b.WriteString("- " + bullet + "\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func renderSlidev(plan domain.Plan) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("theme: seriph\n")
	b.WriteString("title: " + escapeYAML(fallbackText(plan.SlideTitle, "任务演示稿")) + "\n")
	b.WriteString("class: text-center\n")
	b.WriteString("---\n\n")
	b.WriteString("# " + fallbackText(plan.SlideTitle, "任务演示稿") + "\n\n")
	b.WriteString(plan.Summary + "\n\n")

	for _, slide := range plan.Slides {
		b.WriteString("---\n\n")
		b.WriteString("# " + slide.Title + "\n\n")
		for _, bullet := range slide.Bullets {
			b.WriteString("- " + bullet + "\n")
		}
		b.WriteString("\n")
		if slide.SpeakerNote != "" {
			b.WriteString("<!--\n" + slide.SpeakerNote + "\n-->\n\n")
		}
	}
	return b.String()
}

func renderSpeakerNotes(plan domain.Plan) string {
	var b strings.Builder
	b.WriteString("# " + fallbackText(plan.SlideTitle, "任务演示稿") + " - 演讲稿\n\n")
	for i, slide := range plan.Slides {
		b.WriteString(fmt.Sprintf("## 第 %d 页：%s\n\n", i+1, slide.Title))
		if slide.SpeakerNote != "" {
			b.WriteString(slide.SpeakerNote + "\n\n")
		}
	}
	return b.String()
}

func normalizeMessages(data *larkim.ListMessageRespData, threadID string) []string {
	if data == nil {
		return nil
	}
	items := append([]*larkim.Message(nil), data.Items...)
	if threadID != "" {
		threadItems := make([]*larkim.Message, 0, len(items))
		for _, item := range items {
			if item == nil {
				continue
			}
			if stringValue(item.ThreadId) == threadID || stringValue(item.RootId) == threadID {
				threadItems = append(threadItems, item)
			}
		}
		if len(threadItems) > 0 {
			items = threadItems
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		return stringValue(items[i].CreateTime) < stringValue(items[j].CreateTime)
	})

	out := make([]string, 0, len(items))
	for _, item := range items {
		if item == nil {
			continue
		}
		sender := "unknown"
		if item.Sender != nil && item.Sender.Id != nil {
			sender = *item.Sender.Id
		}
		msgType := stringValue(item.MsgType)
		content := ""
		if item.Body != nil {
			content = messageContentText(msgType, stringValue(item.Body.Content))
		}
		if strings.TrimSpace(content) == "" {
			content = "[" + fallbackText(msgType, "message") + "]"
		}
		out = append(out, fmt.Sprintf("%s %s: %s", formatMessageTime(stringValue(item.CreateTime)), sender, content))
	}
	return out
}

func messageContentText(msgType, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var payload any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return raw
	}
	switch msgType {
	case "text":
		if m, ok := payload.(map[string]any); ok {
			if text, ok := m["text"].(string); ok {
				return strings.TrimSpace(text)
			}
		}
	}
	return strings.TrimSpace(collectJSONText(payload))
}

func collectJSONText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := collectJSONText(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, " ")
	case map[string]any:
		keys := []string{"text", "content", "title", "name"}
		parts := make([]string, 0, len(typed))
		for _, key := range keys {
			if text := collectJSONText(typed[key]); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, " ")
	default:
		return ""
	}
}

func limitFromStep(step domain.PlanStep, defaultLimit int) int {
	limit := defaultLimit
	if step.Args != nil {
		if value, ok := step.Args["limit"]; ok {
			switch typed := value.(type) {
			case float64:
				limit = int(typed)
			case int:
				limit = typed
			case string:
				if parsed, err := strconv.Atoi(typed); err == nil {
					limit = parsed
				}
			}
		}
	}
	if limit < 1 {
		return 1
	}
	if limit > 50 {
		return 50
	}
	return limit
}

func formatMessageTime(ms string) string {
	if ms == "" {
		return ""
	}
	value, err := strconv.ParseInt(ms, 10, 64)
	if err != nil {
		return ms
	}
	return time.UnixMilli(value).Format("2006-01-02 15:04")
}

func (r *Runner) documentURL(docID string) string {
	if r.config.FeishuDocBaseURL == "" {
		return ""
	}
	return r.config.FeishuDocBaseURL + "/docx/" + docID
}

func failed(step string, err error) Result {
	return Result{
		StepName:     step,
		ErrorMessage: err.Error(),
		Retryable:    true,
	}
}

func artifactID() string {
	return fmt.Sprintf("%d_%s", time.Now().Unix(), uuid.NewString()[:8])
}

func escapeYAML(value string) string {
	return strings.ReplaceAll(value, ":", "：")
}

func fallbackText(value, defaultValue string) string {
	if strings.TrimSpace(value) == "" {
		return defaultValue
	}
	return value
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
