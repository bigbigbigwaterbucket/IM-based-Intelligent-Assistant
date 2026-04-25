const API_BASE = import.meta.env.VITE_API_BASE ?? "http://localhost:8080";
const WS_BASE = (import.meta.env.VITE_WS_BASE ?? "ws://localhost:8080") + "/ws";
export async function listTasks() {
    const response = await fetch(`${API_BASE}/tasks`);
    if (!response.ok) {
        throw new Error("Failed to load tasks");
    }
    return response.json();
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
}
export function connectEvents(onEvent) {
    const socket = new WebSocket(WS_BASE);
    socket.onmessage = (message) => {
        const event = JSON.parse(message.data);
        onEvent(event);
    };
    return () => socket.close();
}
