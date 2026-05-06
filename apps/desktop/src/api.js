import { normalizeTask, normalizeTasks } from "@agent-pilot/shared";
const API_BASE = import.meta.env.VITE_API_BASE ?? "http://localhost:8080";
const WS_BASE = (import.meta.env.VITE_WS_BASE ?? "ws://localhost:8080") + "/ws";
const COLLAB_WS_BASE = import.meta.env.VITE_WS_BASE ?? "ws://localhost:8080";
export async function listTasks() {
    const response = await fetch(`${API_BASE}/tasks`);
    if (!response.ok) {
        throw new Error("Failed to load tasks");
    }
    const data = (await response.json());
    return normalizeTasks(data);
}
export async function createTask(payload) {
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
    const task = normalizeTask((await response.json()));
    if (!task) {
        throw new Error("Invalid task payload");
    }
    return task;
}
export async function sendTaskAction(taskId, payload) {
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
    const task = normalizeTask((await response.json()));
    if (!task) {
        throw new Error("Invalid task payload");
    }
    return task;
}
export async function loadMarkdownDocument(taskId) {
    const response = await fetch(`${API_BASE}/tasks/${taskId}/documents/markdown`);
    if (!response.ok) {
        throw new Error("Failed to load markdown document");
    }
    return (await response.json());
}
export async function loadCollabState(docKey) {
    const response = await fetch(`${API_BASE}/collab/docs/${encodeURIComponent(docKey)}/state`);
    if (!response.ok) {
        throw new Error("Failed to load collaborative state");
    }
    return (await response.json());
}
export async function loadCollabUpdates(docKey, sinceSeq) {
    const response = await fetch(`${API_BASE}/collab/docs/${encodeURIComponent(docKey)}/updates?sinceSeq=${sinceSeq}`);
    if (!response.ok) {
        throw new Error("Failed to load collaborative updates");
    }
    const payload = (await response.json());
    return Array.isArray(payload) ? payload : [];
}
export async function saveCollabSnapshot(docKey, payload) {
    const response = await fetch(`${API_BASE}/collab/docs/${encodeURIComponent(docKey)}/snapshot`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
    });
    if (!response.ok) {
        throw new Error("Failed to save snapshot");
    }
    return (await response.json());
}
export async function exportCollabMarkdown(docKey, payload) {
    const response = await fetch(`${API_BASE}/collab/docs/${encodeURIComponent(docKey)}/export`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
    });
    if (!response.ok) {
        throw new Error("Failed to export markdown");
    }
    return (await response.json());
}
export function connectCollabDoc(docKey, clientId, onUpdate) {
    const socket = new WebSocket(`${COLLAB_WS_BASE}/collab/docs/${encodeURIComponent(docKey)}/ws?clientId=${encodeURIComponent(clientId)}`);
    socket.onmessage = (event) => {
        const message = JSON.parse(event.data);
        if (message.type === "update" && message.docKey && message.updateBase64 && typeof message.seq === "number" && message.clientId) {
            onUpdate(message);
        }
    };
    return () => socket.close();
}
export function connectEvents(onEvent, onStatus, onReconnect) {
    let socket;
    let retryTimer;
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
            const event = JSON.parse(message.data);
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
