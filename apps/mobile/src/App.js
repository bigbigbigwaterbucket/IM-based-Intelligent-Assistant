import { jsx as _jsx, jsxs as _jsxs } from "react/jsx-runtime";
import { useCallback, useEffect, useMemo, useState } from "react";
import { connectionStatusLabel, formatTaskTime, getTaskProgress, loadCachedTasks, mergeTask, mergeTasks, saveCachedTasks, stepStatusLabel, taskStatusLabel, } from "@agent-pilot/shared";
import { connectEvents, listTasks, sendTaskAction } from "./api";
const clientId = `mobile-${Math.random().toString(36).slice(2)}`;
const cacheKey = "agent-pilot.mobile.tasks.v1";
export function App() {
    const [tasks, setTasks] = useState(() => loadCachedTasks(cacheKey));
    const [selectedId, setSelectedId] = useState();
    const [connectionStatus, setConnectionStatus] = useState(typeof navigator !== "undefined" && navigator.onLine ? "connecting" : "offline");
    const [lastSyncAt, setLastSyncAt] = useState();
    const [errorMessage, setErrorMessage] = useState("");
    const commitTasks = useCallback((mutate) => {
        setTasks((current) => {
            const next = mutate(current);
            saveCachedTasks(cacheKey, next);
            return next;
        });
    }, []);
    const refreshTasks = useCallback(async () => {
        try {
            const items = await listTasks();
            commitTasks((current) => mergeTasks(current, items));
            setLastSyncAt(new Date().toISOString());
            setErrorMessage("");
        }
        catch (error) {
            setErrorMessage(error instanceof Error ? error.message : "同步失败，正在展示本地缓存");
            if (typeof navigator !== "undefined" && !navigator.onLine) {
                setConnectionStatus("offline");
            }
        }
    }, [commitTasks]);
    useEffect(() => {
        void refreshTasks();
        return connectEvents((event) => {
            commitTasks((current) => mergeTask(current, event.payload));
            setSelectedId((current) => current ?? event.taskId ?? event.payload.taskId);
            setLastSyncAt(event.emittedAt);
            setErrorMessage("");
        }, setConnectionStatus, refreshTasks);
    }, [commitTasks, refreshTasks]);
    useEffect(() => {
        if (!selectedId && tasks[0]) {
            setSelectedId(tasks[0].taskId);
        }
    }, [selectedId, tasks]);
    const selectedTask = useMemo(() => tasks.find((task) => task.taskId === selectedId) ?? tasks[0], [selectedId, tasks]);
    const isOnline = connectionStatus === "online";
    async function submitAction(actionType) {
        if (!selectedTask || !isOnline) {
            setErrorMessage("当前处于离线缓存模式，恢复在线后才能提交操作。");
            return;
        }
        try {
            const task = await sendTaskAction(selectedTask.taskId, {
                actionType,
                actorType: "mobile",
                clientId,
            });
            commitTasks((current) => mergeTask(current, task));
            setErrorMessage("");
        }
        catch (error) {
            setErrorMessage(error instanceof Error ? error.message : "操作提交失败");
        }
    }
    return (_jsxs("div", { className: "mobile-shell", children: [_jsxs("header", { className: "mobile-header", children: [_jsxs("div", { children: [_jsx("span", { children: "Agent Pilot" }), _jsx("h1", { children: "\u79FB\u52A8\u534F\u540C\u53F0" })] }), _jsxs("div", { className: `sync-badge sync-${connectionStatus}`, children: [_jsx("i", {}), connectionStatusLabel[connectionStatus]] })] }), _jsxs("section", { className: "sync-note", children: [_jsx("span", { children: lastSyncAt ? `最近同步 ${formatTaskTime(lastSyncAt)}` : "等待同步" }), _jsxs("strong", { children: [tasks.length, " \u4E2A\u4EFB\u52A1"] })] }), errorMessage ? _jsx("p", { className: "mobile-notice", children: errorMessage }) : null, _jsx("section", { className: "mobile-list", children: tasks.map((task) => (_jsxs("button", { className: `mobile-card ${task.taskId === selectedTask?.taskId ? "selected" : ""}`, onClick: () => setSelectedId(task.taskId), children: [_jsx("span", { className: `status-pill status-${task.status}`, children: taskStatusLabel[task.status] }), _jsx("strong", { children: task.title }), _jsx("small", { children: task.progressText || task.currentStep || "等待 Agent 接管" }), _jsx("div", { className: "mobile-progress", children: _jsx("span", { style: { width: `${getTaskProgress(task)}%` } }) })] }, task.taskId))) }), selectedTask ? (_jsxs("section", { className: "mobile-detail", children: [_jsxs("div", { className: "detail-title", children: [_jsxs("div", { children: [_jsx("span", { className: `status-pill status-${selectedTask.status}`, children: taskStatusLabel[selectedTask.status] }), _jsx("h2", { children: selectedTask.title })] }), _jsxs("strong", { children: [getTaskProgress(selectedTask), "%"] })] }), _jsx("p", { children: selectedTask.userInstruction }), _jsx("p", { className: "summary", children: selectedTask.summary || selectedTask.progressText || "等待 Agent 生成摘要。" }), _jsxs("div", { className: "mobile-actions", children: [_jsx("button", { disabled: !isOnline || selectedTask.status !== "failed", onClick: () => void submitAction("retry_task"), children: "\u91CD\u8BD5" }), _jsx("button", { disabled: !isOnline || !selectedTask.requiresAction, onClick: () => void submitAction("approve_continue"), children: "\u7EE7\u7EED" })] }), _jsxs("div", { className: "artifact-list", children: [_jsxs("a", { className: !selectedTask.docUrl ? "disabled" : "", href: selectedTask.docUrl, children: ["\u6587\u6863\uFF1A", selectedTask.docUrl || "未生成"] }), _jsxs("a", { className: !selectedTask.slidesUrl ? "disabled" : "", href: selectedTask.slidesUrl, children: ["\u6F14\u793A\u7A3F\uFF1A", selectedTask.slidesUrl || "未生成"] })] }), _jsxs("div", { className: "step-list", children: [_jsx("h3", { children: "\u6267\u884C\u6B65\u9AA4" }), selectedTask.steps.length > 0 ? (selectedTask.steps.map((step, index) => (_jsxs("div", { className: `step-row step-${step.status}`, children: [_jsx("span", { children: index + 1 }), _jsxs("div", { children: [_jsx("strong", { children: step.name }), _jsx("small", { children: stepStatusLabel[step.status] }), _jsx("p", { children: step.errorMessage || step.payloadSummary || "等待步骤详情" })] })] }, step.id)))) : (_jsx("p", { className: "empty-text", children: "Plan \u751F\u6210\u540E\u4F1A\u540C\u6B65\u5C55\u793A\u6B65\u9AA4\u72B6\u6001\u3002" }))] })] })) : (_jsxs("section", { className: "mobile-empty", children: [_jsx("h2", { children: "\u6682\u65E0\u4EFB\u52A1" }), _jsx("p", { children: "\u7B49\u5F85\u684C\u9762\u7AEF\u6216 IM \u5165\u53E3\u521B\u5EFA Agent Task\u3002" })] }))] }));
}
