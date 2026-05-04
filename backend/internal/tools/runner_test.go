package tools

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"

	"agentpilot/backend/internal/domain"
	larkdocx "github.com/larksuite/oapi-sdk-go/v3/service/docx/v1"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestCreateDocAndSlidesWriteArtifacts(t *testing.T) {
	t.Parallel()

	runner := NewRunner(Config{ArtifactDir: t.TempDir()})
	plan := domain.Plan{
		Summary:    "生成测试产物",
		DocTitle:   "测试文档",
		SlideTitle: "测试演示稿",
		Analysis: domain.IntentAnalysis{
			Objective:    "验证真实产物生成",
			Audience:     "测试人员",
			Deliverables: []string{"方案文档", "演示稿"},
		},
		Steps: []domain.PlanStep{{ID: "s1", Tool: "intent.analyze", Description: "分析"}},
		DocumentSections: []domain.DocumentSection{
			{Heading: "背景", Bullets: []string{"不是占位链接"}},
		},
		Slides: []domain.Slide{
			{Title: "首页", Bullets: []string{"真实 PPT Markdown"}},
		},
	}

	doc := runner.CreateDoc(context.Background(), plan, "生成文档和演示稿", Result{}, "")
	if !doc.Success {
		t.Fatalf("create doc failed: %s", doc.ErrorMessage)
	}
	if !strings.HasPrefix(doc.ArtifactURL, "/artifacts/") {
		t.Fatalf("unexpected doc url: %s", doc.ArtifactURL)
	}
	assertFileContains(t, doc.ArtifactPath, "## 背景")
	assertFileNotContains(t, doc.ArtifactPath, "## 意图分析")
	assertFileNotContains(t, doc.ArtifactPath, "## 执行计划")

	slides := runner.CreateSlides(context.Background(), plan, "")
	if !slides.Success {
		t.Fatalf("create slides failed: %s", slides.ErrorMessage)
	}
	if !strings.HasPrefix(slides.ArtifactURL, "/artifacts/") {
		t.Fatalf("unexpected slides url: %s", slides.ArtifactURL)
	}
	if !strings.HasSuffix(slides.ArtifactURL, ".pptx") {
		t.Fatalf("expected pptx artifact url, got %s", slides.ArtifactURL)
	}
	assertFileContains(t, slides.ArtifactPath, "# 测试演示稿")
	pptxPath := slides.Data["pptx_path"]
	if pptxPath == "" {
		t.Fatal("expected pptx path")
	}
	assertFileExists(t, pptxPath)
}

func TestCreateDocUsesFetchedChatMessages(t *testing.T) {
	t.Parallel()

	runner := NewRunner(Config{ArtifactDir: t.TempDir()})
	plan := domain.Plan{DocTitle: "群聊消息总结"}
	contextResult := Result{
		Success:        true,
		StepName:       "im.fetch_thread",
		PayloadSummary: "已读取飞书会话最近 3 条消息。",
		Data: map[string]string{
			"messages": strings.Join([]string{
				"2026-04-28 09:00 user_a: 今天需要确认方案边界",
				"2026-04-28 09:05 user_b: 我负责整理待办，明天完成",
				"2026-04-28 09:10 user_a: /assistant 总结下聊天消息，生成文档",
			}, "\n"),
		},
	}

	doc := runner.CreateDoc(context.Background(), plan, "总结下聊天消息，生成文档", contextResult, "")
	if !doc.Success {
		t.Fatalf("create doc failed: %s", doc.ErrorMessage)
	}

	assertFileContains(t, doc.ArtifactPath, "## 摘要")
	assertFileContains(t, doc.ArtifactPath, "今天需要确认方案边界")
	assertFileContains(t, doc.ArtifactPath, "我负责整理待办")
	assertFileContains(t, doc.ArtifactPath, "## 原始消息摘录")
	assertFileNotContains(t, doc.ArtifactPath, "## 意图分析")
	assertFileNotContains(t, doc.ArtifactPath, "## 执行计划")
}

func TestNormalizeMessagesSkipsBotAuthoredMessages(t *testing.T) {
	t.Parallel()

	runner := NewRunner(Config{FeishuAppID: "cli_bot"})
	messages := runner.normalizeMessages(&larkim.ListMessageRespData{
		Items: []*larkim.Message{
			{
				MsgType:    stringPtr("text"),
				CreateTime: stringPtr("1000"),
				Sender:     &larkim.Sender{Id: stringPtr("ou_user"), IdType: stringPtr("open_id"), SenderType: stringPtr("user")},
				Body:       &larkim.MessageBody{Content: stringPtr(`{"text":"用户消息"}`)},
			},
			{
				MsgType:    stringPtr("text"),
				CreateTime: stringPtr("2000"),
				Sender:     &larkim.Sender{Id: stringPtr("cli_bot"), IdType: stringPtr("app_id"), SenderType: stringPtr("app")},
				Body:       &larkim.MessageBody{Content: stringPtr(`{"text":"Assistant任务已启动：task-1"}`)},
			},
		},
	}, "")

	if len(messages) != 1 {
		t.Fatalf("expected one user message, got %#v", messages)
	}
	if strings.Contains(messages[0], "Assistant任务已启动") || !strings.Contains(messages[0], "用户消息") {
		t.Fatalf("unexpected normalized messages: %#v", messages)
	}
}

