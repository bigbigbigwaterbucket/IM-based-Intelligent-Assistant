import { useEffect, useMemo, useState } from "react";
import type { Task } from "@agent-pilot/shared";
import { connectEvents, listTasks, sendTaskAction } from "./api";

const clientId = `mobile-${Math.random().toString(36).slice(2)}`;

export function App() {
  const [tasks, setTasks] = useState<Task[]>([]);
  const [selectedId, setSelectedId] = useState<string>();

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

  const selectedTask = useMemo(
    () => tasks.find((task) => task.taskId === selectedId) ?? tasks[0],
    [selectedId, tasks],
  );

  async function submitAction(actionType: "retry_task" | "approve_continue") {
    if (!selectedTask) {
      return;
    }
    await sendTaskAction(selectedTask.taskId, {
      actionType,
      actorType: "mobile",
      clientId,
    });
  }

  return (
    <div className="mobile-shell">
      <header>
        <h1>Agent Pilot</h1>
        <p>移动轻操作台</p>
      </header>

      <section className="mobile-list">
        {tasks.map((task) => (
          <button key={task.taskId} className="mobile-card" onClick={() => setSelectedId(task.taskId)}>
            <strong>{task.title}</strong>
            <span>{task.status}</span>
            <small>{task.progressText}</small>
          </button>
        ))}
      </section>

      {selectedTask ? (
        <section className="mobile-detail">
          <h2>{selectedTask.title}</h2>
          <p>{selectedTask.progressText}</p>
          <p>{selectedTask.summary || "等待结果摘要"}</p>
          <div className="mobile-actions">
            <button disabled={selectedTask.status !== "failed"} onClick={() => submitAction("retry_task")}>
              重试
            </button>
            <button disabled={!selectedTask.requiresAction} onClick={() => submitAction("approve_continue")}>
              继续
            </button>
          </div>
          <a href={selectedTask.docUrl} target="_blank" rel="noreferrer">
            {selectedTask.docUrl || "Doc 未生成"}
          </a>
          <a href={selectedTask.slidesUrl} target="_blank" rel="noreferrer">
            {selectedTask.slidesUrl || "Slides 未生成"}
          </a>
        </section>
      ) : null}
    </div>
  );
}

function mergeTask(tasks: Task[], next: Task): Task[] {
  const current = tasks.findIndex((task) => task.taskId === next.taskId);
  if (current === -1) {
    return [next, ...tasks];
  }
  const clone = tasks.slice();
  clone[current] = next;
  return clone;
}

