import { jsx as _jsx, jsxs as _jsxs } from "react/jsx-runtime";
import { useMemo } from "react";
import { useMarkdownCollab } from "./useMarkdownCollab";
export function MarkdownEditor({ taskId, clientId }) {
    const { document, markdown, updateMarkdown, persistSnapshot, exportMarkdown, saveStatus, error } = useMarkdownCollab(taskId, clientId);
    const preview = useMemo(() => renderMarkdownPreview(markdown), [markdown]);
    return (_jsxs("section", { className: "surface markdown-workbench", children: [_jsxs("div", { className: "section-title", children: [_jsx("h2", { children: "Markdown \u534F\u540C\u7F16\u8F91" }), _jsx("span", { children: statusText[saveStatus] })] }), error ? _jsx("p", { className: "error-text", children: error }) : null, document && !document.sourcePath && !markdown ? (_jsx("p", { className: "warning-text", children: "\u5F53\u524D\u4EFB\u52A1\u8FD8\u6CA1\u6709\u53EF\u7F16\u8F91\u7684 Markdown \u6E90\uFF0C\u6587\u6863\u751F\u6210\u540E\u4F1A\u81EA\u52A8\u52A0\u8F7D\u3002" })) : null, _jsxs("div", { className: "markdown-toolbar", children: [_jsx("span", { children: document?.title ?? "加载文档" }), _jsxs("div", { children: [_jsx("button", { onClick: () => void persistSnapshot(), disabled: !document || saveStatus === "loading" || saveStatus === "saving", children: "\u4FDD\u5B58\u5FEB\u7167" }), _jsx("button", { onClick: () => void exportMarkdown(), disabled: !document || saveStatus === "loading" || saveStatus === "saving", children: "\u5BFC\u51FA Markdown" })] })] }), _jsxs("div", { className: "markdown-grid", children: [_jsx("textarea", { className: "markdown-editor", value: markdown, onChange: (event) => updateMarkdown(event.target.value), spellCheck: false, placeholder: "\u751F\u6210 Markdown \u540E\u53EF\u5728\u8FD9\u91CC\u534F\u540C\u7F16\u8F91" }), _jsx("div", { className: "markdown-preview", dangerouslySetInnerHTML: { __html: preview } })] })] }));
}
const statusText = {
    idle: "未打开",
    loading: "加载中",
    synced: "已同步",
    saving: "保存中",
    exported: "已导出",
    error: "同步异常",
};
function renderMarkdownPreview(markdown) {
    const lines = markdown.split(/\r?\n/);
    const html = [];
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
        }
        else if (line.startsWith("## ")) {
            if (inList) {
                html.push("</ul>");
                inList = false;
            }
            html.push(`<h2>${escapeHtml(line.slice(3))}</h2>`);
        }
        else if (line.startsWith("# ")) {
            if (inList) {
                html.push("</ul>");
                inList = false;
            }
            html.push(`<h1>${escapeHtml(line.slice(2))}</h1>`);
        }
        else if (line.startsWith("- ")) {
            if (!inList) {
                html.push("<ul>");
                inList = true;
            }
            html.push(`<li>${escapeHtml(line.slice(2))}</li>`);
        }
        else {
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
function escapeHtml(value) {
    return value
        .replaceAll("&", "&amp;")
        .replaceAll("<", "&lt;")
        .replaceAll(">", "&gt;")
        .replaceAll('"', "&quot;");
}
