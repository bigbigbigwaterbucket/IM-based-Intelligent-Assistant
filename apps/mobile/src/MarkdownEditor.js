import { jsx as _jsx, jsxs as _jsxs } from "react/jsx-runtime";
import { useMemo, useState } from "react";
import { useMarkdownCollab } from "./useMarkdownCollab";
export function MarkdownEditor({ taskId, clientId }) {
    const [mode, setMode] = useState("edit");
    const { document, markdown, updateMarkdown, persistSnapshot, exportMarkdown, saveStatus, error } = useMarkdownCollab(taskId, clientId);
    const preview = useMemo(() => renderMarkdownPreview(markdown), [markdown]);
    return (_jsxs("section", { className: "mobile-markdown", children: [_jsxs("div", { className: "mobile-markdown-title", children: [_jsxs("div", { children: [_jsx("h3", { children: "Markdown \u7F16\u8F91" }), _jsx("span", { children: document?.title ?? statusText[saveStatus] })] }), _jsx("strong", { children: statusText[saveStatus] })] }), error ? _jsx("p", { className: "mobile-notice", children: error }) : null, document && !document.sourcePath && !markdown ? (_jsx("p", { className: "mobile-notice", children: "\u5F53\u524D\u4EFB\u52A1\u8FD8\u6CA1\u6709\u53EF\u7F16\u8F91\u7684 Markdown \u6E90\uFF0C\u6587\u6863\u751F\u6210\u540E\u4F1A\u81EA\u52A8\u52A0\u8F7D\u3002" })) : null, _jsxs("div", { className: "mobile-segment", children: [_jsx("button", { className: mode === "edit" ? "selected" : "", onClick: () => setMode("edit"), children: "\u7F16\u8F91" }), _jsx("button", { className: mode === "preview" ? "selected" : "", onClick: () => setMode("preview"), children: "\u9884\u89C8" })] }), mode === "edit" ? (_jsx("textarea", { className: "mobile-markdown-editor", value: markdown, onChange: (event) => updateMarkdown(event.target.value), spellCheck: false, placeholder: "\u751F\u6210 Markdown \u540E\u53EF\u5728\u8FD9\u91CC\u534F\u540C\u7F16\u8F91" })) : (_jsx("div", { className: "mobile-markdown-preview", dangerouslySetInnerHTML: { __html: preview } })), _jsxs("div", { className: "mobile-actions", children: [_jsx("button", { disabled: !document || saveStatus === "loading" || saveStatus === "saving", onClick: () => void persistSnapshot(), children: "\u4FDD\u5B58\u5FEB\u7167" }), _jsx("button", { disabled: !document || saveStatus === "loading" || saveStatus === "saving", onClick: () => void exportMarkdown(), children: "\u5BFC\u51FA" })] })] }));
}
const statusText = {
    idle: "未打开",
    loading: "加载中",
    synced: "已同步",
    saving: "保存中",
    exported: "已导出",
    error: "异常",
};
function renderMarkdownPreview(markdown) {
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
function escapeHtml(value) {
    return value
        .replaceAll("&", "&amp;")
        .replaceAll("<", "&lt;")
        .replaceAll(">", "&gt;")
        .replaceAll('"', "&quot;");
}
