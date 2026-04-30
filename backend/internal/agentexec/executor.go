package agentexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"agentpilot/backend/internal/domain"
	"agentpilot/backend/internal/store"
	"agentpilot/backend/internal/tools"
)

type ProgressSink interface {
	StartAgentRun(ctx context.Context, taskID string, plan domain.Plan) (domain.Task, error)
	StartToolStep(ctx context.Context, taskID string, step domain.PlanStep) (domain.Task, error)
	CompleteToolStep(ctx context.Context, taskID string, result tools.Result) (domain.Task, error)
	FailToolStep(ctx context.Context, taskID string, step domain.PlanStep, err error) (domain.Task, error)
	CompleteAgentRun(ctx context.Context, taskID string, plan domain.Plan) (domain.Task, error)
	GetTask(ctx context.Context, taskID string) (domain.Task, error)
}

type Executor interface {
	Execute(ctx context.Context, task domain.Task, plan domain.Plan) (domain.Task, error)
}

type PlanExecutor struct {
	runner  *tools.Runner
	sink    ProgressSink
	history store.HistoryRepository
}

func NewPlanExecutor(runner *tools.Runner, sink ProgressSink, history store.HistoryRepository) *PlanExecutor {
	return &PlanExecutor{
		runner:  runner,
		sink:    sink,
		history: history,
	}
}

func (e *PlanExecutor) Execute(ctx context.Context, task domain.Task, plan domain.Plan) (domain.Task, error) {
	if e.runner == nil {
		return domain.Task{}, errors.New("agent executor requires tools runner")
	}
	if e.sink == nil {
		return domain.Task{}, errors.New("agent executor requires progress sink")
	}

	sessionID := sessionIDForTask(task)
	if err := e.ensureSession(ctx, sessionID, task); err != nil {
		return domain.Task{}, err
	}
	_ = e.appendMessage(ctx, sessionID, "user", task.UserInstruction, "task_input")

	task, err := e.sink.StartAgentRun(ctx, task.TaskID, plan)
	if err != nil {
		return domain.Task{}, err
	}

	state := &executionState{}
	toolSet := newToolSet(e.runner, e.sink, e.history, sessionID, task, plan, state)
	for _, step := range plan.Steps {
		if isLogicalStep(step.Tool) {
			continue
		}
		tl, ok := toolSet.byPlanName[step.Tool]
		if !ok {
			continue
		}
		args, err := json.Marshal(stepToolInput{
			StepID:      step.ID,
			Tool:        step.Tool,
			Description: step.Description,
			Args:        step.Args,
		})
		if err != nil {
			return task, err
		}
		out, err := tl.InvokableRun(ctx, string(args))
		if err != nil {
			_, _ = e.sink.FailToolStep(ctx, task.TaskID, step, err)
			return task, err
		}
		var result tools.Result
		if err := json.Unmarshal([]byte(out), &result); err != nil {
			_, _ = e.sink.FailToolStep(ctx, task.TaskID, step, err)
			return task, err
		}
		if !result.Success {
			err := fmt.Errorf("%s failed: %s", step.Tool, result.ErrorMessage)
			_, _ = e.sink.FailToolStep(ctx, task.TaskID, step, err)
			return task, err
		}
		_ = e.appendMessage(ctx, sessionID, "tool", observationForResult(result), safeToolName(step.Tool))
	}

	task, err = e.sink.CompleteAgentRun(ctx, task.TaskID, plan)
	if err != nil {
		return domain.Task{}, err
	}
	_ = e.appendMessage(ctx, sessionID, "assistant", task.ProgressText, "task_result")
	return task, nil
}

func (e *PlanExecutor) ensureSession(ctx context.Context, sessionID string, task domain.Task) error {
	if e.history == nil {
		return nil
	}
	now := time.Now()
	return e.history.UpsertSession(ctx, domain.Session{
		SessionID: sessionID,
		TaskID:    task.TaskID,
		ChatID:    task.ChatID,
		ThreadID:  task.ThreadID,
		CreatedAt: now,
		UpdatedAt: now,
	})
}

func (e *PlanExecutor) appendMessage(ctx context.Context, sessionID, role, content, metadata string) error {
	if e.history == nil || strings.TrimSpace(content) == "" {
		return nil
	}
	return e.history.AppendMessage(ctx, domain.ConversationMessage{
		MessageID: uuid.NewString(),
		SessionID: sessionID,
		Role:      role,
		Content:   content,
		Metadata:  metadata,
		CreatedAt: time.Now(),
	})
}

