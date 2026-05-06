package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"agentpilot/backend/internal/agentexec"
	"agentpilot/backend/internal/domain"
	"agentpilot/backend/internal/planner"
	"agentpilot/backend/internal/statehub"
	"agentpilot/backend/internal/store"
	"agentpilot/backend/internal/tools"
)

type CreateTaskInput struct {
	Title            string `json:"title"`
	Instruction      string `json:"instruction"`
	Source           string `json:"source"`
	ChatID           string `json:"chatId"`
	ThreadID         string `json:"threadId"`
	MessageID        string `json:"messageId"`
	InitiatorUserID  string `json:"initiatorUserId"`
	InitiatorOpenID  string `json:"initiatorOpenId"`
	InitiatorUnionID string `json:"initiatorUnionId"`
}

type ContinueTaskInput struct {
	SessionID   string `json:"sessionId"`
	Instruction string `json:"instruction"`
	MessageID   string `json:"messageId"`
	ActorType   string `json:"actorType"`
}

type ActionInput struct {
	ActionType   string `json:"actionType"`
	ActorType    string `json:"actorType"`
	ClientID     string `json:"clientId"`
	ActorUserID  string `json:"actorUserId"`
	ActorOpenID  string `json:"actorOpenId"`
	ActorUnionID string `json:"actorUnionId"`
}

type CreateProactiveCandidateInput struct {
	ChatID          string `json:"chatId"`
	ThreadID        string `json:"threadId"`
	SourceMessageID string `json:"sourceMessageId"`
	Title           string `json:"title"`
	Instruction     string `json:"instruction"`
	ContextJSON     string `json:"contextJson"`
	ThemeKey        string `json:"themeKey"`
	TTL             time.Duration
}

type Service struct {
	store    store.TaskRepository
	hub      *statehub.Hub
	planner  planner.Builder
	tools    *tools.Runner
	executor agentexec.Executor
}

func New(taskStore store.TaskRepository, hub *statehub.Hub, plannerSvc planner.Builder, toolRunner *tools.Runner) *Service {
	service := &Service{
		store:   taskStore,
		hub:     hub,
		planner: plannerSvc,
		tools:   toolRunner,
	}
	history, _ := taskStore.(store.HistoryRepository)
	service.executor = agentexec.NewPlanExecutor(toolRunner, service, history)
	return service
}

func NewWithExecutor(taskStore store.TaskRepository, hub *statehub.Hub, plannerSvc planner.Builder, toolRunner *tools.Runner, executor agentexec.Executor) *Service {
	service := New(taskStore, hub, plannerSvc, toolRunner)
	if executor != nil {
		service.executor = executor
	}
	return service
}

func (s *Service) SetExecutor(executor agentexec.Executor) {
	if executor != nil {
		s.executor = executor
	}
}

func (s *Service) CreateTask(ctx context.Context, input CreateTaskInput) (domain.Task, error) {
	if input.Title == "" || input.Instruction == "" {
		return domain.Task{}, errors.New("title and instruction are required")
	}

	now := time.Now()
	task := domain.Task{
		TaskID:            uuid.NewString(),
		Title:             input.Title,
		UserInstruction:   input.Instruction,
		Source:            fallback(input.Source, "desktop"),
		ChatID:            input.ChatID,
		ThreadID:          input.ThreadID,
		MessageID:         input.MessageID,
		InitiatorUserID:   input.InitiatorUserID,
		InitiatorOpenID:   input.InitiatorOpenID,
		InitiatorUnionID:  input.InitiatorUnionID,
		Status:            domain.StatusCreated,
		CurrentStep:       "created",
		ProgressText:      "任务已创建，等待规划",
		Version:           1,
		LastActor:         fallback(input.Source, "desktop"),
		LastInteractionAt: &now,
		CreatedAt:         now,
		UpdatedAt:         now,
		Steps: []domain.Step{
			newStep("capture", "任务创建", domain.StepCompleted, "来自桌面端手动创建"),
		},
	}

	task, err := s.store.Create(ctx, task)
	if err != nil {
		return domain.Task{}, err
	}
	_ = s.upsertSession(ctx, sessionIDForTask(task), task)
	s.hub.Broadcast("task.created", task.TaskID, task.Version, task)

	go s.runTask(context.Background(), task.TaskID)
	return task, nil
}

