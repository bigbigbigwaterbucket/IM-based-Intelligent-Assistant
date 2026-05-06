import * as Y from "yjs";
import type { CollabDocument, CollabExportRequest, CollabSnapshotRequest, CollabState, CollabUpdate } from "./index";

export interface CollabTransport {
  loadMarkdownDocument(taskId: string): Promise<CollabDocument>;
  loadCollabState(docKey: string): Promise<CollabState>;
  loadCollabUpdates(docKey: string, sinceSeq: number): Promise<CollabUpdate[]>;
  saveCollabSnapshot(docKey: string, payload: CollabSnapshotRequest): Promise<CollabDocument>;
  exportCollabMarkdown(docKey: string, payload: CollabExportRequest): Promise<CollabDocument>;
  connectCollabDoc(docKey: string, clientId: string, onUpdate: (message: CollabSocketUpdate) => void): () => void;
}

export interface CollabSocketUpdate {
  type: "update";
  docKey: string;
  seq: number;
  clientId: string;
  updateBase64: string;
}

export interface MarkdownCollabSession {
  doc: Y.Doc;
  text: Y.Text;
  getMarkdown(): string;
  applyRemoteUpdate(updateBase64: string): void;
  encodeStateUpdate(): string;
  destroy(): void;
}

export async function createMarkdownCollabSession(
  document: CollabDocument,
  state: CollabState,
  updates: CollabUpdate[],
): Promise<MarkdownCollabSession> {
  const doc = new Y.Doc();
  const text = doc.getText("markdown");

  if (state.snapshotUpdateBase64) {
    Y.applyUpdate(doc, base64ToBytes(state.snapshotUpdateBase64));
  } else if (document.markdownCache) {
    text.insert(0, document.markdownCache);
  }

  for (const update of Array.isArray(updates) ? updates : []) {
    Y.applyUpdate(doc, base64ToBytes(update.updateBase64));
  }

  return {
    doc,
    text,
    getMarkdown: () => text.toString(),
    applyRemoteUpdate: (updateBase64: string) => Y.applyUpdate(doc, base64ToBytes(updateBase64), "remote"),
    encodeStateUpdate: () => bytesToBase64(Y.encodeStateAsUpdate(doc)),
    destroy: () => doc.destroy(),
  };
}

export function base64ToBytes(value: string): Uint8Array {
  const binary = atob(value);
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) {
    bytes[index] = binary.charCodeAt(index);
  }
  return bytes;
}

export function bytesToBase64(bytes: Uint8Array): string {
  let binary = "";
  const chunkSize = 0x8000;
  for (let index = 0; index < bytes.length; index += chunkSize) {
    const chunk = bytes.subarray(index, index + chunkSize);
    binary += String.fromCharCode(...chunk);
  }
  return btoa(binary);
}
