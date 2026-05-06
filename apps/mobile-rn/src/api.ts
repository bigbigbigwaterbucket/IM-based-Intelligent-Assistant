import type {
  CollabDocument,
  CollabExportRequest,
  CollabSnapshotRequest,
  CollabState,
  CollabUpdate,
  ConnectionStatus,
  EventEnvelope,
  Task,
  TaskActionRequest,
} from "@agent-pilot/shared";
import { normalizeTask, normalizeTasks } from "@agent-pilot/shared";

const API_BASE = process.env.EXPO_PUBLIC_API_BASE ?? "";
const WS_BASE = process.env.EXPO_PUBLIC_WS_BASE ?? "";

function requireAPIBase(): string {
  if (!API_BASE) {
    throw new Error("请配置 EXPO_PUBLIC_API_BASE，例如 http://192.168.1.20:8080");
  }
  return API_BASE.replace(/\/$/, "");
}

function requireWSBase(): string {
  if (!WS_BASE) {
    throw new Error("请配置 EXPO_PUBLIC_WS_BASE，例如 ws://192.168.1.20:8080");
  }
  return WS_BASE.replace(/\/$/, "");
}

export async function listTasks(): Promise<Task[]> {
  const response = await fetch(`${requireAPIBase()}/tasks`);
  if (!response.ok) {
    throw new Error("Failed to load tasks");
  }
  return normalizeTasks((await response.json()) as unknown);
}

export async function sendTaskAction(taskId: string, payload: TaskActionRequest): Promise<Task> {
  const response = await fetch(`${requireAPIBase()}/tasks/${taskId}/actions`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify(payload),
  });
  if (!response.ok) {
    throw new Error("Failed to send action");
  }
  const task = normalizeTask((await response.json()) as unknown);
  if (!task) {
    throw new Error("Invalid task payload");
  }
  return task;
}

export async function loadMarkdownDocument(taskId: string): Promise<CollabDocument> {
  const response = await fetch(`${requireAPIBase()}/tasks/${taskId}/documents/markdown`);
  if (!response.ok) {
    throw new Error("Failed to load markdown document");
  }
  return (await response.json()) as CollabDocument;
}

export async function loadCollabState(docKey: string): Promise<CollabState> {
  const response = await fetch(`${requireAPIBase()}/collab/docs/${encodeURIComponent(docKey)}/state`);
  if (!response.ok) {
    throw new Error("Failed to load collaborative state");
  }
  return (await response.json()) as CollabState;
}

export async function loadCollabUpdates(docKey: string, sinceSeq: number): Promise<CollabUpdate[]> {
  const response = await fetch(`${requireAPIBase()}/collab/docs/${encodeURIComponent(docKey)}/updates?sinceSeq=${sinceSeq}`);
  if (!response.ok) {
    throw new Error("Failed to load collaborative updates");
  }
  const payload = (await response.json()) as unknown;
  return Array.isArray(payload) ? (payload as CollabUpdate[]) : [];
}

export async function saveCollabSnapshot(docKey: string, payload: CollabSnapshotRequest): Promise<CollabDocument> {
  const response = await fetch(`${requireAPIBase()}/collab/docs/${encodeURIComponent(docKey)}/snapshot`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
  if (!response.ok) {
    throw new Error("Failed to save snapshot");
  }
  return (await response.json()) as CollabDocument;
}

export async function exportCollabMarkdown(docKey: string, payload: CollabExportRequest): Promise<CollabDocument> {
  const response = await fetch(`${requireAPIBase()}/collab/docs/${encodeURIComponent(docKey)}/export`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
  if (!response.ok) {
    throw new Error("Failed to export markdown");
  }
  return (await response.json()) as CollabDocument;
}

export function connectEvents(
  onEvent: (event: EventEnvelope<Task>) => void,
  onStatus: (status: ConnectionStatus) => void,
  onReconnect?: () => void,
): () => void {
  let socket: WebSocket | undefined;
  let retryTimer: ReturnType<typeof setTimeout> | undefined;
  let closed = false;
  let attempts = 0;

  function connect() {
    if (closed) {
      return;
    }
    let wsURL = "";
    try {
      wsURL = `${requireWSBase()}/ws`;
    } catch {
      onStatus("offline");
      return;
    }

    onStatus(attempts === 0 ? "connecting" : "reconnecting");
    socket = new WebSocket(wsURL);

    socket.onopen = () => {
      attempts = 0;
      onStatus("online");
      onReconnect?.();
    };

    socket.onmessage = (message) => {
      const event = JSON.parse(String(message.data)) as EventEnvelope<unknown>;
      const payload = normalizeTask(event?.payload);
      if (!payload) {
        return;
      }
      onEvent({
        ...event,
        payload,
      });
    };

    socket.onclose = () => {
      if (closed) {
        return;
      }
      attempts += 1;
      onStatus("reconnecting");
      retryTimer = setTimeout(connect, Math.min(8000, 1000 * attempts));
    };

    socket.onerror = () => {
      socket?.close();
    };
  }

  connect();

  return () => {
    closed = true;
    if (retryTimer) {
      clearTimeout(retryTimer);
    }
    socket?.close();
  };
}

export function collabSocketURL(docKey: string, clientId: string): string {
  return `${requireWSBase()}/collab/docs/${encodeURIComponent(docKey)}/ws?clientId=${encodeURIComponent(clientId)}`;
}
