package agentexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	skillmw "github.com/cloudwego/eino/adk/middlewares/skill"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	openai "github.com/cloudwego/eino-ext/components/model/openai"

	"agentpilot/backend/internal/domain"
	"agentpilot/backend/internal/store"
	"agentpilot/backend/internal/tools"
)

type ADKExecutor struct {
	runner   *tools.Runner
	sink     ProgressSink
	history  store.HistoryRepository
	model    einomodel.ToolCallingChatModel
	skillDir string
	maxStep  int
}

func NewADKExecutor(runner *tools.Runner, sink ProgressSink, history store.HistoryRepository, model einomodel.ToolCallingChatModel) *ADKExecutor {
	return NewADKExecutorWithSkillDir(runner, sink, history, model, defaultSkillDir())
}

func NewADKExecutorWithSkillDir(runner *tools.Runner, sink ProgressSink, history store.HistoryRepository, model einomodel.ToolCallingChatModel, skillDir string) *ADKExecutor {
	return &ADKExecutor{
		runner:   runner,
		sink:     sink,
		history:  history,
		model:    model,
		skillDir: skillDir,
		maxStep:  24,
	}
}

func NewADKExecutorFromEnv(ctx context.Context, runner *tools.Runner, sink ProgressSink, history store.HistoryRepository) (*ADKExecutor, bool, error) {
	if !agentExecutorEnabled() {
		return nil, false, nil
	}
	config := agentModelConfigFromEnv()
	if !config.enabled() {
		return nil, true, errors.New("eino adk agent is enabled but no model config is available")
	}
	model, err := openai.NewChatModel(ctx, config.chatModelConfig())
	if err != nil {
		return nil, true, err
	}
	return NewADKExecutorWithSkillDir(runner, sink, history, model, defaultSkillDir()), true, nil
}

func (e *ADKExecutor) Execute(ctx context.Context, task domain.Task, plan domain.Plan) (domain.Task, error) {
	if e.runner == nil {
		return domain.Task{}, errors.New("adk executor requires tools runner")
	}
	if e.sink == nil {
		return domain.Task{}, errors.New("adk executor requires progress sink")
	}
	if e.model == nil {
		return domain.Task{}, errors.New("adk executor requires tool-calling model")
	}

	sessionID := sessionIDForTask(task)
	if err := ensureSession(ctx, e.history, sessionID, task); err != nil {
		return domain.Task{}, err
	}
	_ = appendMessage(ctx, e.history, sessionID, "user", task.UserInstruction, "task_input")

	task, err := e.sink.StartAgentRun(ctx, task.TaskID, plan)
	if err != nil {
		return domain.Task{}, err
	}

	state := &executionState{}
	toolSet := newToolSet(e.runner, e.sink, e.history, sessionID, task, plan, state)
	skillHandler, err := generationSkillMiddleware(ctx, e.skillDir)
	if err != nil {
		return task, err
	}

	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "agentpilot_executor",
		Description: "Executes IM assistant plans with tools and generation skills.",
		Instruction: adkSystemPrompt(),
		Model:       e.model,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{
				Tools:               toolSet.tools,
				ExecuteSequentially: true,
				ToolCallMiddlewares: []compose.ToolMiddleware{skillToolLogMiddleware()},
			},
		},
		Handlers:      []adk.ChatModelAgentMiddleware{skillHandler},
		MaxIterations: e.maxStep,
	})
	if err != nil {
		return task, err
	}

	planJSON, _ := json.MarshalIndent(plan, "", "  ")
	iter := agent.Run(ctx, &adk.AgentInput{Messages: []adk.Message{
		schema.UserMessage(fmt.Sprintf("Task ID: %s\nTitle: %s\nInstruction: %s\nPlan JSON:\n%s\n\nExecute the plan now. Follow dependency order. For document generation, load and follow the document_generation skill before calling doc tools. For slide/PPT generation, load and follow the slide_generation skill before calling slide tools. Call every required plan tool exactly once using the safe tool names described in the system message. Do not invent artifacts.",
			task.TaskID, task.Title, task.UserInstruction, string(planJSON))),
	}})

	finalTexts := make([]string, 0)
	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event == nil {
			continue
		}
		if event.Err != nil {
			return task, event.Err
		}
		if event.Output == nil || event.Output.MessageOutput == nil {
			continue
		}
		msg, err := event.Output.MessageOutput.GetMessage()
		if err != nil {
			return task, err
		}
		if msg != nil && msg.Role == schema.Assistant && strings.TrimSpace(msg.Content) != "" {
			finalTexts = append(finalTexts, msg.Content)
		}
	}

	if len(finalTexts) > 0 {
		_ = appendMessage(ctx, e.history, sessionID, "assistant", strings.Join(finalTexts, "\n"), "agent_final")
	}
	if planNeedsDoc(plan) && !state.docGenerated {
		return task, errors.New("adk agent finished without generating the required document artifact")
	}
	if planNeedsSlides(plan) && !state.slidesGenerated {
		return task, errors.New("adk agent finished without generating the required slides artifact")
	}

	task, err = e.sink.CompleteAgentRun(ctx, task.TaskID, plan)
	if err != nil {
		return domain.Task{}, err
	}
	return task, nil
}

