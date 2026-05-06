import { useMemo, useState } from "react";
import { Pressable, StyleSheet, Text, TextInput, View } from "react-native";
import { useMarkdownCollab } from "./useMarkdownCollab";

interface MarkdownEditorProps {
  taskId: string;
  clientId: string;
}

export function MarkdownEditor({ taskId, clientId }: MarkdownEditorProps) {
  const [mode, setMode] = useState<"edit" | "preview">("edit");
  const { document, markdown, updateMarkdown, persistSnapshot, exportMarkdown, saveStatus, error } = useMarkdownCollab(taskId, clientId);
  const previewLines = useMemo(() => renderMarkdownPreview(markdown), [markdown]);

  return (
    <View style={styles.container}>
      <View style={styles.titleRow}>
        <View style={styles.titleText}>
          <Text style={styles.heading}>Markdown 编辑</Text>
          <Text style={styles.meta} numberOfLines={1}>
            {document?.title ?? statusText[saveStatus]}
          </Text>
        </View>
        <Text style={styles.metaStrong}>{statusText[saveStatus]}</Text>
      </View>
      {error ? <Text style={styles.notice}>{error}</Text> : null}
      {document && !document.sourcePath && !markdown ? (
        <Text style={styles.notice}>当前任务还没有可编辑的 Markdown 源，文档生成后会自动加载。</Text>
      ) : null}
      <View style={styles.segment}>
        <Pressable style={[styles.segmentButton, mode === "edit" && styles.segmentSelected]} onPress={() => setMode("edit")}>
          <Text style={[styles.segmentText, mode === "edit" && styles.segmentSelectedText]}>编辑</Text>
        </Pressable>
        <Pressable style={[styles.segmentButton, mode === "preview" && styles.segmentSelected]} onPress={() => setMode("preview")}>
          <Text style={[styles.segmentText, mode === "preview" && styles.segmentSelectedText]}>预览</Text>
        </Pressable>
      </View>
      {mode === "edit" ? (
        <TextInput
          style={styles.editor}
          value={markdown}
          onChangeText={updateMarkdown}
          multiline
          textAlignVertical="top"
          autoCapitalize="none"
          autoCorrect={false}
          placeholder="生成 Markdown 后可在这里协同编辑"
        />
      ) : (
        <View style={styles.preview}>
          {previewLines.map((line, index) => (
            <Text key={`${index}-${line.text}`} style={[styles.previewText, line.kind === "h1" && styles.previewH1, line.kind === "h2" && styles.previewH2]}>
              {line.text}
            </Text>
          ))}
        </View>
      )}
      <View style={styles.actions}>
        <Pressable
          style={[styles.actionButton, (!document || saveStatus === "loading" || saveStatus === "saving") && styles.disabledButton]}
          disabled={!document || saveStatus === "loading" || saveStatus === "saving"}
          onPress={() => void persistSnapshot()}
        >
          <Text style={styles.actionText}>保存快照</Text>
        </Pressable>
        <Pressable
          style={[styles.actionButton, (!document || saveStatus === "loading" || saveStatus === "saving") && styles.disabledButton]}
          disabled={!document || saveStatus === "loading" || saveStatus === "saving"}
          onPress={() => void exportMarkdown()}
        >
          <Text style={styles.actionText}>导出</Text>
        </Pressable>
      </View>
    </View>
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

function renderMarkdownPreview(markdown: string): Array<{ kind: "h1" | "h2" | "p"; text: string }> {
  return markdown
    .split(/\r?\n/)
    .map((line) => {
      const trimmed = line.trim();
      if (trimmed.startsWith("# ")) {
        return { kind: "h1" as const, text: trimmed.slice(2) };
      }
      if (trimmed.startsWith("## ")) {
        return { kind: "h2" as const, text: trimmed.slice(3) };
      }
      if (trimmed.startsWith("- ")) {
        return { kind: "p" as const, text: `• ${trimmed.slice(2)}` };
      }
      return trimmed ? { kind: "p" as const, text: trimmed } : null;
    })
    .filter((line): line is { kind: "h1" | "h2" | "p"; text: string } => line !== null);
}

const styles = StyleSheet.create({
  container: {
    gap: 10,
    borderWidth: 1,
    borderColor: "#d8e0e8",
    borderRadius: 8,
    backgroundColor: "#ffffff",
    padding: 12,
  },
  titleRow: {
    flexDirection: "row",
    justifyContent: "space-between",
    gap: 10,
  },
  titleText: {
    flex: 1,
    minWidth: 0,
  },
  heading: {
    color: "#17202a",
    fontSize: 15,
    fontWeight: "700",
  },
  meta: {
    marginTop: 4,
    color: "#667483",
    fontSize: 12,
  },
  metaStrong: {
    color: "#667483",
    fontSize: 12,
    fontWeight: "700",
  },
  notice: {
    borderRadius: 8,
    backgroundColor: "#fff8e8",
    padding: 10,
    color: "#8a4d00",
    fontSize: 13,
  },
  segment: {
    flexDirection: "row",
    gap: 6,
    borderRadius: 6,
    backgroundColor: "#eef3f8",
    padding: 4,
  },
  segmentButton: {
    flex: 1,
    alignItems: "center",
    borderRadius: 5,
    padding: 8,
  },
  segmentSelected: {
    backgroundColor: "#ffffff",
  },
  segmentText: {
    color: "#495664",
    fontSize: 14,
  },
  segmentSelectedText: {
    color: "#1769aa",
    fontWeight: "700",
  },
  editor: {
    minHeight: 320,
    borderWidth: 1,
    borderColor: "#cdd6df",
    borderRadius: 8,
    backgroundColor: "#ffffff",
    padding: 10,
    color: "#17202a",
    fontFamily: "monospace",
    fontSize: 14,
    lineHeight: 22,
  },
  preview: {
    minHeight: 320,
    borderWidth: 1,
    borderColor: "#cdd6df",
    borderRadius: 8,
    backgroundColor: "#ffffff",
    padding: 12,
  },
  previewText: {
    marginBottom: 10,
    color: "#17202a",
    fontSize: 14,
    lineHeight: 22,
  },
  previewH1: {
    fontSize: 20,
    fontWeight: "800",
    lineHeight: 28,
  },
  previewH2: {
    marginTop: 8,
    fontSize: 17,
    fontWeight: "700",
    lineHeight: 24,
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
});
