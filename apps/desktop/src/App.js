import { jsx as _jsx, jsxs as _jsxs, Fragment as _Fragment } from "react/jsx-runtime";
import { useEffect, useMemo, useState } from "react";
import { connectEvents, createTask, listTasks, sendTaskAction } from "./api";
const clientId = `desktop-${Math.random().toString(36).slice(2)}`;
export function App() {
    const [tasks, setTasks] = useState([]);
    const [selectedId, setSelectedId] = useState();
    const [title, setTitle] = useState("Weekly summary");
    const [instruction, setInstruction] = useState("Create a solution doc from this week's product discussion and prepare a short management deck.");
    useEffect(() => {
        listTasks()
            .then((items) => {
            const nextTasks = Array.isArray(items) ? items : [];
            setTasks(nextTasks);
            if (nextTasks[0]) {
                setSelectedId(nextTasks[0].taskId);
            }
        })
            .catch((error) => {
            console.error("Failed to load tasks", error);
            setTasks([]);
        });
        return connectEvents((event) => {
            if (!event?.payload) {
                return;
            }
            setTasks((current) => mergeTask(current, event.payload));
            setSelectedId((current) => current ?? event.taskId ?? event.payload.taskId);
        });
    }, []);
    const taskList = Array.isArray(tasks) ? tasks : [];
    const selectedTask = useMemo(() => taskList.find((task) => task.taskId === selectedId) ?? taskList[0], [selectedId, taskList]);
    async function onSubmit() {
        const task = await createTask({ title, instruction, source: "desktop" });
        if (!task) {
            return;
        }
        setTasks((current) => mergeTask(current, task));
        setSelectedId(task.taskId);
    }
    async function onAction(actionType) {
        if (!selectedTask) {
            return;
        }
        if (actionType === "open_artifact") {
            const target = selectedTask.slidesUrl ?? selectedTask.docUrl;
            if (target) {
                window.open(target, "_blank", "noopener,noreferrer");
            }
            return;
        }
        await sendTaskAction(selectedTask.taskId, {
            actionType,
            actorType: "desktop",
            clientId,
        });
    }
    return (_jsxs("div", { className: "shell", children: [_jsxs("aside", { className: "panel left", children: [_jsx("h1", { children: "Agent Pilot" }), _jsxs("div", { className: "card composer", children: [_jsxs("label", { children: [_jsx("span", { children: "Title" }), _jsx("input", { value: title, onChange: (event) => setTitle(event.target.value) })] }), _jsxs("label", { children: [_jsx("span", { children: "Instruction" }), _jsx("textarea", { value: instruction, onChange: (event) => setInstruction(event.target.value), rows: 6 })] }), _jsx("button", { onClick: onSubmit, children: "Create Task" })] }), _jsx("div", { className: "task-list", children: taskList.map((task) => (_jsxs("button", { className: `task-item ${task.taskId === selectedTask?.taskId ? "selected" : ""}`, onClick: () => setSelectedId(task.taskId), children: [_jsx("strong", { children: task.title }), _jsx("span", { children: task.status }), _jsx("small", { children: task.progressText })] }, task.taskId))) })] }), _jsx("main", { className: "panel content", children: selectedTask ? (_jsxs(_Fragment, { children: [_jsxs("section", { className: "card hero", children: [_jsxs("div", { children: [_jsx("h2", { children: selectedTask.title }), _jsx("p", { children: selectedTask.userInstruction })] }), _jsxs("div", { className: "meta", children: [_jsx("span", { className: `status status-${selectedTask.status}`, children: selectedTask.status }), _jsxs("span", { children: ["v", selectedTask.version] }), _jsxs("span", { children: ["actor: ", selectedTask.lastActor] })] })] }), _jsxs("section", { className: "grid", children: [_jsxs("div", { className: "card", children: [_jsx("h3", { children: "Task Details" }), _jsxs("p", { children: ["Current step: ", selectedTask.currentStep || "-"] }), _jsxs("p", { children: ["Progress: ", selectedTask.progressText || "-"] }), _jsxs("p", { children: ["Summary: ", selectedTask.summary || "-"] }), _jsxs("p", { children: ["Error: ", selectedTask.errorMessage || "-"] }), _jsxs("div", { className: "actions", children: [_jsx("button", { onClick: () => onAction("retry_task"), disabled: selectedTask.status !== "failed", children: "Retry" }), _jsx("button", { onClick: () => onAction("approve_continue"), disabled: !selectedTask.requiresAction, children: "Continue" }), _jsx("button", { onClick: () => onAction("open_artifact"), disabled: !selectedTask.docUrl && !selectedTask.slidesUrl, children: "Open Artifact" })] })] }), _jsxs("div", { className: "card", children: [_jsx("h3", { children: "Artifacts" }), _jsx("a", { href: selectedTask.docUrl, target: "_blank", rel: "noreferrer", children: selectedTask.docUrl || "Doc not ready" }), _jsx("a", { href: selectedTask.slidesUrl, target: "_blank", rel: "noreferrer", children: selectedTask.slidesUrl || "Slides not ready" })] })] }), _jsxs("section", { className: "card", children: [_jsx("h3", { children: "Run Log" }), _jsx("div", { className: "steps", children: (selectedTask.steps ?? []).map((step) => (_jsxs("div", { className: `step step-${step.status}`, children: [_jsxs("div", { children: [_jsx("strong", { children: step.name }), _jsx("small", { children: step.payloadSummary })] }), _jsx("span", { children: step.status })] }, step.id))) })] })] })) : (_jsx("section", { className: "card empty", children: _jsx("h2", { children: "No Tasks" }) })) })] }));
}
function mergeTask(tasks, next) {
    if (!next?.taskId) {
        return Array.isArray(tasks) ? tasks : [];
    }
    const currentTasks = Array.isArray(tasks) ? tasks : [];
    const current = currentTasks.findIndex((task) => task.taskId === next.taskId);
    if (current === -1) {
        return [next, ...currentTasks];
    }
    const clone = currentTasks.slice();
    clone[current] = next;
    return clone;
}
