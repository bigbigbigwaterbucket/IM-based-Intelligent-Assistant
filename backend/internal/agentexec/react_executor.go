package agentexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	openai "github.com/cloudwego/eino-ext/components/model/openai"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	"agentpilot/backend/internal/domain"
	"agentpilot/backend/internal/store"
	"agentpilot/backend/internal/tools"
)

type ReactExecutor struct {
	runner  *tools.Runner
	sink    ProgressSink
	history store.HistoryRepository
	model   einomodel.ToolCallingChatModel
	maxStep int
}

func NewReactExecutor(runner *tools.Runner, sink ProgressSink, history store.HistoryRepository, model einomodel.ToolCallingChatModel) *ReactExecutor {
	return &ReactExecutor{
		runner:  runner,
		sink:    sink,
		history: history,
		model:   model,
		maxStep: 20,
	}
}

func NewReactExecutorFromEnv(ctx context.Context, runner *tools.Runner, sink ProgressSink, history store.HistoryRepository) (*ReactExecutor, bool, error) {
	if !reactAgentEnabled() {
		return nil, false, nil
	}
	config := reactModelConfigFromEnv()
	if !config.enabled() {
		return nil, true, errors.New("react agent is enabled but no model config is available")
	}
	model, err := openai.NewChatModel(ctx, config.chatModelConfig())
	if err != nil {
		return nil, true, err
	}
	return NewReactExecutor(runner, sink, history, model), true, nil
}

func (e *ReactExecutor) Execute(ctx context.Context, task domain.Task, plan domain.Plan) (domain.Task, error) {
	if e.runner == nil {
		return domain.Task{}, errors.New("react executor requires tools runner")
	}
	if e.sink == nil {
		return domain.Task{}, errors.New("react executor requires progress sink")
	}
	if e.model == nil {
		return domain.Task{}, errors.New("react executor requires tool-calling model")
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
	agent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: e.model,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools:               toolSet.tools,
			ExecuteSequentially: true,
		},
		MaxStep: e.maxStep,
		MessageModifier: func(ctx context.Context, input []*schema.Message) []*schema.Message {
			out := make([]*schema.Message, 0, len(input)+1)
			out = append(out, schema.SystemMessage(reactSystemPrompt()))
			out = append(out, input...)
			return out
		},
	})
	if err != nil {
		return task, err
	}

	planJSON, _ := json.MarshalIndent(plan, "", "  ")
	msg, err := agent.Generate(ctx, []*schema.Message{
		schema.UserMessage(fmt.Sprintf("Task ID: %s\nTitle: %s\nInstruction: %s\nPlan JSON:\n%s\n\nExecute the plan now. Call every required tool once, in dependency order, using the safe tool names described in the system message. Do not invent artifacts.", task.TaskID, task.Title, task.UserInstruction, string(planJSON))),
	})
	if err != nil {
		return task, err
	}
	if msg != nil && strings.TrimSpace(msg.Content) != "" {
		_ = appendMessage(ctx, e.history, sessionID, "assistant", msg.Content, "agent_final")
	}
	if planNeedsDoc(plan) && !state.docGenerated {
		return task, errors.New("react agent finished without generating the required document artifact")
	}
	if planNeedsSlides(plan) && !state.slidesGenerated {
		return task, errors.New("react agent finished without generating the required slides artifact")
	}

	task, err = e.sink.CompleteAgentRun(ctx, task.TaskID, plan)
	if err != nil {
		return domain.Task{}, err
	}
	return task, nil
}

func reactSystemPrompt() string {
	return `You are the execution agent for IM-based Intelligent Assistant.
Execute the provided plan using tools only. The original plan tool names use dots, while callable tool names use underscores:
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
2. Call only tools required by the plan.
3. Pass stepId, original dotted tool name, and description in tool arguments.
4. Do not call artifact tools for greeting or status-only tasks.
5. After tool calls complete, summarize what was produced.`
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

type reactModelConfig struct {
	APIKey     string
	BaseURL    string
	Model      string
	Timeout    time.Duration
	HTTPClient *http.Client
}

func (c reactModelConfig) enabled() bool {
	return c.APIKey != "" && c.BaseURL != "" && c.Model != ""
}

func (c reactModelConfig) chatModelConfig() *openai.ChatModelConfig {
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

func reactModelConfigFromEnv() reactModelConfig {
	apiKey := strings.TrimSpace(os.Getenv("AGENT_API_KEY"))
	baseURL := strings.TrimSpace(os.Getenv("AGENT_BASE_URL"))
	model := strings.TrimSpace(os.Getenv("AGENT_MODEL"))
	if apiKey != "" || baseURL != "" || model != "" {
		return reactModelConfig{
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
		return reactModelConfig{
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
	return reactModelConfig{
		APIKey:  strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY")),
		BaseURL: strings.TrimRight(baseURL, "/"),
		Model:   model,
		Timeout: 60 * time.Second,
	}
}

func reactAgentEnabled() bool {
	value := strings.TrimSpace(os.Getenv("AGENT_EXECUTOR"))
	return strings.EqualFold(value, "react") || strings.EqualFold(value, "eino-react") || envBool("ENABLE_REACT_AGENT")
}

func envBool(key string) bool {
	value := strings.TrimSpace(os.Getenv(key))
	return value == "1" || strings.EqualFold(value, "true")
}
