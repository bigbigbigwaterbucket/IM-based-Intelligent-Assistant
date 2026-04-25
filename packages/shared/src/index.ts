export type TaskStatus =
  | "created"
  | "planning"
  | "executing"
  | "waiting_action"
  | "completed"
  | "failed";

export type ActionType = "retry_task" | "approve_continue" | "open_artifact";

export type EventType =
  | "task.created"
  | "task.updated"
  | "step.started"
  | "step.completed"
  | "step.failed"
  | "artifact.updated"
  | "action.required"
  | "action.resolved";

export interface StepRecord {
  id: string;
  name: string;
  status: "pending" | "running" | "completed" | "failed";
  payloadSummary: string;
  errorMessage?: string;
  startedAt?: string;
  completedAt?: string;
}

export interface Task {
  taskId: string;
  title: string;
  userInstruction: string;
  source: "desktop" | "mobile" | "system";
  status: TaskStatus;
  currentStep: string;
  progressText: string;
  docUrl?: string;
  slidesUrl?: string;
  summary?: string;
  requiresAction: boolean;
  errorMessage?: string;
  version: number;
  lastActor: "agent" | "desktop" | "mobile" | "system";
  createdAt: string;
  updatedAt: string;
  steps: StepRecord[];
}

export interface TaskActionRequest {
  actionType: ActionType;
  actorType: "desktop" | "mobile";
  clientId: string;
}

export interface TaskCreateRequest {
  title: string;
  instruction: string;
  source?: "desktop" | "mobile";
}

export interface EventEnvelope<T = unknown> {
  eventType: EventType;
  taskId: string;
  version: number;
  payload: T;
  emittedAt: string;
}

