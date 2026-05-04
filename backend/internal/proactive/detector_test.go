package proactive

import (
	"context"
	"testing"
	"time"

	"agentpilot/backend/internal/domain"
)

func TestDetectRulesScoresOfficeTaskSignals(t *testing.T) {
	messages := []domain.ChatMessage{
		{SenderOpenID: "ou_1", Content: "这个项目下周要给老板看"},
		{SenderOpenID: "ou_2", Content: "我把资料放这里 https://example.com/doc"},
		{SenderOpenID: "ou_1", Content: "帮忙整理一下做个PPT"},
	}

	hit := DetectRules(messages)
	if hit.Score < 0.40 {
		t.Fatalf("expected rule score to pass, got %.2f", hit.Score)
	}
	if !hit.HasTimeSignal || !hit.HasResourceSignal || !hit.MultiSpeaker {
		t.Fatalf("expected time/resource/multi speaker signals, got %#v", hit)
	}
}

func TestDetectorReturnsReadyCandidate(t *testing.T) {
	detector := NewDetector(Config{RuleThreshold: 0.4, LLMConfidence: 0.55}, &fakeJudge{
		judgement: Judgement{
			IsTask:     true,
			Title:      "项目复盘汇报",
			Goal:       "整理项目复盘并生成汇报材料",
			TaskType:   "ppt",
			ThemeKey:   "项目复盘汇报",
			Confidence: 0.8,
			Reason:     "明确要求整理和汇报",
		},
	})

	candidate, err := detector.Detect(context.Background(), []domain.ChatMessage{
		{MessageID: "om1", ChatID: "oc1", SenderOpenID: "ou_1", Content: "下周要做个复盘汇报", CreatedAt: time.Now()},
		{MessageID: "om2", ChatID: "oc1", SenderOpenID: "ou_2", Content: "资料在 https://example.com/a.pdf", CreatedAt: time.Now()},
	})
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if !candidate.Ready {
		t.Fatalf("expected ready candidate: %#v", candidate)
	}
	if candidate.ThemeKey == "" || candidate.Instruction == "" || candidate.ContextJSON == "" {
		t.Fatalf("expected populated candidate: %#v", candidate)
	}
}

func TestDetectorPassesPreviousThemeKeyToJudge(t *testing.T) {
	judge := &fakeJudge{
		judgement: Judgement{
			IsTask:     true,
			Title:      "项目复盘汇报",
			Goal:       "继续整理项目复盘材料",
			TaskType:   "ppt",
			ThemeKey:   "项目复盘汇报",
			Confidence: 0.8,
		},
	}
	detector := NewDetector(Config{RuleThreshold: 0.4, LLMConfidence: 0.55}, judge)

	_, err := detector.DetectWithPreviousThemeKey(context.Background(), []domain.ChatMessage{
		{MessageID: "om1", ChatID: "oc1", SenderOpenID: "ou_1", Content: "继续把复盘资料整理成PPT", CreatedAt: time.Now()},
		{MessageID: "om2", ChatID: "oc1", SenderOpenID: "ou_2", Content: "资料在 https://example.com/a.pdf", CreatedAt: time.Now()},
	}, "项目复盘汇报")
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if judge.previousThemeKey != "项目复盘汇报" {
		t.Fatalf("expected previous theme key to reach judge, got %q", judge.previousThemeKey)
	}
}

func TestParseJudgementStripsCodeFence(t *testing.T) {
	judgement, err := ParseJudgement("```json\n{\"isTask\":true,\"title\":\"周报\",\"confidence\":0.7}\n```")
	if err != nil {
		t.Fatalf("parse judgement: %v", err)
	}
	if !judgement.IsTask || judgement.Title != "周报" {
		t.Fatalf("unexpected judgement: %#v", judgement)
	}
}

type fakeJudge struct {
	judgement        Judgement
	err              error
	previousThemeKey string
}

func (f *fakeJudge) Judge(_ context.Context, _ []domain.ChatMessage, previousThemeKey string) (Judgement, error) {
	f.previousThemeKey = previousThemeKey
	return f.judgement, f.err
}
