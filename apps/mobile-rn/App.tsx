import { StatusBar } from "expo-status-bar";
import { useCallback, useEffect, useMemo, useState } from "react";
import { Linking, Pressable, ScrollView, StyleSheet, Text, View } from "react-native";
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
import { connectEvents, listTasks, sendTaskAction } from "./src/api";
import { MarkdownEditor } from "./src/MarkdownEditor";

const clientId = `mobile-rn-${Math.random().toString(36).slice(2)}`;
const cacheKey = "agent-pilot.mobile-rn.tasks.v1";

export default function App() {
  const [tasks, setTasks] = useState<Task[]>(() => loadCachedTasks(cacheKey));
  const [selectedId, setSelectedId] = useState<string>();
  const [connectionStatus, setConnectionStatus] = useState<ConnectionStatus>("connecting");
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
      setConnectionStatus("offline");
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

  const selectedTask = useMemo(() => tasks.find((task) => task.taskId === selectedId) ?? tasks[0], [selectedId, tasks]);
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
    <View style={styles.root}>
      <StatusBar style="dark" />
      <ScrollView contentContainerStyle={styles.shell}>
        <View style={styles.header}>
          <View>
            <Text style={styles.brand}>Agent Pilot</Text>
            <Text style={styles.headerTitle}>移动协同台</Text>
          </View>
          <View style={styles.syncBadge}>
            <View style={[styles.syncDot, syncDotStyle[connectionStatus]]} />
            <Text style={styles.syncText}>{connectionStatusLabel[connectionStatus]}</Text>
          </View>
        </View>

        <View style={styles.syncNote}>
          <Text style={styles.muted}>{lastSyncAt ? `最近同步 ${formatTaskTime(lastSyncAt)}` : "等待同步"}</Text>
          <Text style={styles.syncCount}>{tasks.length} 个任务</Text>
        </View>

        {errorMessage ? <Text style={styles.notice}>{errorMessage}</Text> : null}

        <View style={styles.taskList}>
          {tasks.map((task) => (
            <Pressable
              key={task.taskId}
              style={[styles.taskCard, task.taskId === selectedTask?.taskId && styles.selectedCard]}
              onPress={() => setSelectedId(task.taskId)}
            >
              <StatusPill status={task.status} />
              <Text style={styles.cardTitle} numberOfLines={1}>
                {task.title}
              </Text>
              <Text style={styles.cardMeta} numberOfLines={1}>
                {task.progressText || task.currentStep || "等待 Agent 接管"}
              </Text>
              <ProgressBar value={getTaskProgress(task)} />
            </Pressable>
          ))}
        </View>

        {selectedTask ? (
          <View style={styles.detail}>
            <View style={styles.detailTitle}>
              <View style={styles.detailTitleText}>
                <StatusPill status={selectedTask.status} />
                <Text style={styles.detailHeading}>{selectedTask.title}</Text>
              </View>
              <Text style={styles.percent}>{getTaskProgress(selectedTask)}%</Text>
            </View>
            <Text style={styles.bodyText}>{selectedTask.userInstruction}</Text>
            <Text style={styles.summary}>{selectedTask.summary || selectedTask.progressText || "等待 Agent 生成摘要。"}</Text>

            <View style={styles.actions}>
              <Pressable
                style={[styles.actionButton, (!isOnline || selectedTask.status !== "failed") && styles.disabledButton]}
                disabled={!isOnline || selectedTask.status !== "failed"}
                onPress={() => void submitAction("retry_task")}
              >
                <Text style={styles.actionText}>重试</Text>
              </Pressable>
              <Pressable
                style={[styles.actionButton, (!isOnline || !selectedTask.requiresAction) && styles.disabledButton]}
                disabled={!isOnline || !selectedTask.requiresAction}
                onPress={() => void submitAction("approve_continue")}
              >
                <Text style={styles.actionText}>继续</Text>
              </Pressable>
            </View>

            <View style={styles.artifactList}>
              <ArtifactLink label="文档" url={selectedTask.docUrl} />
              <ArtifactLink label="演示稿" url={selectedTask.slidesUrl} />
            </View>

            <MarkdownEditor taskId={selectedTask.taskId} clientId={clientId} />

            <View style={styles.stepList}>
              <Text style={styles.sectionHeading}>执行步骤</Text>
              {selectedTask.steps.length > 0 ? (
                selectedTask.steps.map((step, index) => (
                  <View key={step.id} style={styles.stepRow}>
                    <View style={[styles.stepIndex, step.status === "running" && styles.stepRunning, step.status === "completed" && styles.stepCompleted, step.status === "failed" && styles.stepFailed]}>
                      <Text style={styles.stepIndexText}>{index + 1}</Text>
                    </View>
                    <View style={styles.stepBody}>
                      <Text style={styles.stepName}>{step.name}</Text>
                      <Text style={styles.cardMeta}>{stepStatusLabel[step.status]}</Text>
                      <Text style={styles.stepText}>{step.errorMessage || step.payloadSummary || "等待步骤详情"}</Text>
                    </View>
                  </View>
                ))
              ) : (
                <Text style={styles.cardMeta}>Plan 生成后会同步展示步骤状态。</Text>
              )}
            </View>
          </View>
        ) : (
          <View style={styles.empty}>
            <Text style={styles.detailHeading}>暂无任务</Text>
            <Text style={styles.bodyText}>等待桌面端或 IM 入口创建 Agent Task。</Text>
          </View>
        )}
      </ScrollView>
    </View>
  );
}

