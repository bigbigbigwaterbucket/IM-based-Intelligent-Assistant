import type {
  CollabDocument,
  CollabExportRequest,
  CollabSnapshotRequest,
  CollabState,
  CollabUpdate,
  CollabUpdateRequest,
  ConnectionStatus,
  EventEnvelope,
  Task,
  TaskActionRequest,
} from "@agent-pilot/shared";
import { normalizeTask, normalizeTasks } from "@agent-pilot/shared";

const API_BASE = import.meta.env.VITE_API_BASE ?? "http://localhost:8080";
const WS_BASE = (import.meta.env.VITE_WS_BASE ?? "ws://localhost:8080") + "/ws";
const COLLAB_WS_BASE = import.meta.env.VITE_WS_BASE ?? "ws://localhost:8080";

export async function listTasks(): Promise<Task[]> {
  const response = await fetch(`${API_BASE}/tasks`);
  if (!response.ok) {
    throw new Error("Failed to load tasks");
  }
  return normalizeTasks((await response.json()) as unknown);
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
    throw new Error("Failed to send action");
  }
  const task = normalizeTask((await response.json()) as unknown);
  if (!task) {
    throw new Error("Invalid task payload");
  }
  return task;
}

export async function loadMarkdownDocument(taskId: string): Promise<CollabDocument> {
  const response = await fetch(`${API_BASE}/tasks/${taskId}/documents/markdown`);
  if (!response.ok) {
    throw new Error("Failed to load markdown document");
  }
  return (await response.json()) as CollabDocument;
}

export async function loadCollabState(docKey: string): Promise<CollabState> {
  const response = await fetch(`${API_BASE}/collab/docs/${encodeURIComponent(docKey)}/state`);
  if (!response.ok) {
    throw new Error("Failed to load collaborative state");
  }
  return (await response.json()) as CollabState;
}

export async function loadCollabUpdates(docKey: string, sinceSeq: number): Promise<CollabUpdate[]> {
  const response = await fetch(`${API_BASE}/collab/docs/${encodeURIComponent(docKey)}/updates?sinceSeq=${sinceSeq}`);
  if (!response.ok) {
    throw new Error("Failed to load collaborative updates");
  }
  const payload = (await response.json()) as unknown;
  return Array.isArray(payload) ? (payload as CollabUpdate[]) : [];
}

export async function appendCollabUpdate(docKey: string, payload: CollabUpdateRequest): Promise<CollabUpdate> {
  const response = await fetch(`${API_BASE}/collab/docs/${encodeURIComponent(docKey)}/updates`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
  if (!response.ok) {
    throw new Error("Failed to append collaborative update");
  }
  return (await response.json()) as CollabUpdate;
}

export async function saveCollabSnapshot(docKey: string, payload: CollabSnapshotRequest): Promise<CollabDocument> {
  const response = await fetch(`${API_BASE}/collab/docs/${encodeURIComponent(docKey)}/snapshot`, {
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
  const response = await fetch(`${API_BASE}/collab/docs/${encodeURIComponent(docKey)}/export`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
  if (!response.ok) {
    throw new Error("Failed to export markdown");
  }
  return (await response.json()) as CollabDocument;
}

export function connectCollabDoc(
  docKey: string,
  clientId: string,
  onUpdate: (message: { type: "update"; docKey: string; seq: number; clientId: string; updateBase64: string }) => void,
): () => void {
  const socket = new WebSocket(`${COLLAB_WS_BASE}/collab/docs/${encodeURIComponent(docKey)}/ws?clientId=${encodeURIComponent(clientId)}`);
  socket.onmessage = (event) => {
    const message = JSON.parse(event.data as string) as { type?: string; docKey?: string; seq?: number; clientId?: string; updateBase64?: string };
    if (message.type === "update" && message.docKey && message.updateBase64 && typeof message.seq === "number" && message.clientId) {
      onUpdate(message as { type: "update"; docKey: string; seq: number; clientId: string; updateBase64: string });
    }
  };
  return () => socket.close();
}

export function connectEvents(
  onEvent: (event: EventEnvelope<Task>) => void,
  onStatus: (status: ConnectionStatus) => void,
  onReconnect?: () => void,
): () => void {
  let socket: WebSocket | undefined;
  let retryTimer: number | undefined;
  let closed = false;
  let attempts = 0;

  function connect() {
    if (closed) {
      return;
    }
    if (typeof navigator !== "undefined" && !navigator.onLine) {
      onStatus("offline");
      return;
    }

    onStatus(attempts === 0 ? "connecting" : "reconnecting");
    socket = new WebSocket(WS_BASE);

    socket.onopen = () => {
      attempts = 0;
      onStatus("online");
      onReconnect?.();
    };

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

    socket.onclose = () => {
      if (closed) {
        return;
      }
      attempts += 1;
      onStatus(typeof navigator !== "undefined" && !navigator.onLine ? "offline" : "reconnecting");
      retryTimer = window.setTimeout(connect, Math.min(8000, 1000 * attempts));
    };

    socket.onerror = () => {
      socket?.close();
    };
  }

  function handleOffline() {
    onStatus("offline");
    socket?.close();
  }

  function handleOnline() {
    attempts = 0;
    connect();
  }

  window.addEventListener("offline", handleOffline);
  window.addEventListener("online", handleOnline);
  connect();

  return () => {
    closed = true;
    if (retryTimer) {
      window.clearTimeout(retryTimer);
    }
    window.removeEventListener("offline", handleOffline);
    window.removeEventListener("online", handleOnline);
    socket?.close();
  };
}
