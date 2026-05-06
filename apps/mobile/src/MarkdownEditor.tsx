import { useMemo, useState } from "react";
import { useMarkdownCollab } from "./useMarkdownCollab";

interface MarkdownEditorProps {
  taskId: string;
  clientId: string;
}

export function MarkdownEditor({ taskId, clientId }: MarkdownEditorProps) {
  const [mode, setMode] = useState<"edit" | "preview">("edit");
  const { document, markdown, updateMarkdown, persistSnapshot, exportMarkdown, saveStatus, error } = useMarkdownCollab(taskId, clientId);
  const preview = useMemo(() => renderMarkdownPreview(markdown), [markdown]);

  return (
    <section className="mobile-markdown">
      <div className="mobile-markdown-title">
        <div>
          <h3>Markdown 编辑</h3>
          <span>{document?.title ?? statusText[saveStatus]}</span>
        </div>
        <strong>{statusText[saveStatus]}</strong>
      </div>
      {error ? <p className="mobile-notice">{error}</p> : null}
      {document && !document.sourcePath && !markdown ? (
        <p className="mobile-notice">当前任务还没有可编辑的 Markdown 源，文档生成后会自动加载。</p>
      ) : null}
      <div className="mobile-segment">
        <button className={mode === "edit" ? "selected" : ""} onClick={() => setMode("edit")}>
          编辑
        </button>
        <button className={mode === "preview" ? "selected" : ""} onClick={() => setMode("preview")}>
          预览
        </button>
      </div>
      {mode === "edit" ? (
        <textarea
          className="mobile-markdown-editor"
          value={markdown}
          onChange={(event) => updateMarkdown(event.target.value)}
          spellCheck={false}
          placeholder="生成 Markdown 后可在这里协同编辑"
        />
      ) : (
        <div className="mobile-markdown-preview" dangerouslySetInnerHTML={{ __html: preview }} />
      )}
      <div className="mobile-actions">
        <button disabled={!document || saveStatus === "loading" || saveStatus === "saving"} onClick={() => void persistSnapshot()}>
          保存快照
        </button>
        <button disabled={!document || saveStatus === "loading" || saveStatus === "saving"} onClick={() => void exportMarkdown()}>
          导出
        </button>
      </div>
    </section>
  );
}

const statusText = {
  idle: "未打开",
  loading: "加载中",
  synced: "已同步",
  saving: "保存中",
  exported: "已导出",
  error: "异常",
};

function renderMarkdownPreview(markdown: string): string {
  return markdown
    .split(/\r?\n/)
    .map((line) => {
      const trimmed = line.trim();
      if (trimmed.startsWith("# ")) {
        return `<h1>${escapeHtml(trimmed.slice(2))}</h1>`;
      }
      if (trimmed.startsWith("## ")) {
        return `<h2>${escapeHtml(trimmed.slice(3))}</h2>`;
      }
      if (trimmed.startsWith("- ")) {
        return `<p>• ${escapeHtml(trimmed.slice(2))}</p>`;
      }
      return trimmed ? `<p>${escapeHtml(trimmed)}</p>` : "";
    })
    .join("");
}

function escapeHtml(value: string): string {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}