func TestCreateDocUsesGeneratedMarkdown(t *testing.T) {
	t.Parallel()

	runner := NewRunner(Config{ArtifactDir: t.TempDir()})
	plan := domain.Plan{
		DocTitle: "Generated Doc",
		DocumentSections: []domain.DocumentSection{
			{Heading: "Fallback Section", Bullets: []string{"should not be used"}},
		},
	}

	doc := runner.CreateDoc(context.Background(), plan, "write a doc", Result{}, "# Agent Doc\n\n## Decision\n\nUse the agent content.")
	if !doc.Success {
		t.Fatalf("create doc failed: %s", doc.ErrorMessage)
	}

	assertFileContains(t, doc.ArtifactPath, "## Decision")
	assertFileContains(t, doc.ArtifactPath, "Use the agent content.")
	assertFileNotContains(t, doc.ArtifactPath, "Fallback Section")
	if got := doc.Data["content_source"]; got != "agent_markdown" {
		t.Fatalf("expected agent content source, got %q", got)
	}
}

func TestCreateSlidesUsesGeneratedMarkdownAndWritesPPTX(t *testing.T) {
	t.Parallel()

	runner := NewRunner(Config{ArtifactDir: t.TempDir()})
	plan := domain.Plan{SlideTitle: "Generated Slides"}

	slides := runner.CreateSlides(context.Background(), plan, "# Agent Slide\n\n## Summary\n\n- One\n- Two")
	if !slides.Success {
		t.Fatalf("create slides failed: %s", slides.ErrorMessage)
	}
	assertFileContains(t, slides.ArtifactPath, "# Agent Slide")
	if got := slides.Data["content_source"]; got != "agent_markdown" {
		t.Fatalf("expected agent slide source, got %q", got)
	}
	if got := slides.Data["source"]; got != "genppt_pptx" {
		t.Fatalf("expected genppt source, got %q", got)
	}
	if !strings.HasSuffix(slides.ArtifactURL, ".pptx") {
		t.Fatalf("expected pptx artifact url, got %s", slides.ArtifactURL)
	}
	assertFileExists(t, slides.Data["pptx_path"])
}

func TestValidateConvertedBlocks(t *testing.T) {
	t.Parallel()

	blockID := "tmp_block_1"
	if err := validateConvertedBlocks([]string{blockID}, []*larkdocx.Block{{BlockId: &blockID}}); err != nil {
		t.Fatalf("expected converted blocks to validate: %v", err)
	}

	if err := validateConvertedBlocks([]string{"missing"}, []*larkdocx.Block{{BlockId: &blockID}}); err == nil {
		t.Fatal("expected missing first-level block to fail validation")
	}
}

func TestSplitConvertedBlocksBatchesAboveLimit(t *testing.T) {
	t.Parallel()

	firstLevel := make([]string, 0, 1001)
	descendants := make([]*larkdocx.Block, 0, 1002)
	for i := 0; i < 1001; i++ {
		id := "root_" + strconv.Itoa(i)
		firstLevel = append(firstLevel, id)
		block := &larkdocx.Block{BlockId: &id}
		if i == 0 {
			childID := "child_0"
			block.Children = []string{childID}
			descendants = append(descendants, &larkdocx.Block{BlockId: &childID})
		}
		descendants = append(descendants, block)
	}

	chunks, err := splitConvertedBlocks(firstLevel, descendants, maxFeishuDocxDescendantChildren)
	if err != nil {
		t.Fatalf("split converted blocks: %v", err)
	}
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	if got := len(chunks[0].firstLevelBlockIDs); got != maxFeishuDocxDescendantChildren {
		t.Fatalf("expected first chunk to have %d first-level blocks, got %d", maxFeishuDocxDescendantChildren, got)
	}
	if got := len(chunks[1].firstLevelBlockIDs); got != 1 {
		t.Fatalf("expected second chunk to have 1 first-level block, got %d", got)
	}
	if !containsBlockID(chunks[0].descendants, "child_0") {
		t.Fatal("expected first chunk to include child subtree")
	}
	if containsBlockID(chunks[1].descendants, "child_0") {
		t.Fatal("did not expect second chunk to include first chunk child subtree")
	}
}

func containsBlockID(blocks []*larkdocx.Block, id string) bool {
	for _, block := range blocks {
		if block != nil && block.BlockId != nil && *block.BlockId == id {
			return true
		}
	}
	return false
}

func assertFileContains(t *testing.T, path, want string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !strings.Contains(string(data), want) {
		t.Fatalf("expected %s to contain %q", path, want)
	}
}

func assertFileNotContains(t *testing.T, path, want string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if strings.Contains(string(data), want) {
		t.Fatalf("expected %s not to contain %q", path, want)
	}
}

func assertFileExists(t *testing.T, path string) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
	if info.IsDir() {
		t.Fatalf("expected %s to be a file", path)
	}
	if info.Size() == 0 {
		t.Fatalf("expected %s to be non-empty", path)
	}
}

func stringPtr(value string) *string {
	return &value
}
