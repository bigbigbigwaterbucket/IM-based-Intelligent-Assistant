import { normalizeTask, normalizeTasks } from "@agent-pilot/shared";
const API_BASE = import.meta.env.VITE_API_BASE ?? "http://localhost:8080";
const WS_BASE = (import.meta.env.VITE_WS_BASE ?? "ws://localhost:8080") + "/ws";
export async function listTasks() {
    const response = await fetch(`${API_BASE}/tasks`);
    if (!response.ok) {
        throw new Error("Failed to load tasks");
    }
    return normalizeTasks((await response.json()));
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
        throw new Error("Failed to send action");
    }
    const task = normalizeTask((await response.json()));
    if (!task) {
        throw new Error("Invalid task payload");
    }
    return task;
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
