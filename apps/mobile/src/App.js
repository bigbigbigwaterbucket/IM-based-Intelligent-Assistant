import { jsx as _jsx, jsxs as _jsxs } from "react/jsx-runtime";
import { useEffect, useMemo, useState } from "react";
import { connectEvents, listTasks, sendTaskAction } from "./api";
const clientId = `mobile-${Math.random().toString(36).slice(2)}`;
export function App() {
    const [tasks, setTasks] = useState([]);
    const [selectedId, setSelectedId] = useState();
    useEffect(() => {
        listTasks().then((items) => {
            setTasks(items);
            if (items[0]) {
                setSelectedId(items[0].taskId);
            }
        });
        return connectEvents((event) => {
            setTasks((current) => mergeTask(current, event.payload));
            setSelectedId((current) => current ?? event.taskId);
        });
    }, []);
    const selectedTask = useMemo(() => tasks.find((task) => task.taskId === selectedId) ?? tasks[0], [selectedId, tasks]);
    async function submitAction(actionType) {
        if (!selectedTask) {
            return;
        }
        await sendTaskAction(selectedTask.taskId, {
            actionType,
            actorType: "mobile",
            clientId,
        });
    }
    return (_jsxs("div", { className: "mobile-shell", children: [_jsxs("header", { children: [_jsx("h1", { children: "Agent Pilot" }), _jsx("p", { children: "\u79FB\u52A8\u8F7B\u64CD\u4F5C\u53F0" })] }), _jsx("section", { className: "mobile-list", children: tasks.map((task) => (_jsxs("button", { className: "mobile-card", onClick: () => setSelectedId(task.taskId), children: [_jsx("strong", { children: task.title }), _jsx("span", { children: task.status }), _jsx("small", { children: task.progressText })] }, task.taskId))) }), selectedTask ? (_jsxs("section", { className: "mobile-detail", children: [_jsx("h2", { children: selectedTask.title }), _jsx("p", { children: selectedTask.progressText }), _jsx("p", { children: selectedTask.summary || "等待结果摘要" }), _jsxs("div", { className: "mobile-actions", children: [_jsx("button", { disabled: selectedTask.status !== "failed", onClick: () => submitAction("retry_task"), children: "\u91CD\u8BD5" }), _jsx("button", { disabled: !selectedTask.requiresAction, onClick: () => submitAction("approve_continue"), children: "\u7EE7\u7EED" })] }), _jsx("a", { href: selectedTask.docUrl, target: "_blank", rel: "noreferrer", children: selectedTask.docUrl || "Doc 未生成" }), _jsx("a", { href: selectedTask.slidesUrl, target: "_blank", rel: "noreferrer", children: selectedTask.slidesUrl || "Slides 未生成" })] })) : null] }));
}
function mergeTask(tasks, next) {
    const current = tasks.findIndex((task) => task.taskId === next.taskId);
    if (current === -1) {
        return [next, ...tasks];
    }
    const clone = tasks.slice();
    clone[current] = next;
    return clone;
}