function StatusPill({ status }: { status: Task["status"] }) {
  return (
    <View style={[styles.statusPill, statusPillStyle[status]]}>
      <Text style={[styles.statusText, statusTextStyle[status]]}>{taskStatusLabel[status]}</Text>
    </View>
  );
}

function ProgressBar({ value }: { value: number }) {
  return (
    <View style={styles.progress}>
      <View style={[styles.progressFill, { width: `${value}%` }]} />
    </View>
  );
}

function ArtifactLink({ label, url }: { label: string; url?: string }) {
  return (
    <Pressable style={[styles.artifact, !url && styles.disabledArtifact]} disabled={!url} onPress={() => url && void Linking.openURL(url)}>
      <Text style={[styles.artifactText, !url && styles.disabledArtifactText]} numberOfLines={1}>
        {label}：{url || "未生成"}
      </Text>
    </Pressable>
  );
}

const syncDotStyle: Record<ConnectionStatus, { backgroundColor: string }> = {
  connecting: { backgroundColor: "#c98516" },
  online: { backgroundColor: "#1f8f5f" },
  offline: { backgroundColor: "#c84f3d" },
  reconnecting: { backgroundColor: "#c98516" },
};

const statusPillStyle: Record<Task["status"], { backgroundColor: string }> = {
  created: { backgroundColor: "#e8edf3" },
  planning: { backgroundColor: "#e7f1fb" },
  executing: { backgroundColor: "#e7f1fb" },
  waiting_action: { backgroundColor: "#fff4dc" },
  completed: { backgroundColor: "#e3f5ec" },
  failed: { backgroundColor: "#fde8e4" },
};

const statusTextStyle: Record<Task["status"], { color: string }> = {
  created: { color: "#465463" },
  planning: { color: "#1769aa" },
  executing: { color: "#1769aa" },
  waiting_action: { color: "#92610c" },
  completed: { color: "#1f7b53" },
  failed: { color: "#a43e2f" },
};