func skillToolLogMiddleware() compose.ToolMiddleware {
	return compose.ToolMiddleware{
		Invokable: func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
			return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
				if input == nil || input.Name != "skill" {
					return next(ctx, input)
				}
				startedAt := time.Now()
				output, err := next(ctx, input)
				duration := time.Since(startedAt)
				result := ""
				if output != nil {
					result = output.Result
				}
				if err != nil {
					log.Printf("[agent-tool] tool=skill duration=%s args=%s error=%s", duration.Round(time.Millisecond), limitLogText(input.Arguments, 12000), err.Error())
					return output, err
				}
				log.Printf("[agent-tool] tool=skill duration=%s args=%s result=%s", duration.Round(time.Millisecond), limitLogText(input.Arguments, 12000), limitLogText(result, 12000))
				return output, nil
			}
		},
	}
}

func adkSystemPrompt() string {
	return `You are the execution agent for IM-based Intelligent Assistant.
Execute the provided plan using Eino tools and Eino skills only. The original plan tool names use dots, while callable tool names use underscores:
- im.fetch_thread -> im_fetch_thread
- im.context_summarize -> im_context_summarize
- doc.create -> doc_create
- doc.append -> doc_append
- doc.generate -> doc_generate
- slide.generate -> slide_generate
- slide.rehearse -> slide_rehearse
- archive.bundle -> archive_bundle
- sync.broadcast -> sync_broadcast

Rules:
1. Follow dependency order from the plan.
2. Call only tools required by the plan, except the skill tool.
3. Before any doc tool, call skill with skill=document_generation and follow the returned instructions.
4. Before any slide or PPT tool, call skill with skill=slide_generation and follow the returned instructions.
5. Pass stepId, original dotted tool name, and description in plan tool arguments.
6. For doc_create/doc_append/doc_generate, generate the final user-facing Markdown yourself and pass it as content.
7. For slide_generate, generate the final Slidev Markdown yourself and pass it as slidevMarkdown. For slide_rehearse, generate speaker notes and pass them as speakerNotes.
8. Do not call artifact tools for greeting or status-only tasks.
9. After tool calls complete, summarize what was produced.`
}

type agentModelConfig struct {
	APIKey     string
	BaseURL    string
	Model      string
	Timeout    time.Duration
	HTTPClient *http.Client
}

func (c agentModelConfig) enabled() bool {
	return c.APIKey != "" && c.BaseURL != "" && c.Model != ""
}

func (c agentModelConfig) chatModelConfig() *openai.ChatModelConfig {
	temperature := float32(0.2)
	maxTokens := 4096
	return &openai.ChatModelConfig{
		APIKey:      c.APIKey,
		BaseURL:     c.BaseURL,
		Model:       c.Model,
		Timeout:     c.Timeout,
		HTTPClient:  c.HTTPClient,
		Temperature: &temperature,
		MaxTokens:   &maxTokens,
	}
}