func (s *Service) ContinueTask(ctx context.Context, input ContinueTaskInput) (domain.Task, error) {
	if strings.TrimSpace(input.SessionID) == "" {
		return domain.Task{}, errors.New("session id is required")
	}
	revision := strings.TrimSpace(input.Instruction)
	if revision == "" {
		return domain.Task{}, errors.New("instruction is required")
	}

	repo, ok := s.store.(store.ActiveTaskRepository)
	if !ok {
		return domain.Task{}, errors.New("active task lookup is not available")
	}
	task, err := repo.FindActiveTaskBySession(ctx, input.SessionID)
	if err != nil {
		return domain.Task{}, err
	}
	if task.Status == domain.StatusCreated || task.Status == domain.StatusPlanning || task.Status == domain.StatusExecuting {
		return domain.Task{}, errors.New("task is still running; wait for the current run to finish")
	}

	now := time.Now()
	originalInstruction := task.UserInstruction
	task = s.updateTask(ctx, task, func(current *domain.Task) {
		current.UserInstruction = revisionInstruction(originalInstruction, revision)
		current.MessageID = fallback(input.MessageID, current.MessageID)
		current.Status = domain.StatusPlanning
		current.RequiresAction = false
		current.ErrorMessage = ""
		current.CurrentStep = "revision_planning"
		current.ProgressText = "Applying revision request"
		current.LastActor = fallback(input.ActorType, "user")
		current.LastInteractionAt = &now
		current.IdlePromptedAt = nil
		current.Steps = append(current.Steps, newStep("revision_request", "任务修订", domain.StepCompleted, revision))
	})
	if task.TaskID == "" {
		return domain.Task{}, errors.New("task state update failed")
	}
	_ = s.upsertSession(ctx, input.SessionID, task)

	plan := s.buildRevisionPlan(ctx, task, revision)
	go s.runPreparedPlan(context.Background(), task.TaskID, plan)
	return task, nil
}

func (s *Service) SubmitAction(ctx context.Context, taskID string, input ActionInput) (domain.Task, error) {
	task, err := s.store.Get(ctx, taskID)
	if err != nil {
		return domain.Task{}, err
	}

	switch input.ActionType {
	case string(domain.ActionRetryTask):
		if task.Status != domain.StatusFailed {
			return domain.Task{}, errors.New("只有失败的任务可以被重试")
		}
		task.Status = domain.StatusCreated
		task.RequiresAction = false
		task.ErrorMessage = ""
		task.ProgressText = "任务已重新排队"
		task.CurrentStep = "created"
		task.LastActor = input.ActorType
		task.Version++
		task.UpdatedAt = time.Now()
		task.Steps = append(task.Steps, newStep("retry", "任务重试", domain.StepCompleted, fmt.Sprintf("由 %s 发起重试", input.ActorType)))
		task, err = s.store.Update(ctx, task)
		if err != nil {
			return domain.Task{}, err
		}
		s.hub.Broadcast("action.resolved", task.TaskID, task.Version, task)
		go s.runTask(context.Background(), task.TaskID)
		return task, nil
	case string(domain.ActionApproveContinue):
		if !task.RequiresAction {
			return domain.Task{}, errors.New("task is not waiting for action")
		}
		task.RequiresAction = false
		task.Status = domain.StatusExecuting
		task.ProgressText = "收到继续指令，恢复执行"
		task.LastActor = input.ActorType
		task.Version++
		task.UpdatedAt = time.Now()
		task.Steps = append(task.Steps, newStep("continue", "继续执行", domain.StepCompleted, fmt.Sprintf("由 %s 确认继续", input.ActorType)))
		task, err = s.store.Update(ctx, task)
		if err != nil {
			return domain.Task{}, err
		}
		s.hub.Broadcast("action.resolved", task.TaskID, task.Version, task)
		go s.finishTask(context.Background(), task.TaskID)
		return task, nil
	case string(domain.ActionEndTask):
		if task.Status != domain.StatusWaitingAction {
			return domain.Task{}, errors.New("只有待审核的任务可以被结束")
		}
		if !canActorEndTask(task, input) {
			return domain.Task{}, errors.New("只有任务启动者可以结束该任务")
		}
		return s.endTask(ctx, task, fallback(input.ActorType, "user"), "Task ended by user")
	default:
		return domain.Task{}, errors.New("不支持的操作")
	}
}

