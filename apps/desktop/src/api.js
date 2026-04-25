const API_BASE = import.meta.env.VITE_API_BASE ?? "http://localhost:8080";
const WS_BASE = (import.meta.env.VITE_WS_BASE ?? "ws://localhost:8080") + "/ws";
function normalizeTask(task) {
    if (!task || typeof task !== "object") {
        return null;
    }
    const candidate = task;
    return {
        ...candidate,
        steps: Array.isArray(candidate.steps) ? candidate.steps : [],
    };
}
export async function listTasks() {
    const response = await fetch(`${API_BASE}/tasks`);
    if (!response.ok) {
        throw new Error("Failed to load tasks");
    }
    const data = (await response.json());
    return Array.isArray(data) ? data.map(normalizeTask).filter((task) => task !== null) : [];
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
export function connectEvents(onEvent) {
    const socket = new WebSocket(WS_BASE);
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
    return () => {
        socket.close();
    };
}