func ensureSession(ctx context.Context, history store.HistoryRepository, sessionID string, task domain.Task) error {
	if history == nil {
		return nil
	}
	now := time.Now()
	return history.UpsertSession(ctx, domain.Session{
		SessionID: sessionID,
		TaskID:    task.TaskID,
		ChatID:    task.ChatID,
		ThreadID:  task.ThreadID,
		CreatedAt: now,
		UpdatedAt: now,
	})
}

func appendMessage(ctx context.Context, history store.HistoryRepository, sessionID, role, content, metadata string) error {
	if history == nil || strings.TrimSpace(content) == "" {
		return nil
	}
	return history.AppendMessage(ctx, domain.ConversationMessage{
		MessageID: uuid.NewString(),
		SessionID: sessionID,
		Role:      role,
		Content:   content,
		Metadata:  metadata,
		CreatedAt: time.Now(),
	})
}

type toolSet struct {
	byPlanName map[string]tool.InvokableTool
	tools      []tool.BaseTool
}

func newToolSet(runner *tools.Runner, sink ProgressSink, history store.HistoryRepository, sessionID string, task domain.Task, plan domain.Plan, state *executionState) toolSet {
	wrap := func(planName, displayName, description string) tool.InvokableTool {
		return &executionTool{
			info:        toolInfo(safeToolName(planName), description, planName),
			planName:    planName,
			displayName: displayName,
			runner:      runner,
			sink:        sink,
			history:     history,
			sessionID:   sessionID,
			task:        task,
			plan:        plan,
			state:       state,
		}
	}

	byPlanName := map[string]tool.InvokableTool{
		"im.fetch_thread":      wrap("im.fetch_thread", "Read IM context", "Read and summarize Feishu IM thread context for the task."),
		"im.context_summarize": wrap("im.context_summarize", "Read IM context", "Read and summarize Feishu IM thread context for the task."),
		"doc.create":           wrap("doc.create", "Create document", "Create the task document artifact."),
		"doc.append":           wrap("doc.append", "Append document", "Append structured content to the task document artifact."),
		"doc.generate":         wrap("doc.generate", "Generate document", "Generate the task document artifact."),
		"slide.generate":       wrap("slide.generate", "Generate slides", "Generate the task Slidev presentation artifact."),
		"slide.rehearse":       wrap("slide.rehearse", "Generate speaker notes", "Generate or confirm speaker notes for the slides."),
		"archive.bundle":       wrap("archive.bundle", "Bundle artifacts", "Bundle task artifacts into a manifest."),
		"sync.broadcast":       wrap("sync.broadcast", "Broadcast status", "Broadcast task status without creating artifacts."),
	}
	tools := make([]tool.BaseTool, 0, len(byPlanName))
	seen := make(map[string]struct{}, len(byPlanName))
	for _, tl := range byPlanName {
		info, _ := tl.Info(context.Background())
		if info != nil {
			if _, ok := seen[info.Name]; ok {
				continue
			}
			seen[info.Name] = struct{}{}
		}
		tools = append(tools, tl)
	}
	return toolSet{byPlanName: byPlanName, tools: tools}
}

type executionState struct {
	contextResult   tools.Result
	docResult       tools.Result
	slidesResult    tools.Result
	docGenerated    bool
	slidesGenerated bool
}

type executionTool struct {
	info        *schema.ToolInfo
	planName    string
	displayName string
	runner      *tools.Runner
	sink        ProgressSink
	history     store.HistoryRepository
	sessionID   string
	task        domain.Task
	plan        domain.Plan
	state       *executionState
}

func (t *executionTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return t.info, nil
}

