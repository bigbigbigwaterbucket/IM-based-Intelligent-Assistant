import { jsx as _jsx, jsxs as _jsxs, Fragment as _Fragment } from "react/jsx-runtime";
import { useCallback, useEffect, useMemo, useState } from "react";
import { connectionStatusLabel, formatTaskTime, getTaskProgress, loadCachedTasks, mergeTask, mergeTasks, saveCachedTasks, stepStatusLabel, taskStatusLabel, } from "@agent-pilot/shared";
import { connectEvents, createTask, listTasks, sendTaskAction } from "./api";
const clientId = `desktop-${Math.random().toString(36).slice(2)}`;
const cacheKey = "agent-pilot.desktop.tasks.v1";
export function App() {
    const [tasks, setTasks] = useState(() => loadCachedTasks(cacheKey));
    const [selectedId, setSelectedId] = useState();
    const [query, setQuery] = useState("");
    const [connectionStatus, setConnectionStatus] = useState(typeof navigator !== "undefined" && navigator.onLine ? "connecting" : "offline");
    const [lastSyncAt, setLastSyncAt] = useState();
    const [errorMessage, setErrorMessage] = useState("");
    const [isSubmitting, setIsSubmitting] = useState(false);
    const [title, setTitle] = useState("Agent-Pilot 方案汇报");
    const [instruction, setInstruction] = useState("从 IM 讨论中整理需求背景、技术方案和演示稿结构，生成可用于评审的文档与演示材料。");
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
            setErrorMessage(error instanceof Error ? error.message : "任务同步失败，正在使用本地缓存");
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
    const filteredTasks = useMemo(() => {
        const keyword = query.trim().toLowerCase();
        if (!keyword) {
            return tasks;
        }
        return tasks.filter((task) => [task.title, task.userInstruction, task.progressText, task.currentStep]
            .join(" ")
            .toLowerCase()
            .includes(keyword));
    }, [query, tasks]);
    const selectedTask = useMemo(() => tasks.find((task) => task.taskId === selectedId) ?? filteredTasks[0] ?? tasks[0], [filteredTasks, selectedId, tasks]);
    const taskStats = useMemo(() => ({
        total: tasks.length,
        active: tasks.filter((task) => ["planning", "executing", "waiting_action"].includes(task.status)).length,
        completed: tasks.filter((task) => task.status === "completed").length,
        failed: tasks.filter((task) => task.status === "failed").length,
    }), [tasks]);
    const isOnline = connectionStatus === "online";
    async function onSubmit() {
        if (!title.trim() || !instruction.trim() || !isOnline) {
            return;
        }
        setIsSubmitting(true);
        setErrorMessage("");
        try {
            const task = await createTask({ title: title.trim(), instruction: instruction.trim(), source: "desktop" });
            commitTasks((current) => mergeTask(current, task));
            setSelectedId(task.taskId);
        }
        catch (error) {
            setErrorMessage(error instanceof Error ? error.message : "创建任务失败");
        }
        finally {
            setIsSubmitting(false);
        }
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
        if (!isOnline) {
            setErrorMessage("当前处于离线缓存模式，恢复在线后才能提交操作。");
            return;
        }
        try {
            const task = await sendTaskAction(selectedTask.taskId, {
                actionType,
                actorType: "desktop",
                clientId,
            });
            commitTasks((current) => mergeTask(current, task));
            setErrorMessage("");
        }
        catch (error) {
            setErrorMessage(error instanceof Error ? error.message : "操作提交失败");
        }
    }
    return (_jsxs("div", { className: "shell", children: [_jsxs("aside", { className: "sidebar", children: [_jsxs("div", { className: "brand", children: [_jsx("span", { className: "brand-mark", children: "AP" }), _jsxs("div", { children: [_jsx("h1", { children: "Agent Pilot" }), _jsx("p", { children: "\u4EFB\u52A1\u9A7E\u9A76\u8231" })] })] }), _jsxs("div", { className: "sync-strip", children: [_jsx("span", { className: `sync-dot sync-${connectionStatus}` }), _jsx("span", { children: connectionStatusLabel[connectionStatus] }), _jsx("small", { children: lastSyncAt ? `最近同步 ${formatTaskTime(lastSyncAt)}` : "等待同步" })] }), _jsxs("section", { className: "composer", children: [_jsxs("div", { className: "section-title", children: [_jsx("h2", { children: "\u53D1\u8D77\u4EFB\u52A1" }), _jsx("span", { children: "IM / Doc / Slides" })] }), _jsxs("label", { children: [_jsx("span", { children: "\u6807\u9898" }), _jsx("input", { value: title, onChange: (event) => setTitle(event.target.value) })] }), _jsxs("label", { children: [_jsx("span", { children: "\u81EA\u7136\u8BED\u8A00\u6307\u4EE4" }), _jsx("textarea", { value: instruction, onChange: (event) => setInstruction(event.target.value), rows: 5 })] }), _jsx("button", { className: "primary-button", onClick: onSubmit, disabled: !isOnline || isSubmitting, children: isSubmitting ? "创建中" : "创建 Agent Task" })] }), _jsxs("section", { className: "task-nav", children: [_jsxs("div", { className: "section-title", children: [_jsx("h2", { children: "\u4EFB\u52A1" }), _jsxs("span", { children: [taskStats.total, " \u4E2A"] })] }), _jsxs("div", { className: "stat-grid", children: [_jsxs("span", { children: ["\u8FDB\u884C\u4E2D ", taskStats.active] }), _jsxs("span", { children: ["\u5DF2\u5B8C\u6210 ", taskStats.completed] }), _jsxs("span", { children: ["\u5931\u8D25 ", taskStats.failed] })] }), _jsx("input", { className: "search", placeholder: "\u641C\u7D22\u4EFB\u52A1\u3001\u6B65\u9AA4\u6216\u8FDB\u5EA6", value: query, onChange: (event) => setQuery(event.target.value) }), _jsxs("div", { className: "task-list", children: [filteredTasks.map((task) => (_jsxs("button", { className: `task-item ${task.taskId === selectedTask?.taskId ? "selected" : ""}`, onClick: () => setSelectedId(task.taskId), children: [_jsx("span", { className: `status-pill status-${task.status}`, children: taskStatusLabel[task.status] }), _jsx("strong", { children: task.title }), _jsx("small", { children: task.progressText || task.currentStep || "等待 Agent 接管" }), _jsx("div", { className: "task-progress", children: _jsx("span", { style: { width: `${getTaskProgress(task)}%` } }) }), _jsx("em", { children: formatTaskTime(task.updatedAt) })] }, task.taskId))), filteredTasks.length === 0 ? _jsx("p", { className: "empty-text", children: "\u6CA1\u6709\u5339\u914D\u7684\u4EFB\u52A1" }) : null] })] })] }), _jsxs("main", { className: "workspace", children: [errorMessage ? _jsx("div", { className: "notice", children: errorMessage }) : null, selectedTask ? (_jsxs(_Fragment, { children: [_jsxs("section", { className: "task-hero", children: [_jsxs("div", { children: [_jsx("span", { className: "eyebrow", children: "Agent Task" }), _jsx("h2", { children: selectedTask.title }), _jsx("p", { children: selectedTask.userInstruction })] }), _jsxs("div", { className: "hero-meta", children: [_jsx("span", { className: `status-pill status-${selectedTask.status}`, children: taskStatusLabel[selectedTask.status] }), _jsxs("strong", { children: [getTaskProgress(selectedTask), "%"] }), _jsxs("small", { children: ["v", selectedTask.version, " \u00B7 ", selectedTask.lastActor] })] })] }), _jsxs("section", { className: "overview-grid", children: [_jsxs("div", { className: "metric", children: [_jsx("span", { children: "\u5F53\u524D\u6B65\u9AA4" }), _jsx("strong", { children: selectedTask.currentStep || "-" })] }), _jsxs("div", { className: "metric", children: [_jsx("span", { children: "\u6B65\u9AA4\u8FDB\u5EA6" }), _jsxs("strong", { children: [selectedTask.steps.filter((step) => step.status === "completed").length, "/", selectedTask.steps.length] })] }), _jsxs("div", { className: "metric", children: [_jsx("span", { children: "\u66F4\u65B0\u65F6\u95F4" }), _jsx("strong", { children: formatTaskTime(selectedTask.updatedAt) })] })] }), _jsxs("section", { className: "detail-grid", children: [_jsxs("div", { className: "surface run-panel", children: [_jsxs("div", { className: "section-title", children: [_jsx("h2", { children: "\u6267\u884C\u94FE\u8DEF" }), _jsx("span", { children: selectedTask.progressText || "等待状态更新" })] }), _jsx("div", { className: "timeline", children: selectedTask.steps.length > 0 ? (selectedTask.steps.map((step, index) => (_jsxs("div", { className: `timeline-item step-${step.status}`, children: [_jsx("div", { className: "timeline-index", children: index + 1 }), _jsxs("div", { className: "timeline-body", children: [_jsxs("div", { children: [_jsx("strong", { children: step.name }), _jsx("span", { children: stepStatusLabel[step.status] })] }), _jsx("p", { children: step.errorMessage || step.payloadSummary || "等待 Agent 输出步骤详情" }), _jsxs("small", { children: [step.startedAt ? `开始 ${formatTaskTime(step.startedAt)}` : "未开始", step.completedAt ? ` · 完成 ${formatTaskTime(step.completedAt)}` : ""] })] })] }, step.id)))) : (_jsx("p", { className: "empty-text", children: "Plan \u751F\u6210\u540E\u4F1A\u5728\u8FD9\u91CC\u5C55\u793A\u6BCF\u4E2A step \u7684\u72B6\u6001\u3002" })) })] }), _jsxs("aside", { className: "side-panel", children: [_jsxs("section", { className: "surface", children: [_jsxs("div", { className: "section-title", children: [_jsx("h2", { children: "Agent \u8F93\u51FA" }), _jsx("span", { children: "Doc / Slides" })] }), _jsx("p", { className: "summary", children: selectedTask.summary || "等待 Agent 生成任务摘要。" }), _jsxs("a", { className: selectedTask.docUrl ? "artifact-link" : "artifact-link disabled", href: selectedTask.docUrl, children: ["\u6587\u6863\uFF1A", selectedTask.docUrl || "未生成"] }), _jsxs("a", { className: selectedTask.slidesUrl ? "artifact-link" : "artifact-link disabled", href: selectedTask.slidesUrl, children: ["\u6F14\u793A\u7A3F\uFF1A", selectedTask.slidesUrl || "未生成"] })] }), _jsxs("section", { className: "surface", children: [_jsxs("div", { className: "section-title", children: [_jsx("h2", { children: "\u534F\u540C\u64CD\u4F5C" }), _jsx("span", { children: isOnline ? "可提交" : "离线只读" })] }), selectedTask.errorMessage ? _jsx("p", { className: "error-text", children: selectedTask.errorMessage }) : null, selectedTask.requiresAction ? _jsx("p", { className: "warning-text", children: "Agent \u6B63\u7B49\u5F85\u4EBA\u5DE5\u786E\u8BA4\u540E\u7EE7\u7EED\u6267\u884C\u3002" }) : null, _jsxs("div", { className: "actions", children: [_jsx("button", { onClick: () => void onAction("retry_task"), disabled: !isOnline || selectedTask.status !== "failed", children: "\u91CD\u8BD5" }), _jsx("button", { onClick: () => void onAction("approve_continue"), disabled: !isOnline || !selectedTask.requiresAction, children: "\u7EE7\u7EED" }), _jsx("button", { onClick: () => void onAction("open_artifact"), disabled: !selectedTask.docUrl && !selectedTask.slidesUrl, children: "\u6253\u5F00\u4EA7\u7269" })] })] })] })] })] })) : (_jsxs("section", { className: "empty-state", children: [_jsx("h2", { children: "\u6682\u65E0\u4EFB\u52A1" }), _jsx("p", { children: "\u5728\u7EBF\u540E\u53EF\u4EE5\u4ECE\u684C\u9762\u7AEF\u521B\u5EFA\u4EFB\u52A1\uFF0C\u6216\u7B49\u5F85 IM \u5165\u53E3\u89E6\u53D1 Agent Task\u3002" })] }))] })] }));
}