func (s *Service) EndActiveTask(ctx context.Context, sessionID, actorType string) (domain.Task, error) {
	repo, ok := s.store.(store.ActiveTaskRepository)
	if !ok {
		return domain.Task{}, errors.New("active task lookup is not available")
	}
	task, err := repo.FindActiveTaskBySession(ctx, sessionID)
	if err != nil {
		return domain.Task{}, err
	}
	return s.endTask(ctx, task, fallback(actorType, "user"), "Task ended by /assistant new")
}

func (s *Service) ListIdleWaitingTasks(ctx context.Context, idleFor time.Duration) ([]domain.Task, error) {
	repo, ok := s.store.(store.ActiveTaskRepository)
	if !ok {
		return nil, errors.New("idle task lookup is not available")
	}
	if idleFor <= 0 {
		idleFor = 30 * time.Minute
	}
	return repo.ListIdleWaitingTasks(ctx, time.Now().Add(-idleFor))
}

func (s *Service) MarkIdlePrompted(ctx context.Context, taskID string) (domain.Task, error) {
	task, err := s.store.Get(ctx, taskID)
	if err != nil {
		return domain.Task{}, err
	}
	now := time.Now()
	task = s.updateTask(ctx, task, func(current *domain.Task) {
		current.IdlePromptedAt = &now
	})
	if task.TaskID == "" {
		return domain.Task{}, errors.New("task state update failed")
	}
	return task, nil
}

func (s *Service) CreateProactiveCandidate(ctx context.Context, input CreateProactiveCandidateInput) (domain.ProactiveCandidate, error) {
	repo, ok := s.store.(store.ProactiveCandidateRepository)
	if !ok {
		return domain.ProactiveCandidate{}, errors.New("proactive candidate repository is not available")
	}
	if strings.TrimSpace(input.ChatID) == "" {
		return domain.ProactiveCandidate{}, errors.New("chat id is required")
	}
	if strings.TrimSpace(input.Title) == "" || strings.TrimSpace(input.Instruction) == "" {
		return domain.ProactiveCandidate{}, errors.New("title and instruction are required")
	}
	if strings.TrimSpace(input.ThemeKey) == "" {
		return domain.ProactiveCandidate{}, errors.New("theme key is required")
	}
	if input.TTL <= 0 {
		input.TTL = 24 * time.Hour
	}
	now := time.Now()
	return repo.CreateProactiveCandidate(ctx, domain.ProactiveCandidate{
		CandidateID:     uuid.NewString(),
		ChatID:          input.ChatID,
		ThreadID:        input.ThreadID,
		SourceMessageID: input.SourceMessageID,
		Title:           input.Title,
		Instruction:     input.Instruction,
		ContextJSON:     input.ContextJSON,
		ThemeKey:        input.ThemeKey,
		Status:          domain.CandidatePending,
		ExpiresAt:       now.Add(input.TTL),
		CreatedAt:       now,
		UpdatedAt:       now,
	})
}

func (s *Service) AppendChatMessage(ctx context.Context, message domain.ChatMessage, keepLimit int) error {
	repo, ok := s.store.(store.ChatMessageRepository)
	if !ok {
		return errors.New("chat message repository is not available")
	}
	return repo.AppendChatMessage(ctx, message, keepLimit)
}

