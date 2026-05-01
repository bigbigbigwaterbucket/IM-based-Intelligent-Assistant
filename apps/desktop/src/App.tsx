import { useCallback, useEffect, useMemo, useState } from "react";
import type { ActionType, ConnectionStatus, Task } from "@agent-pilot/shared";
import {
  connectionStatusLabel,
  formatTaskTime,
  getTaskProgress,
  loadCachedTasks,
  mergeTask,
  mergeTasks,
  saveCachedTasks,
  stepStatusLabel,
  taskStatusLabel,
} from "@agent-pilot/shared";
import { connectEvents, createTask, listTasks, sendTaskAction } from "./api";

const clientId = `desktop-${Math.random().toString(36).slice(2)}`;
const cacheKey = "agent-pilot.desktop.tasks.v1";

export function App() {
  const [tasks, setTasks] = useState<Task[]>(() => loadCachedTasks(cacheKey));
  const [selectedId, setSelectedId] = useState<string>();
  const [query, setQuery] = useState("");
  const [connectionStatus, setConnectionStatus] = useState<ConnectionStatus>(
    typeof navigator !== "undefined" && navigator.onLine ? "connecting" : "offline",
  );
  const [lastSyncAt, setLastSyncAt] = useState<string>();
  const [errorMessage, setErrorMessage] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [title, setTitle] = useState("Agent-Pilot 方案汇报");
  const [instruction, setInstruction] = useState(
    "从 IM 讨论中整理需求背景、技术方案和演示稿结构，生成可用于评审的文档与演示材料。",
  );

  const commitTasks = useCallback((mutate: (current: Task[]) => Task[]) => {
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
    } catch (error) {
      setErrorMessage(error instanceof Error ? error.message : "任务同步失败，正在使用本地缓存");
      if (typeof navigator !== "undefined" && !navigator.onLine) {
        setConnectionStatus("offline");
      }
    }
  }, [commitTasks]);

  useEffect(() => {
    void refreshTasks();
    return connectEvents(
      (event) => {
        commitTasks((current) => mergeTask(current, event.payload));
        setSelectedId((current) => current ?? event.taskId ?? event.payload.taskId);
        setLastSyncAt(event.emittedAt);
        setErrorMessage("");
      },
      setConnectionStatus,
      refreshTasks,
    );
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
    return tasks.filter((task) =>
      [task.title, task.userInstruction, task.progressText, task.currentStep]
        .join(" ")
        .toLowerCase()
        .includes(keyword),
    );
  }, [query, tasks]);

  const selectedTask = useMemo(
    () => tasks.find((task) => task.taskId === selectedId) ?? filteredTasks[0] ?? tasks[0],
    [filteredTasks, selectedId, tasks],
  );

  const taskStats = useMemo(
    () => ({
      total: tasks.length,
      active: tasks.filter((task) => ["planning", "executing", "waiting_action"].includes(task.status)).length,
      completed: tasks.filter((task) => task.status === "completed").length,
      failed: tasks.filter((task) => task.status === "failed").length,
    }),
    [tasks],
  );

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
    } catch (error) {
      setErrorMessage(error instanceof Error ? error.message : "创建任务失败");
    } finally {
      setIsSubmitting(false);
    }
  }

  async function onAction(actionType: ActionType) {
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
    } catch (error) {
      setErrorMessage(error instanceof Error ? error.message : "操作提交失败");
    }
  }

  return (
    <div className="shell">
      <aside className="sidebar">
        <div className="brand">
          <span className="brand-mark">AP</span>
          <div>
            <h1>Agent Pilot</h1>
            <p>任务驾驶舱</p>
          </div>
        </div>

        <div className="sync-strip">
          <span className={`sync-dot sync-${connectionStatus}`} />
          <span>{connectionStatusLabel[connectionStatus]}</span>
          <small>{lastSyncAt ? `最近同步 ${formatTaskTime(lastSyncAt)}` : "等待同步"}</small>
        </div>

        <section className="composer">
          <div className="section-title">
            <h2>发起任务</h2>
            <span>IM / Doc / Slides</span>
          </div>
          <label>
            <span>标题</span>
            <input value={title} onChange={(event) => setTitle(event.target.value)} />
          </label>
          <label>
            <span>自然语言指令</span>
            <textarea value={instruction} onChange={(event) => setInstruction(event.target.value)} rows={5} />
          </label>
          <button className="primary-button" onClick={onSubmit} disabled={!isOnline || isSubmitting}>
            {isSubmitting ? "创建中" : "创建 Agent Task"}
          </button>
        </section>

        <section className="task-nav">
          <div className="section-title">
            <h2>任务</h2>
            <span>{taskStats.total} 个</span>
          </div>
          <div className="stat-grid">
            <span>进行中 {taskStats.active}</span>
            <span>已完成 {taskStats.completed}</span>
            <span>失败 {taskStats.failed}</span>
          </div>
          <input
            className="search"
            placeholder="搜索任务、步骤或进度"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
          <div className="task-list">
            {filteredTasks.map((task) => (
              <button
                key={task.taskId}
                className={`task-item ${task.taskId === selectedTask?.taskId ? "selected" : ""}`}
                onClick={() => setSelectedId(task.taskId)}
              >
                <span className={`status-pill status-${task.status}`}>{taskStatusLabel[task.status]}</span>
                <strong>{task.title}</strong>
                <small>{task.progressText || task.currentStep || "等待 Agent 接管"}</small>
                <div className="task-progress">
                  <span style={{ width: `${getTaskProgress(task)}%` }} />
                </div>
                <em>{formatTaskTime(task.updatedAt)}</em>
              </button>
            ))}
            {filteredTasks.length === 0 ? <p className="empty-text">没有匹配的任务</p> : null}
          </div>
        </section>
      </aside>

      <main className="workspace">
        {errorMessage ? <div className="notice">{errorMessage}</div> : null}

        {selectedTask ? (
          <>
            <section className="task-hero">
              <div>
                <span className="eyebrow">Agent Task</span>
                <h2>{selectedTask.title}</h2>
                <p>{selectedTask.userInstruction}</p>
              </div>
              <div className="hero-meta">
                <span className={`status-pill status-${selectedTask.status}`}>{taskStatusLabel[selectedTask.status]}</span>
                <strong>{getTaskProgress(selectedTask)}%</strong>
                <small>v{selectedTask.version} · {selectedTask.lastActor}</small>
              </div>
            </section>

            <section className="overview-grid">
              <div className="metric">
                <span>当前步骤</span>
                <strong>{selectedTask.currentStep || "-"}</strong>
              </div>
              <div className="metric">
                <span>步骤进度</span>
                <strong>
                  {selectedTask.steps.filter((step) => step.status === "completed").length}/{selectedTask.steps.length}
                </strong>
              </div>
              <div className="metric">
                <span>更新时间</span>
                <strong>{formatTaskTime(selectedTask.updatedAt)}</strong>
              </div>
            </section>

            <section className="detail-grid">
              <div className="surface run-panel">
                <div className="section-title">
                  <h2>执行链路</h2>
                  <span>{selectedTask.progressText || "等待状态更新"}</span>
                </div>
                <div className="timeline">
                  {selectedTask.steps.length > 0 ? (
                    selectedTask.steps.map((step, index) => (
                      <div key={step.id} className={`timeline-item step-${step.status}`}>
                        <div className="timeline-index">{index + 1}</div>
                        <div className="timeline-body">
                          <div>
                            <strong>{step.name}</strong>
                            <span>{stepStatusLabel[step.status]}</span>
                          </div>
                          <p>{step.errorMessage || step.payloadSummary || "等待 Agent 输出步骤详情"}</p>
                          <small>
                            {step.startedAt ? `开始 ${formatTaskTime(step.startedAt)}` : "未开始"}
                            {step.completedAt ? ` · 完成 ${formatTaskTime(step.completedAt)}` : ""}
                          </small>
                        </div>
                      </div>
                    ))
                  ) : (
                    <p className="empty-text">Plan 生成后会在这里展示每个 step 的状态。</p>
                  )}
                </div>
              </div>

              <aside className="side-panel">
                <section className="surface">
                  <div className="section-title">
                    <h2>Agent 输出</h2>
                    <span>Doc / Slides</span>
                  </div>
                  <p className="summary">{selectedTask.summary || "等待 Agent 生成任务摘要。"}</p>
                  <a className={selectedTask.docUrl ? "artifact-link" : "artifact-link disabled"} href={selectedTask.docUrl}>
                    文档：{selectedTask.docUrl || "未生成"}
                  </a>
                  <a className={selectedTask.slidesUrl ? "artifact-link" : "artifact-link disabled"} href={selectedTask.slidesUrl}>
                    演示稿：{selectedTask.slidesUrl || "未生成"}
                  </a>
                </section>

                <section className="surface">
                  <div className="section-title">
                    <h2>协同操作</h2>
                    <span>{isOnline ? "可提交" : "离线只读"}</span>
                  </div>
                  {selectedTask.errorMessage ? <p className="error-text">{selectedTask.errorMessage}</p> : null}
                  {selectedTask.requiresAction ? <p className="warning-text">Agent 正等待人工确认后继续执行。</p> : null}
                  <div className="actions">
                    <button onClick={() => void onAction("retry_task")} disabled={!isOnline || selectedTask.status !== "failed"}>
                      重试
                    </button>
                    <button
                      onClick={() => void onAction("approve_continue")}
                      disabled={!isOnline || !selectedTask.requiresAction}
                    >
                      继续
                    </button>
                    <button
                      onClick={() => void onAction("open_artifact")}
                      disabled={!selectedTask.docUrl && !selectedTask.slidesUrl}
                    >
                      打开产物
                    </button>
                  </div>
                </section>
              </aside>
            </section>
          </>
        ) : (
          <section className="empty-state">
            <h2>暂无任务</h2>
            <p>在线后可以从桌面端创建任务，或等待 IM 入口触发 Agent Task。</p>
          </section>
        )}
      </main>
    </div>
  );
}
