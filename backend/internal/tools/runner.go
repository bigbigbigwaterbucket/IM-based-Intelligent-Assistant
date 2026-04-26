package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"agentpilot/backend/internal/domain"
)

const defaultArtifactDir = "data/pilot_artifacts"

type Config struct {
	LarkCLIPath     string
	EnableLarkTools bool
	ArtifactDir     string
}

type Runner struct {
	config Config
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
	return &Runner{config: config}
}

func ArtifactDir() string {
	return defaultArtifactDir
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
		PayloadSummary: fmt.Sprintf("生成结构化 Markdown 文档：%s", path),
		ArtifactURL:    "/artifacts/" + fileName,
		ArtifactPath:   path,
		Data:           map[string]string{"source": "local_markdown"},
	}

	if r.config.EnableLarkTools {
		if url, output, err := r.createFeishuDoc(ctx, plan.DocTitle, content); err == nil && url != "" {
			result.ArtifactURL = url
			result.PayloadSummary = "已创建飞书文档：" + url
			result.Data["source"] = "feishu_doc"
			result.Data["local_path"] = path
		} else if output != "" {
			result.Data["lark_cli_output"] = output
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

	result := Result{
		Success:        true,
		StepName:       "slide.generate",
		PayloadSummary: fmt.Sprintf("生成 Slidev 演示稿：%s", path),
		ArtifactURL:    "/artifacts/" + fileName,
		ArtifactPath:   path,
		Data: map[string]string{
			"source":        "slidev_markdown",
			"speaker_notes": "/artifacts/" + notesName,
		},
	}

	if r.config.EnableLarkTools {
		if url, output, err := r.createFeishuSlides(ctx, plan.SlideTitle, plan.Slides); err == nil && url != "" {
			result.ArtifactURL = url
			result.PayloadSummary = "已创建飞书演示稿：" + url
			result.Data["source"] = "feishu_slides"
			result.Data["local_path"] = path
		} else if output != "" {
			result.Data["lark_cli_output"] = output
		}
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
		PayloadSummary: fmt.Sprintf("汇总产物 manifest：%s", path),
		ArtifactURL:    "/artifacts/" + fileName,
		ArtifactPath:   path,
		Data:           map[string]string{"source": "local_manifest"},
	}
}

func (r *Runner) createFeishuDoc(ctx context.Context, title, markdown string) (string, string, error) {
	command := exec.CommandContext(
		ctx,
		r.config.LarkCLIPath,
		"docs",
		"+create",
		"--api-version",
		"v2",
		"--as",
		"user",
		"--title",
		title,
		"--content",
		markdownToDocxXML(title, markdown),
	)
	output, err := command.CombinedOutput()
	return extractFirstURL(string(output)), string(output), err
}

func (r *Runner) createFeishuSlides(ctx context.Context, title string, slides []domain.Slide) (string, string, error) {
	command := exec.CommandContext(
		ctx,
		r.config.LarkCLIPath,
		"slides",
		"+create",
		"--as",
		"user",
		"--title",
		title,
		"--slides",
		slidesToXML(slides),
	)
	output, err := command.CombinedOutput()
	return extractFirstURL(string(output)), string(output), err
}

func renderDocument(plan domain.Plan, instruction string) string {
	var b strings.Builder
	b.WriteString("# " + plan.DocTitle + "\n\n")
	b.WriteString("_由 IM-based Intelligent Assistant 自动生成_\n\n")
	b.WriteString("## 意图分析\n\n")
	if plan.PlannerSource != "" {
		b.WriteString("- 规划来源：" + plan.PlannerSource + "\n")
	}
	if plan.PlannerError != "" {
		b.WriteString("- LLM 规划失败原因：" + plan.PlannerError + "\n")
	}
	b.WriteString("- 目标：" + plan.Analysis.Objective + "\n")
	b.WriteString("- 受众：" + plan.Analysis.Audience + "\n")
	b.WriteString("- 交付物：" + strings.Join(plan.Analysis.Deliverables, "、") + "\n")
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
	b.WriteString("title: " + escapeYAML(plan.SlideTitle) + "\n")
	b.WriteString("class: text-center\n")
	b.WriteString("---\n\n")
	b.WriteString("# " + plan.SlideTitle + "\n\n")
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
	b.WriteString("# " + plan.SlideTitle + " - 演讲稿\n\n")
	for i, slide := range plan.Slides {
		b.WriteString(fmt.Sprintf("## 第 %d 页：%s\n\n", i+1, slide.Title))
		if slide.SpeakerNote != "" {
			b.WriteString(slide.SpeakerNote + "\n\n")
		}
	}
	return b.String()
}

func markdownToDocxXML(title, markdown string) string {
	var b strings.Builder
	b.WriteString("<title>" + escapeXML(title) + "</title>")
	for _, line := range strings.Split(markdown, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "_") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "# "):
			b.WriteString("<h1>" + escapeXML(strings.TrimPrefix(line, "# ")) + "</h1>")
		case strings.HasPrefix(line, "## "):
			b.WriteString("<h2>" + escapeXML(strings.TrimPrefix(line, "## ")) + "</h2>")
		case strings.HasPrefix(line, "- "):
			b.WriteString("<p>• " + escapeXML(strings.TrimPrefix(line, "- ")) + "</p>")
		default:
			b.WriteString("<p>" + escapeXML(line) + "</p>")
		}
	}
	return b.String()
}

func slidesToXML(slides []domain.Slide) string {
	items := make([]string, 0, len(slides))
	for _, slide := range slides {
		body := strings.Join(slide.Bullets, "\\n")
		items = append(items, fmt.Sprintf(
			`<slide xmlns="http://www.larkoffice.com/sml/2.0"><data><shape type="text" topLeftX="80" topLeftY="80" width="800" height="120"><content textType="title"><p>%s</p></content></shape><shape type="text" topLeftX="80" topLeftY="220" width="800" height="320"><content textType="body"><p>%s</p></content></shape></data></slide>`,
			escapeXML(slide.Title),
			escapeXML(body),
		))
	}
	payload, _ := json.Marshal(items)
	return string(payload)
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

func escapeXML(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;")
	return replacer.Replace(value)
}

func extractFirstURL(output string) string {
	for _, field := range strings.Fields(output) {
		field = strings.Trim(field, "\"',")
		if strings.HasPrefix(field, "http://") || strings.HasPrefix(field, "https://") {
			return field
		}
	}
	return ""
}

func sanitize(value string) string {
	value = regexp.MustCompile(`[^a-zA-Z0-9_\-\p{Han}]+`).ReplaceAllString(value, "-")
	value = strings.Trim(value, "-")
	if value == "" {
		return "artifact"
	}
	return strings.ToLower(value)
}