func (s *Service) ListRecentChatMessages(ctx context.Context, chatID string, limit int) ([]domain.ChatMessage, error) {
	repo, ok := s.store.(store.ChatMessageRepository)
	if !ok {
		return nil, errors.New("chat message repository is not available")
	}
	return repo.ListRecentChatMessages(ctx, chatID, limit)
}

func (s *Service) ConsumeChatMessages(ctx context.Context, chatID, throughMessageID string) error {
	repo, ok := s.store.(store.ChatMessageRepository)
	if !ok {
		return errors.New("chat message repository is not available")
	}
	return repo.ConsumeChatMessages(ctx, chatID, throughMessageID)
}

func (s *Service) HasRecentProactiveCandidate(ctx context.Context, chatID, themeKey string, cooldown time.Duration) (bool, error) {
	repo, ok := s.store.(store.ProactiveCandidateRepository)
	if !ok {
		return false, errors.New("proactive candidate repository is not available")
	}
	if cooldown <= 0 {
		cooldown = time.Hour
	}
	return repo.HasRecentProactiveCandidate(ctx, chatID, themeKey, time.Now().Add(-cooldown))
}

func (s *Service) LatestProactiveThemeKey(ctx context.Context, chatID string) (string, error) {
	repo, ok := s.store.(store.ProactiveCandidateRepository)
	if !ok {
		return "", errors.New("proactive candidate repository is not available")
	}
	return repo.LatestProactiveThemeKey(ctx, chatID)
}

func (s *Service) ConfirmProactiveCandidate(ctx context.Context, candidateID string, input ActionInput) (domain.Task, error) {
	repo, ok := s.store.(store.ProactiveCandidateRepository)
	if !ok {
		return domain.Task{}, errors.New("proactive candidate repository is not available")
	}
	candidate, err := repo.GetProactiveCandidate(ctx, candidateID)
	if err != nil {
		return domain.Task{}, err
	}
	if candidate.Status != domain.CandidatePending {
		return domain.Task{}, errors.New("proactive candidate is not pending")
	}
	if !candidate.ExpiresAt.IsZero() && time.Now().After(candidate.ExpiresAt) {
		_, _ = repo.UpdateProactiveCandidateStatus(ctx, candidateID, domain.CandidateIgnored)
		return domain.Task{}, errors.New("proactive candidate has expired")
	}
	task, err := s.CreateTask(ctx, CreateTaskInput{
		Title:            candidate.Title,
		Instruction:      candidate.Instruction,
		Source:           "feishu_proactive",
		ChatID:           candidate.ChatID,
		ThreadID:         candidate.ThreadID,
		MessageID:        candidate.SourceMessageID,
		InitiatorUserID:  input.ActorUserID,
		InitiatorOpenID:  input.ActorOpenID,
		InitiatorUnionID: input.ActorUnionID,
	})
	if err != nil {
		return domain.Task{}, err
	}
	_, _ = repo.UpdateProactiveCandidateStatus(ctx, candidateID, domain.CandidateConfirmed)
	return task, nil
}

func (s *Service) IgnoreProactiveCandidate(ctx context.Context, candidateID string) (domain.ProactiveCandidate, error) {
	repo, ok := s.store.(store.ProactiveCandidateRepository)
	if !ok {
		return domain.ProactiveCandidate{}, errors.New("proactive candidate repository is not available")
	}
	return repo.UpdateProactiveCandidateStatus(ctx, candidateID, domain.CandidateIgnored)
}

func (s *Service) GetTask(ctx context.Context, taskID string) (domain.Task, error) {
	return s.store.Get(ctx, taskID)
}

