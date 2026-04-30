package agentexec

import (
	"testing"
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