func agentModelConfigFromEnv() agentModelConfig {
	apiKey := strings.TrimSpace(os.Getenv("AGENT_API_KEY"))
	baseURL := strings.TrimSpace(os.Getenv("AGENT_BASE_URL"))
	model := strings.TrimSpace(os.Getenv("AGENT_MODEL"))
	if apiKey != "" || baseURL != "" || model != "" {
		return agentModelConfig{
			APIKey:  apiKey,
			BaseURL: strings.TrimRight(baseURL, "/"),
			Model:   model,
			Timeout: 60 * time.Second,
		}
	}

	apiKey = strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
	baseURL = strings.TrimSpace(os.Getenv("DEEPSEEK_BASE_URL"))
	model = strings.TrimSpace(os.Getenv("DEEPSEEK_MODEL"))
	if apiKey != "" && baseURL != "" && model != "" {
		return agentModelConfig{
			APIKey:  apiKey,
			BaseURL: strings.TrimRight(baseURL, "/"),
			Model:   model,
			Timeout: 60 * time.Second,
		}
	}

	baseURL = strings.TrimSpace(os.Getenv("DEEPSEEK_BASE_URL"))
	if baseURL == "" {
		baseURL = "https://api.deepseek.com"
	}
	model = strings.TrimSpace(os.Getenv("DEEPSEEK_MODEL"))
	if model == "" {
		model = "deepseek-chat"
	}
	return agentModelConfig{
		APIKey:  strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY")),
		BaseURL: strings.TrimRight(baseURL, "/"),
		Model:   model,
		Timeout: 60 * time.Second,
	}
}

func agentExecutorEnabled() bool {
	value := strings.TrimSpace(os.Getenv("AGENT_EXECUTOR"))
	return strings.EqualFold(value, "adk") ||
		strings.EqualFold(value, "eino-adk") ||
		strings.EqualFold(value, "react") ||
		strings.EqualFold(value, "eino-react") ||
		envBool("ENABLE_ADK_AGENT") ||
		envBool("ENABLE_REACT_AGENT")
}

func envBool(key string) bool {
	value := strings.TrimSpace(os.Getenv(key))
	return value == "1" || strings.EqualFold(value, "true")
}

func generationSkillMiddleware(ctx context.Context, skillDir string) (adk.ChatModelAgentMiddleware, error) {
	if strings.TrimSpace(skillDir) == "" {
		return nil, errors.New("agent skill directory is empty")
	}
	absSkillDir, err := filepath.Abs(skillDir)
	if err != nil {
		return nil, fmt.Errorf("resolve agent skill directory: %w", err)
	}
	info, err := os.Stat(absSkillDir)
	if err != nil {
		return nil, fmt.Errorf("agent skill directory %q is not available: %w", absSkillDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("agent skill path %q is not a directory", absSkillDir)
	}

	skillBackend, err := skillmw.NewBackendFromFilesystem(ctx, &skillmw.BackendFromFilesystemConfig{
		Backend: newLocalSkillFilesystem(absSkillDir),
		BaseDir: absSkillDir,
	})
	if err != nil {
		return nil, err
	}
	skills, err := skillBackend.List(ctx)
	if err != nil {
		return nil, err
	}
	if len(skills) == 0 {
		return nil, fmt.Errorf("no Eino skills found under %q; expected first-level */SKILL.md files", absSkillDir)
	}

	return skillmw.NewMiddleware(ctx, &skillmw.Config{
		Backend:    skillBackend,
		UseChinese: true,
		CustomToolDescription: func(ctx context.Context, skills []skillmw.FrontMatter) string {
			var builder strings.Builder
			builder.WriteString("Load a generation skill before calling generation tools. Available skills:\n")
			for _, skill := range skills {
				builder.WriteString("- ")
				builder.WriteString(skill.Name)
				if strings.TrimSpace(skill.Description) != "" {
					builder.WriteString(": ")
					builder.WriteString(skill.Description)
				}
				builder.WriteString("\n")
			}
			builder.WriteString("Call with JSON like {\"skill\":\"document_generation\"}.\n")
			return builder.String()
		},
	})
}

func defaultSkillDir() string {
	if value := strings.TrimSpace(os.Getenv("AGENT_SKILL_DIR")); value != "" {
		return value
	}
	for _, candidate := range []string{
		filepath.Join("backend", "skills"),
		"skills",
	} {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return filepath.Join("backend", "skills")
}

var _ Executor = (*ADKExecutor)(nil)
