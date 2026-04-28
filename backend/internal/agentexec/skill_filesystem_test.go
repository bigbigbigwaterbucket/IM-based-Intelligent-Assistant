package agentexec

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudwego/eino/adk/filesystem"
	skillmw "github.com/cloudwego/eino/adk/middlewares/skill"
)

func TestLocalSkillFilesystemLoadsSkills(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	skillDir := filepath.Join(root, "document_generation")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `---
name: document_generation
description: test document skill
---
# Test Skill

Use real context only.
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	fsBackend := newLocalSkillFilesystem(root)
	entries, err := fsBackend.GlobInfo(ctx, &filesystem.GlobInfoRequest{
		Pattern: "*/SKILL.md",
		Path:    root,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 skill file, got %d", len(entries))
	}
	if got, want := filepath.ToSlash(entries[0].Path), "document_generation/SKILL.md"; got != want {
		t.Fatalf("unexpected skill path: got %q want %q", got, want)
	}

	skillBackend, err := skillmw.NewBackendFromFilesystem(ctx, &skillmw.BackendFromFilesystemConfig{
		Backend: fsBackend,
		BaseDir: root,
	})
	if err != nil {
		t.Fatal(err)
	}
	matters, err := skillBackend.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(matters) != 1 || matters[0].Name != "document_generation" {
		t.Fatalf("unexpected skills: %#v", matters)
	}
	skill, err := skillBackend.Get(ctx, "document_generation")
	if err != nil {
		t.Fatal(err)
	}
	if skill.Content == "" {
		t.Fatal("expected loaded skill content")
	}
}

func TestLocalSkillFilesystemRejectsOutsideRoot(t *testing.T) {
	root := t.TempDir()
	fsBackend := newLocalSkillFilesystem(root)
	_, err := fsBackend.Read(context.Background(), &filesystem.ReadRequest{
		FilePath: filepath.Join(root, "..", "outside.md"),
	})
	if err == nil {
		t.Fatal("expected outside-root read to fail")
	}
}

func TestGenerationSkillMiddlewareLoadsRepoSkills(t *testing.T) {
	skillDir := filepath.Join("..", "..", "skills")
	if _, err := generationSkillMiddleware(context.Background(), skillDir); err != nil {
		t.Fatal(err)
	}
}
