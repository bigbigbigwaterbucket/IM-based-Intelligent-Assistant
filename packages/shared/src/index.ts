export type TaskStatus =
  | "created"
  | "planning"
  | "executing"
  | "waiting_action"
  | "completed"
  | "failed";

export type ActionType = "retry_task" | "approve_continue" | "open_artifact" | "end_task";

export type ConnectionStatus = "connecting" | "online" | "offline" | "reconnecting";

export type EventType =
  | "task.created"
  | "task.updated"
  | "step.started"
  | "step.completed"
  | "step.failed"
  | "artifact.updated"
  | "action.required"
  | "action.resolved"
  | "sync.broadcast";

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
  source: string;
  status: TaskStatus;
  currentStep: string;
  progressText: string;
  docUrl?: string;
  slidesUrl?: string;
  docId?: string;
  docArtifactPath?: string;
  slidesArtifactPath?: string;
  summary?: string;
  requiresAction: boolean;
  errorMessage?: string;
  version: number;
  lastActor: string;
  lastInteractionAt?: string;
  idlePromptedAt?: string;
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

export interface CollabDocument {
  docKey: string;
  taskId: string;
  kind: "markdown";
  title: string;
  sourcePath?: string;
  snapshotSeq: number;
  snapshotUpdateBase64?: string;
  markdownCache: string;
  editable: boolean;
  createdAt: string;
  updatedAt: string;
}

export interface CollabState {
  docKey: string;
  snapshotSeq: number;
  snapshotUpdateBase64?: string;
}

export interface CollabUpdate {
  docKey: string;
  seq: number;
  clientId: string;
  updateBase64: string;
  createdAt: string;
}

export interface CollabSnapshotRequest {
  baseSeq: number;
  snapshotUpdateBase64: string;
  markdownCache: string;
  clientId: string;
}

export interface CollabUpdateRequest {
  clientId: string;
  updateBase64: string;
}

export interface CollabExportRequest {
  markdown: string;
  baseSeq: number;
  snapshotUpdateBase64: string;
  clientId: string;
}

export interface EventEnvelope<T = unknown> {
  eventType: EventType;
  taskId: string;
  version: number;
  payload: T;
  emittedAt: string;
}

export const taskStatusLabel: Record<TaskStatus, string> = {
  created: "已创建",
  planning: "规划中",
  executing: "执行中",
  waiting_action: "待确认",
  completed: "已完成",
  failed: "失败",
};

export const stepStatusLabel: Record<StepRecord["status"], string> = {
  pending: "待执行",
  running: "执行中",
  completed: "已完成",
  failed: "失败",
};

export const connectionStatusLabel: Record<ConnectionStatus, string> = {
  connecting: "连接中",
  online: "在线同步",
  offline: "离线缓存",
  reconnecting: "重连中",
};

export function normalizeTask(task: unknown): Task | null {
  if (!task || typeof task !== "object") {
    return null;
  }
  const candidate = task as Partial<Task>;
  if (!candidate.taskId || !candidate.title) {
    return null;
  }
  return {
    taskId: candidate.taskId,
    title: candidate.title,
    userInstruction: candidate.userInstruction ?? "",
    source: candidate.source ?? "system",
    status: candidate.status ?? "created",
    currentStep: candidate.currentStep ?? "",
    progressText: candidate.progressText ?? "",
    docUrl: candidate.docUrl,
    slidesUrl: candidate.slidesUrl,
    docId: candidate.docId,
    docArtifactPath: candidate.docArtifactPath,
    slidesArtifactPath: candidate.slidesArtifactPath,
    summary: candidate.summary,
    requiresAction: Boolean(candidate.requiresAction),
    errorMessage: candidate.errorMessage,
    version: Number(candidate.version ?? 0),
    lastActor: candidate.lastActor ?? "system",
    lastInteractionAt: candidate.lastInteractionAt,
    idlePromptedAt: candidate.idlePromptedAt,
    createdAt: candidate.createdAt ?? "",
    updatedAt: candidate.updatedAt ?? candidate.createdAt ?? "",
    steps: Array.isArray(candidate.steps) ? candidate.steps.map(normalizeStep).filter(isStep) : [],
  };
}

export function normalizeTasks(payload: unknown): Task[] {
  return Array.isArray(payload)
    ? sortTasks(payload.map(normalizeTask).filter((task): task is Task => task !== null))
    : [];
}

export function mergeTask(tasks: Task[], next: Task): Task[] {
  const currentTasks = Array.isArray(tasks) ? tasks : [];
  const currentIndex = currentTasks.findIndex((task) => task.taskId === next.taskId);
  if (currentIndex === -1) {
    return sortTasks([next, ...currentTasks]);
  }

  const current = currentTasks[currentIndex];
  if (current.version > next.version) {
    return sortTasks(currentTasks);
  }

  const clone = currentTasks.slice();
  clone[currentIndex] = next;
  return sortTasks(clone);
}

export function mergeTasks(current: Task[], incoming: Task[]): Task[] {
  return incoming.reduce((tasks, task) => mergeTask(tasks, task), Array.isArray(current) ? current : []);
}

export function sortTasks(tasks: Task[]): Task[] {
  return tasks.slice().sort((a, b) => getTime(b.updatedAt || b.createdAt) - getTime(a.updatedAt || a.createdAt));
}

export function getTaskProgress(task: Task): number {
  if (task.status === "completed") {
    return 100;
  }
  const steps = Array.isArray(task.steps) ? task.steps : [];
  if (steps.length === 0) {
    return task.status === "created" ? 5 : 10;
  }
  const completed = steps.filter((step) => step.status === "completed").length;
  const runningBonus = steps.some((step) => step.status === "running") ? 0.5 : 0;
  return Math.min(99, Math.max(5, Math.round(((completed + runningBonus) / steps.length) * 100)));
}

export function formatTaskTime(value?: string): string {
  if (!value) {
    return "-";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "-";
  }
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

export function loadCachedTasks(cacheKey: string): Task[] {
  if (typeof localStorage === "undefined") {
    return [];
  }
  try {
    return normalizeTasks(JSON.parse(localStorage.getItem(cacheKey) ?? "[]"));
  } catch {
    return [];
  }
}

export function saveCachedTasks(cacheKey: string, tasks: Task[]): void {
  if (typeof localStorage === "undefined") {
    return;
  }
  localStorage.setItem(cacheKey, JSON.stringify(sortTasks(tasks)));
}

export {
  base64ToBytes,
  bytesToBase64,
  createMarkdownCollabSession,
} from "./collab";
export type {
  CollabSocketUpdate,
  CollabTransport,
  MarkdownCollabSession,
} from "./collab";

function normalizeStep(step: StepRecord): StepRecord | null {
  if (!step || typeof step !== "object" || !step.id) {
    return null;
  }
  return {
    id: step.id,
    name: step.name ?? "未命名步骤",
    status: step.status ?? "pending",
    payloadSummary: step.payloadSummary ?? "",
    errorMessage: step.errorMessage,
    startedAt: step.startedAt,
    completedAt: step.completedAt,
  };
}

function isStep(step: StepRecord | null): step is StepRecord {
  return step !== null;
}

function getTime(value?: string): number {
  if (!value) {
    return 0;
  }
  const time = new Date(value).getTime();
  return Number.isNaN(time) ? 0 : time;
}
