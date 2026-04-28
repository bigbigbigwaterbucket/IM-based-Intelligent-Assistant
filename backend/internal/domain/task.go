package domain

import "time"

type TaskStatus string

const (
	StatusCreated       TaskStatus = "created"
	StatusPlanning      TaskStatus = "planning"
	StatusExecuting     TaskStatus = "executing"
	StatusWaitingAction TaskStatus = "waiting_action"
	StatusCompleted     TaskStatus = "completed"
	StatusFailed        TaskStatus = "failed"
)

type StepStatus string

const (
	StepPending   StepStatus = "pending"
	StepRunning   StepStatus = "running"
	StepCompleted StepStatus = "completed"
	StepFailed    StepStatus = "failed"
)

type Task struct {
	TaskID          string     `json:"taskId"`
	Title           string     `json:"title"`
	UserInstruction string     `json:"userInstruction"`
	Source          string     `json:"source"`
	ChatID          string     `json:"chatId,omitempty"`
	ThreadID        string     `json:"threadId,omitempty"`
	MessageID       string     `json:"messageId,omitempty"`
	Status          TaskStatus `json:"status"`
	CurrentStep     string     `json:"currentStep"`
	ProgressText    string     `json:"progressText"`
	DocURL          string     `json:"docUrl,omitempty"`
	SlidesURL       string     `json:"slidesUrl,omitempty"`
	Summary         string     `json:"summary,omitempty"`
	RequiresAction  bool       `json:"requiresAction"`
	ErrorMessage    string     `json:"errorMessage,omitempty"`
	Version         int        `json:"version"`
	LastActor       string     `json:"lastActor"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
	Steps           []Step     `json:"steps"`
}

type Step struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Status         StepStatus `json:"status"`
	PayloadSummary string     `json:"payloadSummary"`
	ErrorMessage   string     `json:"errorMessage,omitempty"`
	StartedAt      *time.Time `json:"startedAt,omitempty"`
	CompletedAt    *time.Time `json:"completedAt,omitempty"`
}

type Plan struct {
	Summary          string
	PlannerSource    string
	PlannerError     string
	Analysis         IntentAnalysis
	Steps            []PlanStep
	DocTitle         string
	SlideTitle       string
	DocumentSections []DocumentSection
	Slides           []Slide
}

type IntentAnalysis struct {
	Objective      string
	Audience       string
	Deliverables   []string
	ContextNeeded  bool
	Risks          []string
	ClarifyingHint string
}

type PlanStep struct {
	ID          string
	Tool        string
	Description string
	Args        map[string]any
	DependsOn   []string
}

type DocumentSection struct {
	Heading string
	Bullets []string
}

type Slide struct {
	Title       string
	Bullets     []string
	SpeakerNote string
}

type Session struct {
	SessionID string    `json:"sessionId"`
	TaskID    string    `json:"taskId"`
	ChatID    string    `json:"chatId,omitempty"`
	ThreadID  string    `json:"threadId,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type ConversationMessage struct {
	MessageID string    `json:"messageId"`
	SessionID string    `json:"sessionId"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Metadata  string    `json:"metadata,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

type ToolInvocation struct {
	InvocationID   string    `json:"invocationId"`
	SessionID      string    `json:"sessionId"`
	TaskID         string    `json:"taskId"`
	StepID         string    `json:"stepId"`
	ToolName       string    `json:"toolName"`
	ArgumentsJSON  string    `json:"argumentsJson"`
	ResultSummary  string    `json:"resultSummary"`
	ResultJSON     string    `json:"resultJson"`
	ErrorMessage   string    `json:"errorMessage,omitempty"`
	ArtifactURL    string    `json:"artifactUrl,omitempty"`
	ArtifactPath   string    `json:"artifactPath,omitempty"`
	StartedAt      time.Time `json:"startedAt"`
	CompletedAt    time.Time `json:"completedAt"`
	DurationMillis int64     `json:"durationMillis"`
}

type ActionType string

const (
	ActionRetryTask       ActionType = "retry_task"
	ActionApproveContinue ActionType = "approve_continue"
	ActionOpenArtifact    ActionType = "open_artifact"
)