func (t *executionTool) InvokableRun(ctx context.Context, argumentsInJSON string, _ ...tool.Option) (string, error) {
	var input stepToolInput
	if err := json.Unmarshal([]byte(argumentsInJSON), &input); err != nil {
		return "", err
	}
	step := domain.PlanStep{
		ID:          fallback(input.StepID, safeToolName(t.planName)),
		Tool:        normalizePlanTool(input.Tool, t.planName),
		Description: fallback(input.Description, t.displayName),
		Args:        input.Args,
	}

	startedAt := time.Now()
	if _, err := t.sink.StartToolStep(ctx, t.task.TaskID, step); err != nil {
		return "", err
	}

	result := t.execute(ctx, step, input)
	completedAt := time.Now()
	logToolResult(t.task.TaskID, step, argumentsInJSON, result, completedAt.Sub(startedAt))
	if t.history != nil {
		_ = t.history.AppendToolInvocation(ctx, toolInvocation(t.sessionID, t.task.TaskID, step, argumentsInJSON, result, startedAt, completedAt))
	}

	if !result.Success {
		err := fmt.Errorf("%s failed: %s", step.Tool, result.ErrorMessage)
		_, _ = t.sink.FailToolStep(ctx, t.task.TaskID, step, err)
		return "", err
	}
	if _, err := t.sink.CompleteToolStep(ctx, t.task.TaskID, result); err != nil {
		return "", err
	}

	payload, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func (t *executionTool) execute(ctx context.Context, step domain.PlanStep, input stepToolInput) tools.Result {
	switch step.Tool {
	case "im.fetch_thread", "im.context_summarize":
		t.state.contextResult = t.runner.FetchThread(ctx, t.task, step)
		return t.state.contextResult
	case "doc.create", "doc.append", "doc.generate":
		if t.state.docGenerated {
			return t.runner.CompleteStep(step)
		}
		t.state.docResult = t.runner.CreateDoc(ctx, t.plan, t.task.UserInstruction, t.state.contextResult, input.documentContent())
		t.state.docGenerated = t.state.docResult.Success
		return t.state.docResult
	case "slide.generate":
		if t.state.slidesGenerated {
			return t.runner.CompleteStep(step)
		}
		t.state.slidesResult = t.runner.CreateSlides(ctx, t.plan, input.slidevContent(), input.speakerNotesContent())
		t.state.slidesGenerated = t.state.slidesResult.Success
		return t.state.slidesResult
	case "slide.rehearse":
		if !t.state.slidesGenerated {
			t.state.slidesResult = t.runner.CreateSlides(ctx, t.plan, input.slidevContent(), input.speakerNotesContent())
			t.state.slidesGenerated = t.state.slidesResult.Success
			return t.state.slidesResult
		}
		result := t.runner.CreateSpeakerNotes(ctx, t.plan, input.speakerNotesContent(), t.state.slidesResult)
		if result.Success {
			t.mergeSlideResultData(result)
		}
		return result
	case "archive.bundle":
		return t.runner.Bundle(ctx, t.task, t.plan, t.state.docResult, t.state.slidesResult)
	case "sync.broadcast":
		return t.runner.CompleteStep(step)
	default:
		return t.runner.CompleteStep(step)
	}
}

func (t *executionTool) mergeSlideResultData(result tools.Result) {
	if t.state.slidesResult.Data == nil {
		t.state.slidesResult.Data = map[string]string{}
	}
	for key, value := range result.Data {
		t.state.slidesResult.Data[key] = value
	}
	if result.ArtifactURL != "" {
		t.state.slidesResult.ArtifactURL = result.ArtifactURL
	}
}

type stepToolInput struct {
	StepID         string         `json:"stepId"`
	Tool           string         `json:"tool"`
	Description    string         `json:"description"`
	Content        string         `json:"content,omitempty"`
	Markdown       string         `json:"markdown,omitempty"`
	SlidevMarkdown string         `json:"slidevMarkdown,omitempty"`
	SpeakerNotes   string         `json:"speakerNotes,omitempty"`
	Args           map[string]any `json:"args,omitempty"`
}

func (i stepToolInput) documentContent() string {
	if strings.TrimSpace(i.Content) != "" {
		return i.Content
	}
	if strings.TrimSpace(i.Markdown) != "" {
		return i.Markdown
	}
	return stringArg(i.Args, "content", "markdown", "documentMarkdown", "document")
}

func (i stepToolInput) slidevContent() string {
	if strings.TrimSpace(i.SlidevMarkdown) != "" {
		return i.SlidevMarkdown
	}
	if strings.TrimSpace(i.Content) != "" {
		return i.Content
	}
	if strings.TrimSpace(i.Markdown) != "" {
		return i.Markdown
	}
	return stringArg(i.Args, "slidevMarkdown", "content", "markdown", "slides")
}

func (i stepToolInput) speakerNotesContent() string {
	if strings.TrimSpace(i.SpeakerNotes) != "" {
		return i.SpeakerNotes
	}
	return stringArg(i.Args, "speakerNotes", "notes")
}

func stringArg(args map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := args[key]; ok {
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
				return text
			}
		}
	}
	return ""
}

func toolInvocation(sessionID, taskID string, step domain.PlanStep, args string, result tools.Result, startedAt, completedAt time.Time) domain.ToolInvocation {
	resultJSON, _ := json.Marshal(result)
	return domain.ToolInvocation{
		InvocationID:   uuid.NewString(),
		SessionID:      sessionID,
		TaskID:         taskID,
		StepID:         step.ID,
		ToolName:       step.Tool,
		ArgumentsJSON:  args,
		ResultSummary:  result.PayloadSummary,
		ResultJSON:     string(resultJSON),
		ErrorMessage:   result.ErrorMessage,
		ArtifactURL:    result.ArtifactURL,
		ArtifactPath:   result.ArtifactPath,
		StartedAt:      startedAt,
		CompletedAt:    completedAt,
		DurationMillis: completedAt.Sub(startedAt).Milliseconds(),
	}
}

