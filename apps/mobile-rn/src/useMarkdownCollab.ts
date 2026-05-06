import { useCallback, useEffect, useRef, useState } from "react";
import * as Y from "yjs";
import { bytesToBase64, createMarkdownCollabSession } from "@agent-pilot/shared";
import type { CollabDocument } from "@agent-pilot/shared";
import {
  collabSocketURL,
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
  const ydocRef = useRef<Y.Doc | undefined>(undefined);
  const ytextRef = useRef<Y.Text | undefined>(undefined);
  const documentRef = useRef<CollabDocument | undefined>(undefined);
  const cleanupRef = useRef<(() => void) | undefined>(undefined);
  const seqRef = useRef(0);
  const applyingInputRef = useRef(false);
  const snapshotTimerRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const socketSendRef = useRef<((updateBase64: string) => void) | undefined>(undefined);

  const persistSnapshot = useCallback(async (status: CollabSaveStatus = "synced") => {
    const currentDocument = documentRef.current;
    if (!currentDocument || !ydocRef.current || !ytextRef.current) {
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
    setSaveStatus("loading");
    setError("");
    const activeTaskId = taskId;

    function scheduleSnapshot() {
      if (snapshotTimerRef.current) {
        clearTimeout(snapshotTimerRef.current);
      }
      snapshotTimerRef.current = setTimeout(() => void persistSnapshot(), 30000);
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
          socketSendRef.current?.(bytesToBase64(update));
          scheduleSnapshot();
        });

        const socket = new WebSocket(collabSocketURL(collabDoc.docKey, clientId));
        socket.onopen = () => setSaveStatus("synced");
        socket.onmessage = (event) => {
          const message = JSON.parse(String(event.data)) as { type?: string; seq?: number; clientId?: string; updateBase64?: string };
          if (message.type !== "update" || !message.updateBase64 || typeof message.seq !== "number") {
            return;
          }
          seqRef.current = Math.max(seqRef.current, message.seq);
          if (message.clientId !== clientId) {
            session.applyRemoteUpdate(message.updateBase64);
          }
        };
        socket.onerror = () => {
          setSaveStatus("error");
          setError("协同连接失败，请检查 EXPO_PUBLIC_WS_BASE");
        };
        socketSendRef.current = (updateBase64: string) => {
          if (socket.readyState === WebSocket.OPEN) {
            socket.send(JSON.stringify({ type: "update", docKey: collabDoc.docKey, clientId, updateBase64 }));
          }
        };
        cleanupRef.current = () => {
          socket.close();
          session.destroy();
        };
        if (!state.snapshotUpdateBase64 && collabDoc.markdownCache) {
          setTimeout(() => void persistSnapshot(), 0);
        }
      } catch (err) {
        if (!closed) {
          setSaveStatus("error");
          setError(err instanceof Error ? err.message : "加载协同文档失败");
        }
      }
    }

    void boot();
    return () => {
      closed = true;
      if (snapshotTimerRef.current) {
        clearTimeout(snapshotTimerRef.current);
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
