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
	Title       string `json:"title"`
	Instruction string `json:"instruction"`
	Source      string `json:"source"`
	ChatID      string `json:"chatId"`
	ThreadID    string `json:"threadId"`
	MessageID   string `json:"messageId"`
}

type ActionInput struct {
	ActionType string `json:"actionType"`
	ActorType  string `json:"actorType"`
	ClientID   string `json:"clientId"`
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
		TaskID:          uuid.NewString(),
		Title:           input.Title,
		UserInstruction: input.Instruction,
		Source:          fallback(input.Source, "desktop"),
		ChatID:          input.ChatID,
		ThreadID:        input.ThreadID,
		MessageID:       input.MessageID,
		Status:          domain.StatusCreated,
		CurrentStep:     "created",
		ProgressText:    "任务已创建，等待规划",
		Version:         1,
		LastActor:       "desktop",
		CreatedAt:       now,
		UpdatedAt:       now,
		Steps: []domain.Step{
			newStep("capture", "任务创建", domain.StepCompleted, "来自桌面端手动创建"),
		},
	}

	task, err := s.store.Create(ctx, task)
	if err != nil {
		return domain.Task{}, err
	}
	s.hub.Broadcast("task.created", task.TaskID, task.Version, task)

	go s.runTask(context.Background(), task.TaskID)
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
			return domain.Task{}, errors.New("only failed tasks can be retried")
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
	default:
		return domain.Task{}, errors.New("unsupported action")
	}
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
		if task.Status == domain.StatusCompleted || task.Status == domain.StatusFailed {
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
			if docResult.ArtifactURL != "" {
				current.DocURL = docResult.ArtifactURL
			}
			if slidesResult.ArtifactURL != "" {
				current.SlidesURL = slidesResult.ArtifactURL
			}
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
	case "im.fetch_thread", "im.context_summarize":
		*contextResult = s.tools.FetchThread(ctx, task, step)
		return *contextResult
	case "doc.create", "doc.append", "doc.generate":
		if *docGenerated {
			return s.tools.CompleteStep(step)
		}
		*docResult = s.tools.CreateDoc(ctx, plan, task.UserInstruction, *contextResult)
		*docGenerated = docResult.Success
		return *docResult
	case "slide.generate":
		if *slidesGenerated {
			return s.tools.CompleteStep(step)
		}
		*slidesResult = s.tools.CreateSlides(ctx, plan)
		*slidesGenerated = slidesResult.Success
		return *slidesResult
	case "slide.rehearse":
		if *slidesGenerated {
			return s.tools.CompleteStep(step)
		}
		*slidesResult = s.tools.CreateSlides(ctx, plan)
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
		if result.ArtifactURL != "" {
			switch {
			case strings.HasPrefix(result.StepName, "doc."):
				current.DocURL = result.ArtifactURL
			case strings.HasPrefix(result.StepName, "slide."):
				current.SlidesURL = result.ArtifactURL
			}
		}
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
		current.CurrentStep = "completed"
		current.Status = domain.StatusCompleted
		current.Summary = plan.Summary
		current.RequiresAction = false
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
	case "im.fetch_thread", "im.context_summarize":
		return "读取 IM 上下文"
	case "doc.create":
		return "创建文档"
	case "doc.append", "doc.generate":
		return "写入文档"
	case "slide.generate":
		return "生成演示稿"
	case "slide.rehearse":
		return "生成演讲稿"
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