const styles = StyleSheet.create({
  root: {
    flex: 1,
    backgroundColor: "#eef1f5",
  },
  shell: {
    gap: 12,
    padding: 14,
    paddingTop: 48,
  },
  header: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "space-between",
    gap: 12,
    borderWidth: 1,
    borderColor: "#d8e0e8",
    borderRadius: 8,
    backgroundColor: "#ffffff",
    padding: 14,
  },
  brand: {
    color: "#1769aa",
    fontSize: 12,
    fontWeight: "700",
  },
  headerTitle: {
    marginTop: 4,
    color: "#17202a",
    fontSize: 22,
    fontWeight: "800",
  },
  syncBadge: {
    flexDirection: "row",
    alignItems: "center",
    gap: 6,
    borderRadius: 999,
    backgroundColor: "#f0f4f8",
    paddingHorizontal: 9,
    paddingVertical: 6,
  },
  syncDot: {
    width: 8,
    height: 8,
    borderRadius: 999,
  },
  syncText: {
    color: "#495664",
    fontSize: 12,
  },
  syncNote: {
    flexDirection: "row",
    justifyContent: "space-between",
    borderWidth: 1,
    borderColor: "#d8e0e8",
    borderRadius: 8,
    backgroundColor: "#ffffff",
    paddingHorizontal: 12,
    paddingVertical: 10,
  },
  muted: {
    color: "#667483",
    fontSize: 12,
  },
  syncCount: {
    color: "#17202a",
    fontSize: 12,
    fontWeight: "700",
  },
  notice: {
    borderWidth: 1,
    borderColor: "#d8e0e8",
    borderRadius: 8,
    backgroundColor: "#fff8e8",
    padding: 10,
    color: "#8a4d00",
    fontSize: 13,
  },
  taskList: {
    gap: 8,
  },
  taskCard: {
    gap: 7,
    borderWidth: 1,
    borderColor: "#d8e0e8",
    borderRadius: 8,
    backgroundColor: "#ffffff",
    padding: 12,
  },
  selectedCard: {
    borderColor: "#1769aa",
    backgroundColor: "#f5faff",
  },
  statusPill: {
    alignSelf: "flex-start",
    borderRadius: 999,
    paddingHorizontal: 8,
    paddingVertical: 3,
  },
  statusText: {
    fontSize: 12,
    fontWeight: "700",
  },
  cardTitle: {
    color: "#17202a",
    fontSize: 15,
    fontWeight: "700",
  },
  cardMeta: {
    color: "#667483",
    fontSize: 12,
  },
  progress: {
    height: 5,
    overflow: "hidden",
    borderRadius: 999,
    backgroundColor: "#e5ebf1",
  },
  progressFill: {
    height: "100%",
    borderRadius: 999,
    backgroundColor: "#2d7fbc",
  },
  detail: {
    gap: 14,
    borderWidth: 1,
    borderColor: "#d8e0e8",
    borderRadius: 8,
    backgroundColor: "#ffffff",
    padding: 14,
  },
  empty: {
    gap: 14,
    borderWidth: 1,
    borderColor: "#d8e0e8",
    borderRadius: 8,
    backgroundColor: "#ffffff",
    padding: 14,
  },
  detailTitle: {
    flexDirection: "row",
    alignItems: "flex-start",
    justifyContent: "space-between",
    gap: 12,
  },
  detailTitleText: {
    flex: 1,
    minWidth: 0,
  },
  detailHeading: {
    marginTop: 8,
    color: "#17202a",
    fontSize: 20,
    fontWeight: "800",
  },
  percent: {
    color: "#17202a",
    fontSize: 24,
    fontWeight: "800",
  },
  bodyText: {
    color: "#495664",
    fontSize: 14,
    lineHeight: 22,
  },
  summary: {
    borderLeftWidth: 3,
    borderLeftColor: "#1769aa",
    paddingLeft: 10,
    color: "#495664",
    fontSize: 14,
    lineHeight: 22,
  },
  actions: {
    flexDirection: "row",
    gap: 8,
  },
  actionButton: {
    flex: 1,
    alignItems: "center",
    borderRadius: 6,
    backgroundColor: "#1769aa",
    padding: 10,
  },
  disabledButton: {
    opacity: 0.48,
  },
  actionText: {
    color: "#ffffff",
    fontWeight: "700",
  },
  artifactList: {
    gap: 8,
  },
  artifact: {
    borderWidth: 1,
    borderColor: "#d8e0e8",
    borderRadius: 6,
    paddingHorizontal: 10,
    paddingVertical: 9,
  },
  artifactText: {
    color: "#1769aa",
    fontSize: 13,
  },
  disabledArtifact: {
    opacity: 0.7,
  },
  disabledArtifactText: {
    color: "#8b96a3",
  },
  stepList: {
    gap: 10,
  },
  sectionHeading: {
    color: "#17202a",
    fontSize: 15,
    fontWeight: "800",
  },
  stepRow: {
    flexDirection: "row",
    gap: 10,
  },
  stepIndex: {
    alignItems: "center",
    justifyContent: "center",
    width: 28,
    height: 28,
    borderRadius: 999,
    backgroundColor: "#e8edf3",
  },
  stepRunning: {
    backgroundColor: "#dcecff",
  },
  stepCompleted: {
    backgroundColor: "#dff3e9",
  },
  stepFailed: {
    backgroundColor: "#fde1dc",
  },
  stepIndexText: {
    color: "#495664",
    fontSize: 12,
    fontWeight: "800",
  },
  stepBody: {
    flex: 1,
    borderWidth: 1,
    borderColor: "#d8e0e8",
    borderRadius: 8,
    backgroundColor: "#fbfcfe",
    paddingHorizontal: 10,
    paddingVertical: 9,
  },
  stepName: {
    color: "#17202a",
    fontSize: 14,
    fontWeight: "700",
  },
  stepText: {
    marginTop: 6,
    color: "#495664",
    fontSize: 13,
    lineHeight: 19,
  },
});
