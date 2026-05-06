import { useMemo } from "react";
import { useMarkdownCollab } from "./useMarkdownCollab";

interface MarkdownEditorProps {
  taskId: string;
  clientId: string;
}

export function MarkdownEditor({ taskId, clientId }: MarkdownEditorProps) {
  const { document, markdown, updateMarkdown, persistSnapshot, exportMarkdown, saveStatus, error } = useMarkdownCollab(taskId, clientId);
  const preview = useMemo(() => renderMarkdownPreview(markdown), [markdown]);

  return (
    <section className="surface markdown-workbench">
      <div className="section-title">
        <h2>Markdown 协同编辑</h2>
        <span>{statusText[saveStatus]}</span>
      </div>
      {error ? <p className="error-text">{error}</p> : null}
      {document && !document.sourcePath && !markdown ? (
        <p className="warning-text">当前任务还没有可编辑的 Markdown 源，文档生成后会自动加载。</p>
      ) : null}
      <div className="markdown-toolbar">
        <span>{document?.title ?? "加载文档"}</span>
        <div>
          <button onClick={() => void persistSnapshot()} disabled={!document || saveStatus === "loading" || saveStatus === "saving"}>
            保存快照
          </button>
          <button onClick={() => void exportMarkdown()} disabled={!document || saveStatus === "loading" || saveStatus === "saving"}>
            导出 Markdown
          </button>
        </div>
      </div>
      <div className="markdown-grid">
        <textarea
          className="markdown-editor"
          value={markdown}
          onChange={(event) => updateMarkdown(event.target.value)}
          spellCheck={false}
          placeholder="生成 Markdown 后可在这里协同编辑"
        />
        <div className="markdown-preview" dangerouslySetInnerHTML={{ __html: preview }} />
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
  error: "同步异常",
};

function renderMarkdownPreview(markdown: string): string {
  const lines = markdown.split(/\r?\n/);
  const html: string[] = [];
  let inList = false;

  for (const rawLine of lines) {
    const line = rawLine.trim();
    if (!line) {
      if (inList) {
        html.push("</ul>");
        inList = false;
      }
      continue;
    }
    if (line.startsWith("### ")) {
      if (inList) {
        html.push("</ul>");
        inList = false;
      }
      html.push(`<h3>${escapeHtml(line.slice(4))}</h3>`);
    } else if (line.startsWith("## ")) {
      if (inList) {
        html.push("</ul>");
        inList = false;
      }
      html.push(`<h2>${escapeHtml(line.slice(3))}</h2>`);
    } else if (line.startsWith("# ")) {
      if (inList) {
        html.push("</ul>");
        inList = false;
      }
      html.push(`<h1>${escapeHtml(line.slice(2))}</h1>`);
    } else if (line.startsWith("- ")) {
      if (!inList) {
        html.push("<ul>");
        inList = true;
      }
      html.push(`<li>${escapeHtml(line.slice(2))}</li>`);
    } else {
      if (inList) {
        html.push("</ul>");
        inList = false;
      }
      html.push(`<p>${escapeHtml(line)}</p>`);
    }
  }
  if (inList) {
    html.push("</ul>");
  }
  return html.join("");
}

function escapeHtml(value: string): string {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;");
}
