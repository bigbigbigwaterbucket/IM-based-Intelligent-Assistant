import { useCallback, useEffect, useMemo, useState } from "react";
import type { ConnectionStatus, Task } from "@agent-pilot/shared";
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
import { connectEvents, listTasks, sendTaskAction } from "./api";

const clientId = `mobile-${Math.random().toString(36).slice(2)}`;
const cacheKey = "agent-pilot.mobile.tasks.v1";

export function App() {
  const [tasks, setTasks] = useState<Task[]>(() => loadCachedTasks(cacheKey));
  const [selectedId, setSelectedId] = useState<string>();
  const [connectionStatus, setConnectionStatus] = useState<ConnectionStatus>(
    typeof navigator !== "undefined" && navigator.onLine ? "connecting" : "offline",
  );
  const [lastSyncAt, setLastSyncAt] = useState<string>();
  const [errorMessage, setErrorMessage] = useState("");

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
      setErrorMessage(error instanceof Error ? error.message : "同步失败，正在展示本地缓存");
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

  const selectedTask = useMemo(
    () => tasks.find((task) => task.taskId === selectedId) ?? tasks[0],
    [selectedId, tasks],
  );

  const isOnline = connectionStatus === "online";

  async function submitAction(actionType: "retry_task" | "approve_continue") {
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
    } catch (error) {
      setErrorMessage(error instanceof Error ? error.message : "操作提交失败");
    }
  }

  return (
    <div className="mobile-shell">
      <header className="mobile-header">
        <div>
          <span>Agent Pilot</span>
          <h1>移动协同台</h1>
        </div>
        <div className={`sync-badge sync-${connectionStatus}`}>
          <i />
          {connectionStatusLabel[connectionStatus]}
        </div>
      </header>

      <section className="sync-note">
        <span>{lastSyncAt ? `最近同步 ${formatTaskTime(lastSyncAt)}` : "等待同步"}</span>
        <strong>{tasks.length} 个任务</strong>
      </section>

      {errorMessage ? <p className="mobile-notice">{errorMessage}</p> : null}

      <section className="mobile-list">
        {tasks.map((task) => (
          <button
            key={task.taskId}
            className={`mobile-card ${task.taskId === selectedTask?.taskId ? "selected" : ""}`}
            onClick={() => setSelectedId(task.taskId)}
          >
            <span className={`status-pill status-${task.status}`}>{taskStatusLabel[task.status]}</span>
            <strong>{task.title}</strong>
            <small>{task.progressText || task.currentStep || "等待 Agent 接管"}</small>
            <div className="mobile-progress">
              <span style={{ width: `${getTaskProgress(task)}%` }} />
            </div>
          </button>
        ))}
      </section>

      {selectedTask ? (
        <section className="mobile-detail">
          <div className="detail-title">
            <div>
              <span className={`status-pill status-${selectedTask.status}`}>{taskStatusLabel[selectedTask.status]}</span>
              <h2>{selectedTask.title}</h2>
            </div>
            <strong>{getTaskProgress(selectedTask)}%</strong>
          </div>
          <p>{selectedTask.userInstruction}</p>
          <p className="summary">{selectedTask.summary || selectedTask.progressText || "等待 Agent 生成摘要。"}</p>

          <div className="mobile-actions">
            <button disabled={!isOnline || selectedTask.status !== "failed"} onClick={() => void submitAction("retry_task")}>
              重试
            </button>
            <button disabled={!isOnline || !selectedTask.requiresAction} onClick={() => void submitAction("approve_continue")}>
              继续
            </button>
          </div>

          <div className="artifact-list">
            <a className={!selectedTask.docUrl ? "disabled" : ""} href={selectedTask.docUrl}>
              文档：{selectedTask.docUrl || "未生成"}
            </a>
            <a className={!selectedTask.slidesUrl ? "disabled" : ""} href={selectedTask.slidesUrl}>
              演示稿：{selectedTask.slidesUrl || "未生成"}
            </a>
          </div>

          <div className="step-list">
            <h3>执行步骤</h3>
            {selectedTask.steps.length > 0 ? (
              selectedTask.steps.map((step, index) => (
                <div key={step.id} className={`step-row step-${step.status}`}>
                  <span>{index + 1}</span>
                  <div>
                    <strong>{step.name}</strong>
                    <small>{stepStatusLabel[step.status]}</small>
                    <p>{step.errorMessage || step.payloadSummary || "等待步骤详情"}</p>
                  </div>
                </div>
              ))
            ) : (
              <p className="empty-text">Plan 生成后会同步展示步骤状态。</p>
            )}
          </div>
        </section>
      ) : (
        <section className="mobile-empty">
          <h2>暂无任务</h2>
          <p>等待桌面端或 IM 入口创建 Agent Task。</p>
        </section>
      )}
    </div>
  );
}
