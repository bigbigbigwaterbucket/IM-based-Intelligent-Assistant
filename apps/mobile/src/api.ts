import type { EventEnvelope, Task, TaskActionRequest } from "@agent-pilot/shared";

const API_BASE = import.meta.env.VITE_API_BASE ?? "http://localhost:8080";
const WS_BASE = (import.meta.env.VITE_WS_BASE ?? "ws://localhost:8080") + "/ws";

export async function listTasks(): Promise<Task[]> {
  const response = await fetch(`${API_BASE}/tasks`);
  if (!response.ok) {
    throw new Error("Failed to load tasks");
  }
  return response.json();
}

export async function sendTaskAction(taskId: string, payload: TaskActionRequest): Promise<void> {
  const response = await fetch(`${API_BASE}/tasks/${taskId}/actions`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(payload),
  });
  if (!response.ok) {
    throw new Error("Failed to send action");
  }
}

export function connectEvents(onEvent: (event: EventEnvelope<Task>) => void): () => void {
  const socket = new WebSocket(WS_BASE);
  socket.onmessage = (message) => {
    const event = JSON.parse(message.data) as EventEnvelope<Task>;
    onEvent(event);
  };
  return () => socket.close();
}

