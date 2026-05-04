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

	"github.com/CoolBanHub/genppt"
	"github.com/google/uuid"
	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkdocx "github.com/larksuite/oapi-sdk-go/v3/service/docx/v1"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	"agentpilot/backend/internal/domain"
)

const defaultArtifactDir = "data/pilot_artifacts"
const maxFeishuDocxDescendantChildren = 1000

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

	messages := r.normalizeMessages(resp.Data, task.ThreadID)
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

func (r *Runner) CreateDoc(ctx context.Context, plan domain.Plan, instruction string, contextResult Result, generatedMarkdown string) Result {
	if err := os.MkdirAll(r.config.ArtifactDir, 0755); err != nil {
		return failed("doc.generate", err)
	}

	fileName := fmt.Sprintf("doc_%s.md", artifactID())
	path := filepath.Join(r.config.ArtifactDir, fileName)
	content := strings.TrimSpace(generatedMarkdown)
	contentSource := "agent_markdown"
	if content == "" {
		content = renderDocument(plan, instruction, contextResult)
		contentSource = "planner_fallback"
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return failed("doc.generate", err)
	}

	result := Result{
		Success:        true,
		StepName:       "doc.generate",
		PayloadSummary: fmt.Sprintf("已生成结构化 Markdown 文档：%s", path),
		ArtifactURL:    "/artifacts/" + fileName,
		ArtifactPath:   path,
		Data:           map[string]string{"source": "local_markdown", "content_source": contentSource},
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

func (r *Runner) CreateSlides(ctx context.Context, plan domain.Plan, slideMarkdown string) Result {
	if err := os.MkdirAll(r.config.ArtifactDir, 0755); err != nil {
		return failed("slide.generate", err)
	}

	slideID := "slide_" + artifactID()
	markdownName := slideID + ".md"
	markdownPath := filepath.Join(r.config.ArtifactDir, markdownName)
	pptxName := slideID + ".pptx"
	pptxPath := filepath.Join(r.config.ArtifactDir, pptxName)
	content := strings.TrimSpace(slideMarkdown)
	contentSource := "agent_markdown"
	if content == "" {
		content = renderSlideMarkdown(plan)
		contentSource = "planner_fallback"
	}
	if err := os.WriteFile(markdownPath, []byte(content), 0644); err != nil {
		return failed("slide.generate", err)
	}
	if err := writePPTXFromMarkdown(content, pptxPath); err != nil {
		return failed("slide.generate", err)
	}

	select {
	case <-ctx.Done():
		return failed("slide.generate", ctx.Err())
	default:
	}

	data := map[string]string{
		"source":             "genppt_pptx",
		"content_source":     contentSource,
		"markdown_artifact":  "/artifacts/" + markdownName,
		"markdown_path":      markdownPath,
		"pptx_artifact":      "/artifacts/" + pptxName,
		"pptx_path":          pptxPath,
		"conversion_library": "github.com/CoolBanHub/genppt",
	}
	if r.config.EnableFeishuTools {
		data["feishu_slides"] = "not_created"
		data["feishu_slides_reason"] = "github.com/larksuite/oapi-sdk-go/v3 v3.6.1 does not expose a direct Slides create service"
	}
	return Result{
		Success:        true,
		StepName:       "slide.generate",
		PayloadSummary: fmt.Sprintf("已通过 genppt 生成 PPTX：%s", pptxPath),
		ArtifactURL:    "/artifacts/" + pptxName,
		ArtifactPath:   markdownPath,
		Data:           data,
	}
}

func (r *Runner) UpdateDoc(ctx context.Context, task domain.Task, plan domain.Plan, instruction string, generatedMarkdown string) Result {
	if err := os.MkdirAll(r.config.ArtifactDir, 0755); err != nil {
		return failed("doc.update", err)
	}

	fileName := fmt.Sprintf("doc_revision_%s.md", artifactID())
	path := filepath.Join(r.config.ArtifactDir, fileName)
	content := strings.TrimSpace(generatedMarkdown)
	contentSource := "agent_markdown"
	if content == "" {
		content = appendRevisionToExisting(task.DocArtifactPath, instruction)
		if content == "" {
			content = renderDocument(plan, instruction, Result{})
		}
		contentSource = "planner_fallback"
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return failed("doc.update", err)
	}

	result := Result{
		Success:        true,
		StepName:       "doc.update",
		PayloadSummary: fmt.Sprintf("Generated revised Markdown document: %s", path),
		ArtifactURL:    task.DocURL,
		ArtifactPath:   path,
		Data: map[string]string{
			"source":         "local_markdown",
			"content_source": contentSource,
			"local_path":     path,
		},
	}
	if result.ArtifactURL == "" {
		result.ArtifactURL = "/artifacts/" + fileName
	}

	if !r.config.EnableFeishuTools {
		return result
	}
	docID := strings.TrimSpace(task.DocID)
	if docID == "" {
		url, createdDocID, err := r.createFeishuDoc(ctx, fallbackTitle(plan.DocTitle, task.Title), content)
		if err != nil {
			result.Data["feishu_error"] = err.Error()
			return result
		}
		result.Data["source"] = "feishu_docx"
		result.Data["feishu_document_id"] = createdDocID
		if url != "" {
			result.ArtifactURL = url
		}
		result.PayloadSummary = "Created Feishu Docx for revised document: " + result.ArtifactURL
		return result
	}
	if err := r.replaceFeishuDoc(ctx, docID, content); err != nil {
		result.Data["feishu_error"] = err.Error()
		return result
	}
	result.Data["source"] = "feishu_docx"
	result.Data["feishu_document_id"] = docID
	if result.ArtifactURL == "" || strings.HasPrefix(result.ArtifactURL, "/artifacts/") {
		result.ArtifactURL = r.documentURL(docID)
	}
	result.PayloadSummary = "Updated Feishu Docx in place: " + result.ArtifactURL
	return result
}

func (r *Runner) RegenerateSlides(ctx context.Context, task domain.Task, plan domain.Plan, slideMarkdown string) Result {
	if strings.TrimSpace(slideMarkdown) == "" {
		slideMarkdown = appendRevisionToExisting(task.SlidesArtifactPath, plan.Summary)
	}
	result := r.CreateSlides(ctx, plan, slideMarkdown)
	result.StepName = "slide.regenerate"
	if result.Success {
		result.PayloadSummary = "Regenerated PPTX presentation: " + result.Data["pptx_path"]
	}
	return result
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
		"slidesPptxPath": slidesResult.Data["pptx_path"],
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
		if err := r.appendFeishuDocBlocks(ctx, docID, firstLevelBlockIDs, descendants); err != nil {
			return "", docID, err
		}
	}
	return r.documentURL(docID), docID, nil
}

func (r *Runner) appendFeishuDocBlocks(ctx context.Context, docID string, firstLevelBlockIDs []string, descendants []*larkdocx.Block) error {
	if len(firstLevelBlockIDs) == 0 || len(descendants) == 0 {
		return nil
	}
	if err := validateConvertedBlocks(firstLevelBlockIDs, descendants); err != nil {
		return err
	}

	chunks, err := splitConvertedBlocks(firstLevelBlockIDs, descendants, maxFeishuDocxDescendantChildren)
	if err != nil {
		return err
	}
	for _, chunk := range chunks {
		appendReq := larkdocx.NewCreateDocumentBlockDescendantReqBuilder().
			DocumentId(docID).
			BlockId(docID).
			DocumentRevisionId(-1).
			ClientToken(uuid.NewString()).
			Body(larkdocx.NewCreateDocumentBlockDescendantReqBodyBuilder().
				ChildrenId(chunk.firstLevelBlockIDs).
				Descendants(chunk.descendants).
				Index(-1).
				Build()).
			Build()
		appendResp, err := r.client.Docx.V1.DocumentBlockDescendant.Create(ctx, appendReq)
		if err != nil {
			return err
		}
		if appendResp == nil {
			return errors.New("empty Feishu Docx append response")
		}
		if !appendResp.Success() {
			return fmt.Errorf("Feishu Docx append failed: code=%d msg=%s request_id=%s first_level_blocks=%d descendants=%d", appendResp.Code, appendResp.Msg, appendResp.RequestId(), len(chunk.firstLevelBlockIDs), len(chunk.descendants))
		}
	}
	return nil
}

func (r *Runner) replaceFeishuDoc(ctx context.Context, docID string, markdown string) error {
	if r.client == nil {
		return errors.New("Feishu SDK client is not configured")
	}
	childIDs, err := r.listRootBlockIDs(ctx, docID)
	if err != nil {
		return err
	}
	if len(childIDs) > 0 {
		deleteReq := larkdocx.NewBatchDeleteDocumentBlockChildrenReqBuilder().
			DocumentId(docID).
			BlockId(docID).
			DocumentRevisionId(-1).
			Body(larkdocx.NewBatchDeleteDocumentBlockChildrenReqBodyBuilder().
				StartIndex(0).
				EndIndex(len(childIDs)).
				Build()).
			Build()
		deleteResp, err := r.client.Docx.V1.DocumentBlockChildren.BatchDelete(ctx, deleteReq)
		if err != nil {
			return err
		}
		if deleteResp == nil {
			return errors.New("empty Feishu Docx delete response")
		}
		if !deleteResp.Success() {
			return fmt.Errorf("Feishu Docx delete failed: code=%d msg=%s request_id=%s", deleteResp.Code, deleteResp.Msg, deleteResp.RequestId())
		}
	}

	firstLevelBlockIDs, descendants, err := r.convertMarkdownToDocxBlocks(ctx, markdown)
	if err != nil {
		return err
	}
	return r.appendFeishuDocBlocks(ctx, docID, firstLevelBlockIDs, descendants)
}

func (r *Runner) listRootBlockIDs(ctx context.Context, docID string) ([]string, error) {
	req := larkdocx.NewGetDocumentBlockChildrenReqBuilder().
		DocumentId(docID).
		BlockId(docID).
		DocumentRevisionId(-1).
		PageSize(500).
		Build()
	iterator, err := r.client.Docx.V1.DocumentBlockChildren.GetByIterator(ctx, req)
	if err != nil {
		return nil, err
	}
	var ids []string
	for {
		ok, block, err := iterator.Next()
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		if block != nil && block.BlockId != nil && *block.BlockId != "" {
			ids = append(ids, *block.BlockId)
		}
	}
	return ids, nil
}

type feishuDocBlockChunk struct {
	firstLevelBlockIDs []string
	descendants        []*larkdocx.Block
}

func splitConvertedBlocks(firstLevelBlockIDs []string, descendants []*larkdocx.Block, maxFirstLevel int) ([]feishuDocBlockChunk, error) {
	if maxFirstLevel <= 0 {
		maxFirstLevel = maxFeishuDocxDescendantChildren
	}
	blockByID := make(map[string]*larkdocx.Block, len(descendants))
	for _, block := range descendants {
		sanitizeConvertedBlock(block)
		if block != nil && block.BlockId != nil {
			blockByID[*block.BlockId] = block
		}
	}

	chunks := make([]feishuDocBlockChunk, 0, (len(firstLevelBlockIDs)+maxFirstLevel-1)/maxFirstLevel)
	for start := 0; start < len(firstLevelBlockIDs); start += maxFirstLevel {
		end := start + maxFirstLevel
		if end > len(firstLevelBlockIDs) {
			end = len(firstLevelBlockIDs)
		}
		roots := append([]string(nil), firstLevelBlockIDs[start:end]...)
		wanted := map[string]struct{}{}
		for _, root := range roots {
			collectBlockSubtree(root, blockByID, wanted)
		}
		chunkDescendants := make([]*larkdocx.Block, 0, len(wanted))
		for _, block := range descendants {
			if block == nil || block.BlockId == nil {
				continue
			}
			if _, ok := wanted[*block.BlockId]; ok {
				chunkDescendants = append(chunkDescendants, block)
			}
		}
		if err := validateConvertedBlocks(roots, chunkDescendants); err != nil {
			return nil, err
		}
		chunks = append(chunks, feishuDocBlockChunk{firstLevelBlockIDs: roots, descendants: chunkDescendants})
	}
	return chunks, nil
}

func collectBlockSubtree(blockID string, blockByID map[string]*larkdocx.Block, wanted map[string]struct{}) {
	if _, ok := wanted[blockID]; ok {
		return
	}
	block := blockByID[blockID]
	if block == nil {
		return
	}
	wanted[blockID] = struct{}{}
	for _, childID := range block.Children {
		collectBlockSubtree(childID, blockByID, wanted)
	}
}

func sanitizeConvertedBlock(block *larkdocx.Block) {
	if block == nil {
		return
	}
	block.CommentIds = nil
	if block.Table != nil && block.Table.Property != nil {
		block.Table.Property.MergeInfo = nil
	}
}

func validateConvertedBlocks(firstLevelBlockIDs []string, descendants []*larkdocx.Block) error {
	blockIDs := make(map[string]struct{}, len(descendants))
	for _, block := range descendants {
		if block == nil || block.BlockId == nil || strings.TrimSpace(*block.BlockId) == "" {
			return errors.New("Feishu Docx convert returned a descendant block without block_id")
		}
		blockIDs[*block.BlockId] = struct{}{}
	}
	for _, id := range firstLevelBlockIDs {
		if strings.TrimSpace(id) == "" {
			return errors.New("Feishu Docx convert returned an empty first-level block id")
		}
		if _, ok := blockIDs[id]; !ok {
			return fmt.Errorf("Feishu Docx convert returned first-level block id %q without matching descendant", id)
		}
	}
	return nil
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

func renderDocument(plan domain.Plan, instruction string, contextResult Result) string {
	var b strings.Builder
	b.WriteString("# " + fallbackText(plan.DocTitle, "聊天消息总结") + "\n\n")
	b.WriteString("_由 IM-based Intelligent Assistant 自动生成_\n\n")

	messages := chatMessagesFromResult(contextResult)
	if len(messages) > 0 {
		writeChatSummary(&b, messages, instruction)
		return b.String()
	}

	if reason := missingContextReason(contextResult); reason != "" {
		b.WriteString("## 摘要\n\n")
		b.WriteString("- 未获取到可用于总结的聊天消息，当前文档只保留用户需求和待补充信息。\n")
		b.WriteString("- 原因：" + reason + "\n")
		b.WriteString("- 用户需求：" + instruction + "\n\n")
	}

	for _, section := range plan.DocumentSections {
		b.WriteString("## " + section.Heading + "\n\n")
		for _, bullet := range section.Bullets {
			b.WriteString("- " + bullet + "\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

type chatMessage struct {
	Time    string
	Sender  string
	Content string
}

func chatMessagesFromResult(result Result) []chatMessage {
	if result.Data == nil {
		return nil
	}
	raw := strings.TrimSpace(result.Data["messages"])
	if raw == "" {
		return nil
	}
	lines := strings.Split(raw, "\n")
	messages := make([]chatMessage, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		messages = append(messages, parseChatMessageLine(line))
	}
	return messages
}

func parseChatMessageLine(line string) chatMessage {
	msg := chatMessage{Content: line}
	const layout = "2006-01-02 15:04"
	if len(line) > len(layout) {
		timePart := line[:len(layout)]
		rest := strings.TrimSpace(line[len(layout):])
		if _, err := time.Parse(layout, timePart); err == nil {
			msg.Time = timePart
			msg.Content = rest
		}
	}
	if idx := strings.Index(msg.Content, ": "); idx > 0 {
		msg.Sender = strings.TrimSpace(msg.Content[:idx])
		msg.Content = strings.TrimSpace(msg.Content[idx+2:])
	}
	return msg
}

func writeChatSummary(b *strings.Builder, messages []chatMessage, instruction string) {
	usable := make([]chatMessage, 0, len(messages))
	for _, msg := range messages {
		if isAssistantCommand(msg.Content) {
			continue
		}
		usable = append(usable, msg)
	}

	b.WriteString("## 摘要\n\n")
	b.WriteString(fmt.Sprintf("- 已读取聊天消息 %d 条。", len(messages)))
	if len(usable) != len(messages) {
		b.WriteString(fmt.Sprintf("其中 %d 条为触发助手的命令，已从内容摘要中排除。", len(messages)-len(usable)))
	}
	b.WriteString("\n")
	if len(usable) == 0 {
		b.WriteString("- 未读取到除助手触发命令之外的可总结聊天内容。\n")
		b.WriteString("- 用户需求：" + instruction + "\n\n")
		writeRawMessages(b, messages)
		return
	}

	if first, last := usable[0], usable[len(usable)-1]; first.Time != "" || last.Time != "" {
		b.WriteString("- 时间范围：" + fallbackText(first.Time, "未知") + " 至 " + fallbackText(last.Time, "未知") + "\n")
	}
	participants := messageParticipants(usable)
	if len(participants) > 0 {
		b.WriteString("- 参与者：" + strings.Join(participants, "、") + "\n")
	}
	b.WriteString("\n")

	b.WriteString("## 关键内容\n\n")
	for _, msg := range usable {
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		prefix := ""
		if msg.Sender != "" {
			prefix = msg.Sender + "："
		}
		b.WriteString("- " + prefix + truncateText(content, 220) + "\n")
	}
	b.WriteString("\n")

	todos := extractActionItems(usable)
	if len(todos) > 0 {
		b.WriteString("## 待办与结论\n\n")
		for _, item := range todos {
			b.WriteString("- " + item + "\n")
		}
		b.WriteString("\n")
	}

	writeRawMessages(b, messages)
}

func writeRawMessages(b *strings.Builder, messages []chatMessage) {
	b.WriteString("## 原始消息摘录\n\n")
	for _, msg := range messages {
		parts := make([]string, 0, 2)
		if msg.Time != "" {
			parts = append(parts, msg.Time)
		}
		if msg.Sender != "" {
			parts = append(parts, msg.Sender)
		}
		prefix := ""
		if len(parts) > 0 {
			prefix = strings.Join(parts, " ") + "："
		}
		b.WriteString("- " + prefix + msg.Content + "\n")
	}
	b.WriteString("\n")
}

func messageParticipants(messages []chatMessage) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, msg := range messages {
		if msg.Sender == "" {
			continue
		}
		if _, ok := seen[msg.Sender]; ok {
			continue
		}
		seen[msg.Sender] = struct{}{}
		out = append(out, msg.Sender)
	}
	sort.Strings(out)
	return out
}

func extractActionItems(messages []chatMessage) []string {
	keywords := []string{"待办", "todo", "TODO", "负责", "跟进", "确认", "截止", "明天", "今天", "下周", "完成"}
	out := make([]string, 0)
	for _, msg := range messages {
		for _, keyword := range keywords {
			if strings.Contains(msg.Content, keyword) {
				out = append(out, truncateText(msg.Content, 220))
				break
			}
		}
	}
	return out
}

func isAssistantCommand(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), "/assistant")
}

func missingContextReason(result Result) string {
	if result.PayloadSummary != "" {
		return result.PayloadSummary
	}
	if result.Data != nil {
		if source := result.Data["source"]; source != "" {
			return source
		}
	}
	return ""
}

func truncateText(text string, maxRunes int) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= maxRunes {
		return string(runes)
	}
	return string(runes[:maxRunes]) + "..."
}

func renderSlideMarkdown(plan domain.Plan) string {
	var b strings.Builder
	b.WriteString("# " + fallbackText(plan.SlideTitle, "任务演示稿") + "\n\n")
	b.WriteString(plan.Summary + "\n\n")

	for _, slide := range plan.Slides {
		b.WriteString("# " + slide.Title + "\n\n")
		for _, bullet := range slide.Bullets {
			b.WriteString("- " + bullet + "\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func writePPTXFromMarkdown(markdown, path string) error {
	opts := genppt.DefaultMarkdownOptions()
	opts.TitleFontSize = 40
	opts.HeadingFontSize = 30
	opts.BodyFontSize = 18
	opts.TitleColor = "#1E3A5F"
	opts.HeadingColor = "#1E3A5F"
	opts.BodyColor = "#333333"
	pres := genppt.FromMarkdownWithOptions(markdown, opts)
	pres.SetTitle(firstMarkdownTitle(markdown))
	pres.SetAuthor("IM-based Intelligent Assistant")
	return pres.WriteFile(path)
}

func firstMarkdownTitle(markdown string) string {
	for _, line := range strings.Split(markdown, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return "任务演示稿"
}

func (r *Runner) normalizeMessages(data *larkim.ListMessageRespData, threadID string) []string {
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
		if r.isBotAuthoredMessage(item) {
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

func (r *Runner) isBotAuthoredMessage(item *larkim.Message) bool {
	if item == nil || item.Sender == nil {
		return false
	}
	senderType := strings.ToLower(strings.TrimSpace(stringValue(item.Sender.SenderType)))
	if senderType == "app" {
		return true
	}
	idType := strings.ToLower(strings.TrimSpace(stringValue(item.Sender.IdType)))
	if idType == "app_id" {
		return true
	}
	if r != nil && strings.TrimSpace(r.config.FeishuAppID) != "" && stringValue(item.Sender.Id) == r.config.FeishuAppID {
		return true
	}
	return false
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
	baseURL := r.config.FeishuDocBaseURL
	if baseURL == "" {
		baseURL = "https://sample.feishu.cn"
	}
	return strings.TrimRight(baseURL, "/") + "/docx/" + docID
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

func fallbackText(value, defaultValue string) string {
	if strings.TrimSpace(value) == "" {
		return defaultValue
	}
	return value
}

func fallbackTitle(value, defaultValue string) string {
	if strings.TrimSpace(value) == "" {
		return strings.TrimSpace(defaultValue)
	}
	return value
}

func appendRevisionToExisting(path string, instruction string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return ""
	}
	instruction = strings.TrimSpace(instruction)
	if instruction == "" {
		return content
	}
	return content + "\n\n## Revision request\n\n" + instruction + "\n"
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
