import { useCallback, useEffect, useRef, useState } from "react";
import * as Y from "yjs";
import { bytesToBase64, createMarkdownCollabSession } from "@agent-pilot/shared";
import type { CollabDocument } from "@agent-pilot/shared";
import {
  exportCollabMarkdown,
  loadCollabState,
  loadCollabUpdates,
  loadMarkdownDocument,
  saveCollabSnapshot,
} from "./api";

export type CollabSaveStatus = "idle" | "loading" | "synced" | "saving" | "exported" | "error";

export function useMarkdownCollab(taskId: string | undefined, clientId: string) {
  const [document, setDocument] = useState<CollabDocument>();
  const [markdown, setMarkdown] = useState("");
  const [saveStatus, setSaveStatus] = useState<CollabSaveStatus>("idle");
  const [error, setError] = useState("");
  const ydocRef = useRef<Y.Doc>();
  const ytextRef = useRef<Y.Text>();
  const documentRef = useRef<CollabDocument>();
  const cleanupRef = useRef<(() => void) | undefined>();
  const seqRef = useRef(0);
  const applyingInputRef = useRef(false);
  const snapshotTimerRef = useRef<number>();
  const reconnectTimerRef = useRef<number>();
  const bootRetryTimerRef = useRef<number>();
  const catchupTimerRef = useRef<number>();
  const pendingUpdatesRef = useRef<string[]>([]);
  const socketRef = useRef<WebSocket>();
  const socketSendRef = useRef<(updateBase64: string) => void>();

  const persistSnapshot = useCallback(async (status: CollabSaveStatus = "synced") => {
    const currentDocument = documentRef.current;
    if (!currentDocument || !ydocRef.current || !ytextRef.current) {
      return;
    }
    if (pendingUpdatesRef.current.length > 0) {
      return;
    }
    setSaveStatus("saving");
    try {
      const next = await saveCollabSnapshot(currentDocument.docKey, {
        baseSeq: seqRef.current,
        snapshotUpdateBase64: bytesToBase64(Y.encodeStateAsUpdate(ydocRef.current)),
        markdownCache: ytextRef.current.toString(),
        clientId,
      });
      documentRef.current = next;
      setDocument(next);
      setSaveStatus(status);
      setError("");
    } catch (err) {
      setSaveStatus("error");
      setError(err instanceof Error ? err.message : "保存协同快照失败");
    }
  }, [clientId]);

  useEffect(() => {
    if (!taskId) {
      return;
    }
    let closed = false;
    cleanupRef.current?.();
    ydocRef.current?.destroy();
    pendingUpdatesRef.current = [];
    setSaveStatus("loading");
    setError("");
    const activeTaskId = taskId;

    function scheduleSnapshot() {
      if (pendingUpdatesRef.current.length > 0) {
        return;
      }
      if (snapshotTimerRef.current) {
        window.clearTimeout(snapshotTimerRef.current);
      }
      snapshotTimerRef.current = window.setTimeout(() => void persistSnapshot(), 30000);
    }

    function acknowledgePending(updateBase64: string) {
      const index = pendingUpdatesRef.current.indexOf(updateBase64);
      if (index !== -1) {
        pendingUpdatesRef.current.splice(index, 1);
      }
    }

    function setSyncedIfIdle() {
      if (pendingUpdatesRef.current.length === 0) {
        setSaveStatus("synced");
        setError("");
      } else {
        setSaveStatus("error");
        setError("有本地离线编辑待同步，正在等待服务端确认。");
      }
    }

    function applyServerUpdates(session: Awaited<ReturnType<typeof createMarkdownCollabSession>>, updates: Awaited<ReturnType<typeof loadCollabUpdates>>) {
      for (const update of updates) {
        seqRef.current = Math.max(seqRef.current, update.seq);
        if (update.clientId === clientId) {
          acknowledgePending(update.updateBase64);
        } else {
          session.applyRemoteUpdate(update.updateBase64);
        }
      }
    }

    function sendPendingOverSocket(socket: WebSocket, docKey: string) {
      const pending = pendingUpdatesRef.current.slice();
      for (const updateBase64 of pending) {
        try {
          socket.send(JSON.stringify({ type: "update", docKey, clientId, updateBase64 }));
        } catch {
          socket.close();
          return;
        }
      }
    }

    async function catchUpFromServer(session: Awaited<ReturnType<typeof createMarkdownCollabSession>>, collabDoc: CollabDocument) {
      const state = await loadCollabState(collabDoc.docKey);
      seqRef.current = Math.max(seqRef.current, state.snapshotSeq);
      if (state.snapshotUpdateBase64) {
        session.applyRemoteUpdate(state.snapshotUpdateBase64);
      }
      const updates = await loadCollabUpdates(collabDoc.docKey, state.snapshotSeq);
      applyServerUpdates(session, updates);
      setSyncedIfIdle();
    }

    function queueOrSend(updateBase64: string) {
      if (snapshotTimerRef.current) {
        window.clearTimeout(snapshotTimerRef.current);
        snapshotTimerRef.current = undefined;
      }
      pendingUpdatesRef.current.push(updateBase64);
      setSaveStatus("error");
      setError("有本地离线编辑待同步，后端恢复后会自动提交。");
      const socket = socketRef.current;
      if (socket?.readyState === WebSocket.OPEN) {
        try {
          socket.send(JSON.stringify({ type: "update", docKey: documentRef.current?.docKey, clientId, updateBase64 }));
        } catch {
          socket.close();
        }
      }
    }

    function scheduleReconnect(connect: () => void) {
      if (closed || reconnectTimerRef.current) {
        return;
      }
      setSaveStatus("error");
      setError("协同连接中断，正在重连；离线编辑会在恢复后同步。");
      reconnectTimerRef.current = window.setTimeout(() => {
        reconnectTimerRef.current = undefined;
        connect();
      }, 2000);
    }

    async function boot() {
      try {
        const collabDoc = await loadMarkdownDocument(activeTaskId);
        const state = await loadCollabState(collabDoc.docKey);
        const updates = await loadCollabUpdates(collabDoc.docKey, state.snapshotSeq);
        if (closed) {
          return;
        }
        const session = await createMarkdownCollabSession(collabDoc, state, updates);
        ydocRef.current = session.doc;
        ytextRef.current = session.text;
        documentRef.current = collabDoc;
        seqRef.current = updates.reduce((max, update) => Math.max(max, update.seq), state.snapshotSeq);
        setDocument(collabDoc);
        setMarkdown(session.getMarkdown());

        session.text.observe(() => {
          if (!applyingInputRef.current) {
            setMarkdown(session.getMarkdown());
          }
        });
        session.doc.on("update", (update: Uint8Array, origin: unknown) => {
          if (origin === "remote" || origin === "initial") {
            return;
          }
          queueOrSend(bytesToBase64(update));
          scheduleSnapshot();
        });

        const connectSocket = () => {
          if (closed) {
            return;
          }
          const socket = new WebSocket(
            `${(import.meta.env.VITE_WS_BASE ?? "ws://localhost:8080")}/collab/docs/${encodeURIComponent(collabDoc.docKey)}/ws?clientId=${encodeURIComponent(clientId)}`,
          );
          socketRef.current = socket;
          socket.onopen = () => {
            void (async () => {
              try {
                if (closed || socketRef.current !== socket) {
                  return;
                }
                await catchUpFromServer(session, collabDoc);
                sendPendingOverSocket(socket, collabDoc.docKey);
              } catch {
                socket.close();
              }
            })();
          };
          socket.onmessage = (event) => {
            const message = JSON.parse(event.data as string) as { type?: string; seq?: number; clientId?: string; updateBase64?: string };
            if (message.type !== "update" || !message.updateBase64 || typeof message.seq !== "number") {
              return;
            }
            seqRef.current = Math.max(seqRef.current, message.seq);
            if (message.clientId === clientId) {
              acknowledgePending(message.updateBase64);
              if (pendingUpdatesRef.current.length === 0) {
                setSaveStatus("synced");
                setError("");
              }
            } else {
              session.applyRemoteUpdate(message.updateBase64);
            }
          };
          socket.onclose = () => {
            if (socketRef.current === socket) {
              socketRef.current = undefined;
            }
            scheduleReconnect(connectSocket);
          };
          socket.onerror = () => {
            socket.close();
          };
        };
        socketSendRef.current = queueOrSend;
        connectSocket();
        catchupTimerRef.current = window.setInterval(() => {
          void (async () => {
            try {
              if (closed) {
                return;
              }
              await catchUpFromServer(session, collabDoc);
            } catch {
              if (!closed) {
                setSaveStatus("error");
                setError("协同服务不可用，正在重试；本地编辑会暂存到恢复连接后同步。");
              }
            }
          })();
        }, 1000);
        cleanupRef.current = () => {
          socketRef.current?.close();
          session.destroy();
        };
        if (!state.snapshotUpdateBase64 && collabDoc.markdownCache) {
          window.setTimeout(() => void persistSnapshot(), 0);
        }
      } catch (err) {
        if (!closed) {
          setSaveStatus("error");
          setError(`${err instanceof Error ? err.message : "加载协同文档失败"}，正在重试。`);
          bootRetryTimerRef.current = window.setTimeout(() => void boot(), 2000);
        }
      }
    }

    void boot();
    return () => {
      closed = true;
      if (snapshotTimerRef.current) {
        window.clearTimeout(snapshotTimerRef.current);
      }
      if (reconnectTimerRef.current) {
        window.clearTimeout(reconnectTimerRef.current);
      }
      if (bootRetryTimerRef.current) {
        window.clearTimeout(bootRetryTimerRef.current);
      }
      if (catchupTimerRef.current) {
        window.clearInterval(catchupTimerRef.current);
      }
      cleanupRef.current?.();
      cleanupRef.current = undefined;
      socketSendRef.current = undefined;
      documentRef.current = undefined;
      ydocRef.current = undefined;
      ytextRef.current = undefined;
    };
  }, [clientId, persistSnapshot, taskId]);

  const updateMarkdown = useCallback((next: string) => {
    const text = ytextRef.current;
    if (!text) {
      return;
    }
    const current = text.toString();
    if (current === next) {
      return;
    }
    const change = diffText(current, next);
    applyingInputRef.current = true;
    text.doc?.transact(() => {
      if (change.deleteLength > 0) {
        text.delete(change.index, change.deleteLength);
      }
      if (change.insertText) {
        text.insert(change.index, change.insertText);
      }
    });
    applyingInputRef.current = false;
    setMarkdown(next);
  }, []);

  const exportMarkdown = useCallback(async () => {
    const currentDocument = documentRef.current;
    if (!currentDocument || !ydocRef.current || !ytextRef.current) {
      return;
    }
    setSaveStatus("saving");
    try {
      const next = await exportCollabMarkdown(currentDocument.docKey, {
        markdown: ytextRef.current.toString(),
        baseSeq: seqRef.current,
        snapshotUpdateBase64: bytesToBase64(Y.encodeStateAsUpdate(ydocRef.current)),
        clientId,
      });
      documentRef.current = next;
      setDocument(next);
      setSaveStatus("exported");
      setError("");
    } catch (err) {
      setSaveStatus("error");
      setError(err instanceof Error ? err.message : "导出 Markdown 失败");
    }
  }, [clientId]);

  return { document, markdown, updateMarkdown, persistSnapshot, exportMarkdown, saveStatus, error };
}

function diffText(previous: string, next: string): { index: number; deleteLength: number; insertText: string } {
  let start = 0;
  while (start < previous.length && start < next.length && previous[start] === next[start]) {
    start += 1;
  }

  let previousEnd = previous.length;
  let nextEnd = next.length;
  while (previousEnd > start && nextEnd > start && previous[previousEnd - 1] === next[nextEnd - 1]) {
    previousEnd -= 1;
    nextEnd -= 1;
  }

  return {
    index: start,
    deleteLength: previousEnd - start,
    insertText: next.slice(start, nextEnd),
  };
}
