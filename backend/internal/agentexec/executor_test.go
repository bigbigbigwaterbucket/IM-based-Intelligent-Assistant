package agentexec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentpilot/backend/internal/domain"
)

func TestNormalizePlanToolAcceptsSafeToolName(t *testing.T) {
	t.Parallel()

	if got := normalizePlanTool("doc_create", "doc.create"); got != "doc.create" {
		t.Fatalf("expected dotted tool name, got %q", got)
	}
	if got := normalizePlanTool("slide-generate", "slide.generate"); got != "slide.generate" {
		t.Fatalf("expected dotted tool name, got %q", got)
	}
}

func TestLoadArtifactContextReadsExistingArtifacts(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	docPath := filepath.Join(dir, "doc.md")
	slidesPath := filepath.Join(dir, "slides.md")
	if err := os.WriteFile(docPath, []byte("# Existing Doc\n\nKeep this section."), 0644); err != nil {
		t.Fatalf("write doc: %v", err)
	}
	if err := os.WriteFile(slidesPath, []byte("---\ntheme: seriph\n---\n\n# Existing Slides"), 0644); err != nil {
		t.Fatalf("write slides: %v", err)
	}

	ctx := loadArtifactContext(domain.Task{
		DocArtifactPath:    docPath,
		SlidesArtifactPath: slidesPath,
	}, nil, "chat:oc_test")
	prompt := ctx.PromptText()
	if !strings.Contains(prompt, "Existing Doc") {
		t.Fatalf("expected existing document in prompt: %s", prompt)
	}
	if !strings.Contains(prompt, "Existing Slides") {
		t.Fatalf("expected existing slides in prompt: %s", prompt)
	}
}

func TestEnrichStepInputWithContextForRevisionTools(t *testing.T) {
	t.Parallel()

	ctx := artifactContext{Document: "# Old Doc", Slides: "# Old Slides", History: "user: revise"}
	docInput := stepToolInput{}
	enrichStepInputWithContext(&docInput, "doc.update", ctx)
	if docInput.ExistingDoc != "# Old Doc" {
		t.Fatalf("expected doc context, got %q", docInput.ExistingDoc)
	}
	if docInput.Content != "" {
		t.Fatalf("did not expect existing doc to be treated as generated content: %q", docInput.Content)
	}
	if docInput.RecentHistory != "user: revise" {
		t.Fatalf("expected recent history context, got %q", docInput.RecentHistory)
	}

	slideInput := stepToolInput{}
	enrichStepInputWithContext(&slideInput, "slide.regenerate", ctx)
	if slideInput.ExistingSlides != "# Old Slides" {
		t.Fatalf("expected slide context, got %q", slideInput.ExistingSlides)
	}
	if slideInput.SlideMarkdown != "" {
		t.Fatalf("did not expect existing slides to be treated as generated content: %q", slideInput.SlideMarkdown)
	}
}