func (s *Service) WaitTaskDone(ctx context.Context, taskID string, timeout, interval time.Duration) (domain.Task, error) {
	if timeout <= 0 {
		timeout = 180 * time.Second
	}
	if interval <= 0 {
		interval = 3 * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		task, err := s.store.Get(ctx, taskID)
		if err != nil {
			return domain.Task{}, err
		}
		if task.Status == domain.StatusCompleted || task.Status == domain.StatusWaitingAction || task.Status == domain.StatusFailed {
			return task, nil
		}

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return domain.Task{}, fmt.Errorf("wait task done: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

func (s *Service) runTask(ctx context.Context, taskID string) {
	task, err := s.store.Get(ctx, taskID)
	if err != nil {
		return
	}

	task = s.updateTask(ctx, task, func(current *domain.Task) {
		current.Status = domain.StatusPlanning
		current.CurrentStep = "planning"
		current.ProgressText = "Agent 正在规划任务"
		current.LastActor = "agent"
		current.Steps = append(current.Steps, newStep("intent_analysis", "意图分析与任务规划", domain.StepRunning, "识别受众、交付物、上下文需求和风险"))
	})
	if task.TaskID == "" {
		return
	}

	plan, err := s.planner.BuildPlan(ctx, task.Title, task.UserInstruction)
	if err != nil {
		s.failTask(ctx, taskID, "规划失败: "+err.Error(), true)
		return
	}

	task = s.updateTask(ctx, task, func(current *domain.Task) {
		completeLatestStep(current)
		current.Summary = plan.Summary
		current.Status = domain.StatusExecuting
		current.CurrentStep = "execute_plan"
		current.ProgressText = fmt.Sprintf("规划完成：%d 个步骤，开始按工具计划执行", len(plan.Steps))
	})

	task, err = s.executor.Execute(ctx, task, plan)
	if err != nil {
		s.failTask(ctx, taskID, err.Error(), true)
		return
	}

	s.hub.Broadcast("artifact.updated", taskID, task.Version, task)
}

func (s *Service) runPreparedPlan(ctx context.Context, taskID string, plan domain.Plan) {
	task, err := s.store.Get(ctx, taskID)
	if err != nil {
		return
	}
	task, err = s.executor.Execute(ctx, task, plan)
	if err != nil {
		s.failTask(ctx, taskID, err.Error(), true)
		return
	}
	s.hub.Broadcast("artifact.updated", taskID, task.Version, task)
}

func (s *Service) executePlan(ctx context.Context, task domain.Task, plan domain.Plan) (domain.Task, error) {
	var contextResult tools.Result
	var docResult tools.Result
	var slidesResult tools.Result
	docGenerated := false
	slidesGenerated := false
	executed := false

	for _, step := range plan.Steps {
		if isLogicalStep(step.Tool) {
			continue
		}
		executed = true
		task = s.updateTask(ctx, task, func(current *domain.Task) {
			current.CurrentStep = step.Tool
			current.ProgressText = step.Description
			current.Steps = append(current.Steps, newStep(step.ID, toolDisplayName(step.Tool), domain.StepRunning, step.Description))
		})
		if task.TaskID == "" {
			return domain.Task{}, errors.New("任务状态更新失败")
		}

		result := s.runToolStep(ctx, task, plan, step, &contextResult, &docResult, &slidesResult, &docGenerated, &slidesGenerated)
		if !result.Success {
			return task, fmt.Errorf("%s 失败: %s", step.Tool, result.ErrorMessage)
		}

		task = s.updateTask(ctx, task, func(current *domain.Task) {
			completeLatestStep(current)
			applyToolResult(current, docResult)
			applyToolResult(current, slidesResult)
			applyToolResult(current, result)
			current.ProgressText = result.PayloadSummary
		})
		s.hub.Broadcast("artifact.updated", task.TaskID, task.Version, task)
	}

	if !executed {
		task = s.updateTask(ctx, task, func(current *domain.Task) {
			current.ProgressText = "规划结果不需要调用产物工具，已完成"
		})
	}
	return task, nil
}

func (s *Service) runToolStep(ctx context.Context, task domain.Task, plan domain.Plan, step domain.PlanStep, contextResult, docResult, slidesResult *tools.Result, docGenerated, slidesGenerated *bool) tools.Result {
	switch step.Tool {
	case "im.fetch_thread":
		*contextResult = s.tools.FetchThread(ctx, task, step)
		return *contextResult
	case "doc.create", "doc.append", "doc.generate":
		if *docGenerated {
			return s.tools.CompleteStep(step)
		}
		*docResult = s.tools.CreateDoc(ctx, plan, task.UserInstruction, *contextResult, "")
		*docGenerated = docResult.Success
		return *docResult
	case "doc.update":
		*docResult = s.tools.UpdateDoc(ctx, task, plan, task.UserInstruction, "")
		*docGenerated = docResult.Success
		return *docResult
	case "slide.generate":
		if *slidesGenerated {
			return s.tools.CompleteStep(step)
		}
		*slidesResult = s.tools.CreateSlides(ctx, plan, "")
		*slidesGenerated = slidesResult.Success
		return *slidesResult
	case "slide.regenerate":
		*slidesResult = s.tools.RegenerateSlides(ctx, task, plan, "")
		*slidesGenerated = slidesResult.Success
		return *slidesResult
	case "archive.bundle":
		return s.tools.Bundle(ctx, task, plan, *docResult, *slidesResult)
	case "sync.broadcast":
		s.hub.Broadcast("sync.broadcast", task.TaskID, task.Version, task)
		return s.tools.CompleteStep(step)
	default:
		return s.tools.CompleteStep(step)
	}
}

func (s *Service) StartAgentRun(ctx context.Context, taskID string, plan domain.Plan) (domain.Task, error) {
	task, err := s.store.Get(ctx, taskID)
	if err != nil {
		return domain.Task{}, err
	}
	task = s.updateTask(ctx, task, func(current *domain.Task) {
		current.Status = domain.StatusExecuting
		current.CurrentStep = "agent_execute"
		current.ProgressText = fmt.Sprintf("Agent is executing %d planned steps", len(plan.Steps))
		current.Summary = plan.Summary
		current.LastActor = "agent"
	})
	if task.TaskID == "" {
		return domain.Task{}, errors.New("task state update failed")
	}
	return task, nil
}

func (s *Service) StartToolStep(ctx context.Context, taskID string, step domain.PlanStep) (domain.Task, error) {
	task, err := s.store.Get(ctx, taskID)
	if err != nil {
		return domain.Task{}, err
	}
	task = s.updateTask(ctx, task, func(current *domain.Task) {
		current.CurrentStep = step.Tool
		current.ProgressText = step.Description
		current.LastActor = "agent"
		current.Steps = append(current.Steps, newStep(step.ID, toolDisplayName(step.Tool), domain.StepRunning, step.Description))
	})
	if task.TaskID == "" {
		return domain.Task{}, errors.New("task state update failed")
	}
	return task, nil
}

func (s *Service) CompleteToolStep(ctx context.Context, taskID string, result tools.Result) (domain.Task, error) {
	task, err := s.store.Get(ctx, taskID)
	if err != nil {
		return domain.Task{}, err
	}
	task = s.updateTask(ctx, task, func(current *domain.Task) {
		completeLatestStep(current)
		applyToolResult(current, result)
		if result.PayloadSummary != "" {
			current.ProgressText = result.PayloadSummary
		}
	})
	if task.TaskID == "" {
		return domain.Task{}, errors.New("task state update failed")
	}
	s.hub.Broadcast("artifact.updated", task.TaskID, task.Version, task)
	return task, nil
}

func (s *Service) FailToolStep(ctx context.Context, taskID string, step domain.PlanStep, err error) (domain.Task, error) {
	task, getErr := s.store.Get(ctx, taskID)
	if getErr != nil {
		return domain.Task{}, getErr
	}
	message := fmt.Sprintf("%s failed: %s", step.Tool, err.Error())
	task = s.updateTask(ctx, task, func(current *domain.Task) {
		failLatestStep(current, message)
		current.CurrentStep = step.Tool
		current.ProgressText = message
		current.ErrorMessage = message
	})
	if task.TaskID == "" {
		return domain.Task{}, errors.New("task state update failed")
	}
	return task, nil
}

func (s *Service) CompleteAgentRun(ctx context.Context, taskID string, plan domain.Plan) (domain.Task, error) {
	task, err := s.store.Get(ctx, taskID)
	if err != nil {
		return domain.Task{}, err
	}
	task = s.updateTask(ctx, task, func(current *domain.Task) {
		if len(current.Steps) > 0 && current.Steps[len(current.Steps)-1].Status == domain.StepRunning {
			completeLatestStep(current)
		}
		current.ProgressText = completionText(*current)
		current.Summary = plan.Summary
		if current.DocURL != "" || current.SlidesURL != "" {
			now := time.Now()
			current.CurrentStep = "awaiting_feedback"
			current.Status = domain.StatusWaitingAction
			current.RequiresAction = true
			current.LastInteractionAt = &now
		} else {
			current.CurrentStep = "completed"
			current.Status = domain.StatusCompleted
			current.RequiresAction = false
		}
	})
	if task.TaskID == "" {
		return domain.Task{}, errors.New("task state update failed")
	}
	return task, nil
}

func (s *Service) finishTask(ctx context.Context, taskID string) {
	task, err := s.store.Get(ctx, taskID)
	if err != nil {
		return
	}
	task = s.updateTask(ctx, task, func(current *domain.Task) {
		current.Status = domain.StatusCompleted
		current.CurrentStep = "completed"
		current.ProgressText = "任务经人工确认后完成"
	})
	_ = task
}

func (s *Service) endTask(ctx context.Context, task domain.Task, actorType, summary string) (domain.Task, error) {
	task = s.updateTask(ctx, task, func(current *domain.Task) {
		current.Status = domain.StatusCompleted
		current.CurrentStep = "completed"
		current.RequiresAction = false
		current.ProgressText = summary
		current.LastActor = actorType
		current.Steps = append(current.Steps, newStep("end_task", "End task", domain.StepCompleted, summary))
	})
	if task.TaskID == "" {
		return domain.Task{}, errors.New("task state update failed")
	}
	s.hub.Broadcast("action.resolved", task.TaskID, task.Version, task)
	return task, nil
}

func canActorEndTask(task domain.Task, input ActionInput) bool {
	if input.ActorType != "feishu_card" {
		return true
	}
	if task.InitiatorUserID == "" && task.InitiatorOpenID == "" && task.InitiatorUnionID == "" {
		return true
	}
	return sameNonEmptyID(task.InitiatorUserID, input.ActorUserID) ||
		sameNonEmptyID(task.InitiatorOpenID, input.ActorOpenID) ||
		sameNonEmptyID(task.InitiatorUnionID, input.ActorUnionID)
}

func sameNonEmptyID(expected, actual string) bool {
	return strings.TrimSpace(expected) != "" && strings.TrimSpace(expected) == strings.TrimSpace(actual)
}

func (s *Service) failTask(ctx context.Context, taskID, message string, requiresAction bool) {
	task, err := s.store.Get(ctx, taskID)
	if err != nil {
		return
	}

	task = s.updateTask(ctx, task, func(current *domain.Task) {
		failLatestStep(current, message)
		current.Status = domain.StatusFailed
		current.CurrentStep = "failed"
		current.ProgressText = message
		current.ErrorMessage = message
		current.RequiresAction = requiresAction
	})
	if requiresAction {
		s.hub.Broadcast("action.required", task.TaskID, task.Version, task)
	}
}

func (s *Service) updateTask(ctx context.Context, task domain.Task, mutate func(*domain.Task)) domain.Task {
	mutate(&task)
	task.Version++
	task.UpdatedAt = time.Now()
	task, err := s.store.Update(ctx, task)
	if err != nil {
		return domain.Task{}
	}
	s.hub.Broadcast("task.updated", task.TaskID, task.Version, task)
	return task
}

func newStep(id, name string, status domain.StepStatus, summary string) domain.Step {
	now := time.Now()
	step := domain.Step{
		ID:             id + "-" + uuid.NewString()[:8],
		Name:           name,
		Status:         status,
		PayloadSummary: summary,
	}
	if status == domain.StepRunning {
		step.StartedAt = &now
	}
	if status == domain.StepCompleted {
		step.StartedAt = &now
		step.CompletedAt = &now
	}
	return step
}

func (s *Service) upsertSession(ctx context.Context, sessionID string, task domain.Task) error {
	history, ok := s.store.(store.HistoryRepository)
	if !ok || strings.TrimSpace(sessionID) == "" {
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

func applyToolResult(task *domain.Task, result tools.Result) {
	if result.StepName == "" {
		return
	}
	if result.ArtifactURL != "" {
		switch {
		case strings.HasPrefix(result.StepName, "doc."):
			task.DocURL = result.ArtifactURL
		case strings.HasPrefix(result.StepName, "slide."):
			task.SlidesURL = result.ArtifactURL
		}
	}
	if result.ArtifactPath != "" {
		switch {
		case strings.HasPrefix(result.StepName, "doc."):
			task.DocArtifactPath = result.ArtifactPath
		case result.StepName == "slide.generate" || result.StepName == "slide.regenerate":
			task.SlidesArtifactPath = result.ArtifactPath
		}
	}
	if result.Data == nil {
		return
	}
	if docID := result.Data["feishu_document_id"]; docID != "" {
		task.DocID = docID
	}
	if path := result.Data["local_path"]; path != "" && strings.HasPrefix(result.StepName, "doc.") {
		task.DocArtifactPath = path
	}
}

func revisionInstruction(original, revision string) string {
	original = strings.TrimSpace(original)
	revision = strings.TrimSpace(revision)
	if original == "" {
		return revision
	}
	return original + "\n\nRevision request: " + revision
}

func (s *Service) buildRevisionPlan(ctx context.Context, task domain.Task, instruction string) domain.Plan {
	if revisionPlanner, ok := s.planner.(planner.RevisionBuilder); ok {
		plan, err := revisionPlanner.BuildRevisionPlan(ctx, task, instruction)
		if err == nil {
			return plan
		}
		plan = planner.BuildHeuristicRevisionPlan(task, instruction)
		plan.PlannerSource = "revision_fallback"
		plan.PlannerError = err.Error()
		return plan
	}
	return planner.BuildHeuristicRevisionPlan(task, instruction)
}

func sessionIDForTask(task domain.Task) string {
	if task.ChatID != "" {
		return "chat:" + task.ChatID
	}
	return "task:" + task.TaskID
}

func completeLatestStep(task *domain.Task) {
	if len(task.Steps) == 0 {
		return
	}
	now := time.Now()
	last := &task.Steps[len(task.Steps)-1]
	last.Status = domain.StepCompleted
	last.CompletedAt = &now
	if last.StartedAt == nil {
		last.StartedAt = &now
	}
}

func failLatestStep(task *domain.Task, message string) {
	if len(task.Steps) == 0 {
		return
	}
	now := time.Now()
	last := &task.Steps[len(task.Steps)-1]
	last.Status = domain.StepFailed
	last.ErrorMessage = message
	last.CompletedAt = &now
	if last.StartedAt == nil {
		last.StartedAt = &now
	}
}

func fallback(value, defaultValue string) string {
	if value == "" {
		return defaultValue
	}
	return value
}

func isLogicalStep(tool string) bool {
	switch tool {
	case "intent.analyze", "planner.build":
		return true
	default:
		return false
	}
}

func toolDisplayName(tool string) string {
	switch tool {
	case "doc.update":
		return "更新文档"
	case "slide.regenerate":
		return "更新PPT"
	case "im.fetch_thread":
		return "读取 IM 上下文"
	case "doc.create":
		return "创建文档"
	case "doc.append", "doc.generate":
		return "写入文档"
	case "slide.generate":
		return "生成演示稿"
	case "archive.bundle":
		return "汇总产物"
	case "sync.broadcast":
		return "广播状态"
	default:
		return tool
	}
}

func completionText(task domain.Task) string {
	switch {
	case task.DocURL != "" && task.SlidesURL != "":
		return "文档、演示稿与产物清单均已生成"
	case task.DocURL != "":
		return "文档已生成"
	case task.SlidesURL != "":
		return "演示稿已生成"
	default:
		return "无需生成文档或演示稿，任务已完成"
	}
}
