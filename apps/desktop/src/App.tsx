import { useEffect, useMemo, useState } from "react";
import type { Task } from "@agent-pilot/shared";
import { connectEvents, createTask, listTasks, sendTaskAction } from "./api";

const clientId = `desktop-${Math.random().toString(36).slice(2)}`;

export function App() {
  const [tasks, setTasks] = useState<Task[]>([]);
  const [selectedId, setSelectedId] = useState<string>();
  const [title, setTitle] = useState("Weekly summary");
  const [instruction, setInstruction] = useState(
    "Create a solution doc from this week's product discussion and prepare a short management deck.",
  );

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
  const selectedTask = useMemo(
    () => taskList.find((task) => task.taskId === selectedId) ?? taskList[0],
    [selectedId, taskList],
  );

  async function onSubmit() {
    const task = await createTask({ title, instruction, source: "desktop" });
    if (!task) {
      return;
    }
    setTasks((current) => mergeTask(current, task));
    setSelectedId(task.taskId);
  }

  async function onAction(actionType: "retry_task" | "approve_continue" | "open_artifact") {
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

  return (
    <div className="shell">
      <aside className="panel left">
        <h1>Agent Pilot</h1>
        <div className="card composer">
          <label>
            <span>Title</span>
            <input value={title} onChange={(event) => setTitle(event.target.value)} />
          </label>
          <label>
            <span>Instruction</span>
            <textarea value={instruction} onChange={(event) => setInstruction(event.target.value)} rows={6} />
          </label>
          <button onClick={onSubmit}>Create Task</button>
        </div>
        <div className="task-list">
          {taskList.map((task) => (
            <button
              key={task.taskId}
              className={`task-item ${task.taskId === selectedTask?.taskId ? "selected" : ""}`}
              onClick={() => setSelectedId(task.taskId)}
            >
              <strong>{task.title}</strong>
              <span>{task.status}</span>
              <small>{task.progressText}</small>
            </button>
          ))}
        </div>
      </aside>

      <main className="panel content">
        {selectedTask ? (
          <>
            <section className="card hero">
              <div>
                <h2>{selectedTask.title}</h2>
                <p>{selectedTask.userInstruction}</p>
              </div>
              <div className="meta">
                <span className={`status status-${selectedTask.status}`}>{selectedTask.status}</span>
                <span>v{selectedTask.version}</span>
                <span>actor: {selectedTask.lastActor}</span>
              </div>
            </section>

            <section className="grid">
              <div className="card">
                <h3>Task Details</h3>
                <p>Current step: {selectedTask.currentStep || "-"}</p>
                <p>Progress: {selectedTask.progressText || "-"}</p>
                <p>Summary: {selectedTask.summary || "-"}</p>
                <p>Error: {selectedTask.errorMessage || "-"}</p>
                <div className="actions">
                  <button onClick={() => onAction("retry_task")} disabled={selectedTask.status !== "failed"}>
                    Retry
                  </button>
                  <button
                    onClick={() => onAction("approve_continue")}
                    disabled={!selectedTask.requiresAction}
                  >
                    Continue
                  </button>
                  <button onClick={() => onAction("open_artifact")} disabled={!selectedTask.docUrl && !selectedTask.slidesUrl}>
                    Open Artifact
                  </button>
                </div>
              </div>

              <div className="card">
                <h3>Artifacts</h3>
                <a href={selectedTask.docUrl} target="_blank" rel="noreferrer">
                  {selectedTask.docUrl || "Doc not ready"}
                </a>
                <a href={selectedTask.slidesUrl} target="_blank" rel="noreferrer">
                  {selectedTask.slidesUrl || "Slides not ready"}
                </a>
              </div>
            </section>

            <section className="card">
              <h3>Run Log</h3>
              <div className="steps">
                {(selectedTask.steps ?? []).map((step) => (
                  <div key={step.id} className={`step step-${step.status}`}>
                    <div>
                      <strong>{step.name}</strong>
                      <small>{step.payloadSummary}</small>
                    </div>
                    <span>{step.status}</span>
                  </div>
                ))}
              </div>
            </section>
          </>
        ) : (
          <section className="card empty">
            <h2>No Tasks</h2>
          </section>
        )}
      </main>
    </div>
  );
}

function mergeTask(tasks: Task[], next: Task): Task[] {
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
