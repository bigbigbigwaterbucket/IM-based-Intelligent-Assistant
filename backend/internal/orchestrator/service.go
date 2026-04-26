package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

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
}

type ActionInput struct {
	ActionType string `json:"actionType"`
	ActorType  string `json:"actorType"`
	ClientID   string `json:"clientId"`
}

type Service struct {
	store   store.TaskRepository
	hub     *statehub.Hub
	planner *planner.Service
	tools   *tools.Runner
}

func New(taskStore store.TaskRepository, hub *statehub.Hub, plannerSvc *planner.Service, toolRunner *tools.Runner) *Service {
	return &Service{
		store:   taskStore,
		hub:     hub,
		planner: plannerSvc,
		tools:   toolRunner,
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
		current.CurrentStep = "generate_doc"
		current.ProgressText = fmt.Sprintf("规划完成：%d 个步骤，正在生成方案文档", len(plan.Steps))
		current.Steps = append(current.Steps, newStep("generate_doc", "生成方案文档", domain.StepRunning, plan.DocTitle))
	})

	docResult := s.tools.CreateDoc(ctx, plan, task.UserInstruction)
	if !docResult.Success {
		s.failTask(ctx, taskID, "文档生成失败: "+docResult.ErrorMessage, true)
		return
	}

	task = s.updateTask(ctx, task, func(current *domain.Task) {
		completeLatestStep(current)
		current.DocURL = docResult.ArtifactURL
		current.ProgressText = "文档已生成，正在生成演示稿与演讲稿"
		current.CurrentStep = "generate_slides"
		current.Steps = append(current.Steps, newStep("generate_slides", "生成演示稿", domain.StepRunning, plan.SlideTitle))
	})
	s.hub.Broadcast("artifact.updated", taskID, task.Version, task)

	slidesResult := s.tools.CreateSlides(ctx, plan)
	if !slidesResult.Success {
		s.failTask(ctx, taskID, "演示稿生成失败: "+slidesResult.ErrorMessage, true)
		return
	}

	task = s.updateTask(ctx, task, func(current *domain.Task) {
		completeLatestStep(current)
		current.SlidesURL = slidesResult.ArtifactURL
		current.ProgressText = "演示稿已生成，正在汇总产物"
		current.CurrentStep = "archive_bundle"
		current.Steps = append(current.Steps, newStep("archive_bundle", "汇总产物", domain.StepRunning, "生成 manifest 并整理交付链接"))
	})
	s.hub.Broadcast("artifact.updated", taskID, task.Version, task)

	bundleResult := s.tools.Bundle(ctx, task, plan, docResult, slidesResult)
	if !bundleResult.Success {
		s.failTask(ctx, taskID, "产物汇总失败: "+bundleResult.ErrorMessage, true)
		return
	}

	task = s.updateTask(ctx, task, func(current *domain.Task) {
		completeLatestStep(current)
		current.ProgressText = "文档、演示稿与产物清单均已生成"
		current.CurrentStep = "completed"
		current.Status = domain.StatusCompleted
		current.Summary = plan.Summary
		current.RequiresAction = false
	})

	s.hub.Broadcast("artifact.updated", taskID, task.Version, task)
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
