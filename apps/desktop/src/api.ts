import type {
  EventEnvelope,
  Task,
  TaskActionRequest,
  TaskCreateRequest,
} from "@agent-pilot/shared";

const API_BASE = import.meta.env.VITE_API_BASE ?? "http://localhost:8080";
const WS_BASE = (import.meta.env.VITE_WS_BASE ?? "ws://localhost:8080") + "/ws";

function normalizeTask(task: unknown): Task | null {
  if (!task || typeof task !== "object") {
    return null;
  }
  const candidate = task as Task;
  return {
    ...candidate,
    steps: Array.isArray(candidate.steps) ? candidate.steps : [],
  };
}

export async function listTasks(): Promise<Task[]> {
  const response = await fetch(`${API_BASE}/tasks`);
  if (!response.ok) {
    throw new Error("Failed to load tasks");
  }
  const data = (await response.json()) as unknown;
  return Array.isArray(data) ? data.map(normalizeTask).filter((task): task is Task => task !== null) : [];
}

export async function createTask(payload: TaskCreateRequest): Promise<Task> {
  const response = await fetch(`${API_BASE}/tasks`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(payload),
  });
  if (!response.ok) {
    throw new Error("Failed to create task");
  }
  const task = normalizeTask((await response.json()) as unknown);
  if (!task) {
    throw new Error("Invalid task payload");
  }
  return task;
}

export async function sendTaskAction(taskId: string, payload: TaskActionRequest): Promise<Task> {
  const response = await fetch(`${API_BASE}/tasks/${taskId}/actions`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(payload),
  });
  if (!response.ok) {
    throw new Error("Failed to submit task action");
  }
  const task = normalizeTask((await response.json()) as unknown);
  if (!task) {
    throw new Error("Invalid task payload");
  }
  return task;
}

export function connectEvents(onEvent: (event: EventEnvelope<Task>) => void): () => void {
  const socket = new WebSocket(WS_BASE);
  socket.onmessage = (message) => {
    const event = JSON.parse(message.data) as EventEnvelope<unknown>;
    const payload = normalizeTask(event?.payload);
    if (!payload) {
      return;
    }
    onEvent({
      ...event,
      payload,
    });
  };
  return () => {
    socket.close();
  };
}