func logToolResult(taskID string, step domain.PlanStep, args string, result tools.Result, duration time.Duration) {
	args = prettyJSONForLog(args)
	resultJSON, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		resultJSON = []byte(fmt.Sprintf(`{"marshal_error":%q}`, err.Error()))
	}

	log.Printf("[agent-tool] task=%s step=%s tool=%s duration=%s args=%s result=%s",
		taskID,
		step.ID,
		step.Tool,
		duration.Round(time.Millisecond),
		limitLogText(args, 12000),
		limitLogText(string(resultJSON), 12000),
	)
}

func prettyJSONForLog(raw string) string {
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return raw
	}
	out, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return raw
	}
	return string(out)
}

func limitLogText(text string, max int) string {
	if max <= 0 || len(text) <= max {
		return text
	}
	return text[:max] + fmt.Sprintf("...<truncated %d bytes>", len(text)-max)
}

func sessionIDForTask(task domain.Task) string {
	switch {
	case task.ChatID != "" && task.ThreadID != "":
		return "thread:" + task.ChatID + ":" + task.ThreadID
	case task.ChatID != "":
		return "chat:" + task.ChatID
	default:
		return "task:" + task.TaskID
	}
}

func observationForResult(result tools.Result) string {
	parts := []string{result.PayloadSummary}
	if result.ArtifactURL != "" {
		parts = append(parts, "artifact="+result.ArtifactURL)
	}
	if result.Data != nil {
		if source := result.Data["source"]; source != "" {
			parts = append(parts, "source="+source)
		}
		if count := result.Data["count"]; count != "" {
			parts = append(parts, "count="+count)
		}
	}
	return strings.Join(parts, "; ")
}

func safeToolName(name string) string {
	name = strings.ReplaceAll(name, ".", "_")
	name = strings.ReplaceAll(name, "-", "_")
	return name
}

func normalizePlanTool(value, defaultTool string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultTool
	}
	if value == defaultTool || safeToolName(value) == safeToolName(defaultTool) {
		return defaultTool
	}
	return value
}

func toolInfo(name, description, planName string) *schema.ToolInfo {
	params := map[string]*schema.ParameterInfo{
		"stepId": {
			Type:     schema.String,
			Desc:     "Plan step ID.",
			Required: true,
		},
		"tool": {
			Type:     schema.String,
			Desc:     "Original plan tool name.",
			Required: true,
		},
		"description": {
			Type:     schema.String,
			Desc:     "Human-readable step description.",
			Required: true,
		},
	}

	switch planName {
	case "doc.create", "doc.append", "doc.generate":
		params["content"] = &schema.ParameterInfo{
			Type:     schema.String,
			Desc:     "Complete Markdown document content to persist. Generate this from the task instruction, plan, and fetched IM context; do not leave it empty.",
			Required: true,
		}
	case "slide.generate":
		params["slidevMarkdown"] = &schema.ParameterInfo{
			Type:     schema.String,
			Desc:     "Complete Slidev Markdown presentation content to persist. Include frontmatter and all slides.",
			Required: true,
		}
		params["speakerNotes"] = &schema.ParameterInfo{
			Type:     schema.String,
			Desc:     "Optional speaker notes Markdown. If omitted, call slide_rehearse later with complete notes.",
			Required: false,
		}
	case "slide.rehearse":
		params["speakerNotes"] = &schema.ParameterInfo{
			Type:     schema.String,
			Desc:     "Complete speaker notes Markdown for the generated presentation.",
			Required: true,
		}
	}

	return &schema.ToolInfo{
		Name:        name,
		Desc:        description,
		ParamsOneOf: schema.NewParamsOneOfByParams(params),
	}
}

func fallback(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func isLogicalStep(tool string) bool {
	return tool == "intent.analyze" || tool == "planner.build"
}

func planNeedsDoc(plan domain.Plan) bool {
	for _, step := range plan.Steps {
		if step.Tool == "doc.create" || step.Tool == "doc.append" || step.Tool == "doc.generate" {
			return true
		}
	}
	return false
}

func planNeedsSlides(plan domain.Plan) bool {
	for _, step := range plan.Steps {
		if step.Tool == "slide.generate" || step.Tool == "slide.rehearse" {
			return true
		}
	}
	return false
}
