package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentpilot/backend/internal/domain"
)

func TestCreateDocAndSlidesWriteArtifacts(t *testing.T) {
	t.Parallel()

	runner := NewRunner(Config{ArtifactDir: t.TempDir()})
	plan := domain.Plan{
		Summary:    "生成测试产物",
		DocTitle:   "测试文档",
		SlideTitle: "测试演示稿",
		Analysis: domain.IntentAnalysis{
			Objective:    "验证真实产物生成",
			Audience:     "测试人员",
			Deliverables: []string{"方案文档", "演示稿"},
		},
		Steps: []domain.PlanStep{{ID: "s1", Tool: "intent.analyze", Description: "分析"}},
		DocumentSections: []domain.DocumentSection{
			{Heading: "背景", Bullets: []string{"不是占位链接"}},
		},
		Slides: []domain.Slide{
			{Title: "首页", Bullets: []string{"真实 Slidev Markdown"}, SpeakerNote: "说明首页。"},
		},
	}

	doc := runner.CreateDoc(context.Background(), plan, "生成文档和演示稿")
	if !doc.Success {
		t.Fatalf("create doc failed: %s", doc.ErrorMessage)
	}
	if !strings.HasPrefix(doc.ArtifactURL, "/artifacts/") {
		t.Fatalf("unexpected doc url: %s", doc.ArtifactURL)
	}
	assertFileContains(t, doc.ArtifactPath, "## 意图分析")

	slides := runner.CreateSlides(context.Background(), plan)
	if !slides.Success {
		t.Fatalf("create slides failed: %s", slides.ErrorMessage)
	}
	if !strings.HasPrefix(slides.ArtifactURL, "/artifacts/") {
		t.Fatalf("unexpected slides url: %s", slides.ArtifactURL)
	}
	assertFileContains(t, slides.ArtifactPath, "theme: seriph")

	notesURL := slides.Data["speaker_notes"]
	if notesURL == "" {
		t.Fatal("expected speaker notes artifact")
	}
	notesPath := filepath.Join(runner.config.ArtifactDir, strings.TrimPrefix(notesURL, "/artifacts/"))
	assertFileContains(t, notesPath, "说明首页")
}

func assertFileContains(t *testing.T, path, want string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !strings.Contains(string(data), want) {
		t.Fatalf("expected %s to contain %q", path, want)
	}
}
