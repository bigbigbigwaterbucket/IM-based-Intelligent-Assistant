package planner

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
)

func TestBuildPlanProducesFullArtifactFlow(t *testing.T) {
	t.Parallel()

	plan, err := NewService().BuildPlan(context.Background(), "群聊总结", "把群聊消息总结成方案+ppt")
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}

	if plan.Analysis.Objective == "" {
		t.Fatal("expected intent analysis objective")
	}
	if len(plan.Steps) < 6 {
		t.Fatalf("expected full step plan, got %d steps", len(plan.Steps))
	}
	if len(plan.DocumentSections) == 0 {
		t.Fatal("expected document sections")
	}
	if len(plan.Slides) == 0 {
		t.Fatal("expected slides")
	}
	if plan.DocTitle == "" || plan.SlideTitle == "" {
		t.Fatal("expected artifact titles")
	}
}

func TestBuildPlanUsesLLMWhenConfigured(t *testing.T) {
	t.Parallel()

	client := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != "https://api.example.com/v1/chat/completions" {
			t.Fatalf("unexpected url: %s", req.URL.String())
		}
		if req.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("missing bearer token")
		}
		return jsonResponse(`{
			"choices":[{
				"message":{
					"content":"{\"summary\":\"LLM规划完成\",\"analysis\":{\"objective\":\"生成可评审方案\",\"audience\":\"管理层\",\"deliverables\":[\"方案文档\",\"演示稿\"],\"contextNeeded\":true,\"risks\":[\"信息不足\"],\"clarifyingHint\":\"确认截止时间\"},\"steps\":[{\"id\":\"s1\",\"tool\":\"intent.analyze\",\"description\":\"分析意图\",\"dependsOn\":[]}],\"docTitle\":\"LLM文档\",\"slideTitle\":\"LLM演示稿\",\"documentSections\":[{\"heading\":\"背景\",\"bullets\":[\"A\",\"B\"]}],\"slides\":[{\"title\":\"首页\",\"bullets\":[\"A\",\"B\"],\"speakerNote\":\"讲解首页\"}]}"
				}
			}]
		}`), nil
	})
	service := NewServiceWithLLM(NewLLMPlanner("test-key", "https://api.example.com/v1", "test-model", client))

	plan, err := service.BuildPlan(context.Background(), "测试", "生成方案和PPT")
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if plan.Summary != "LLM规划完成" {
		t.Fatalf("expected llm summary, got %s", plan.Summary)
	}
	if plan.DocTitle != "LLM文档" {
		t.Fatalf("expected llm doc title, got %s", plan.DocTitle)
	}
}

func TestBuildPlanFallsBackWhenLLMInvalid(t *testing.T) {
	t.Parallel()

	client := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return jsonResponse(`{"choices":[{"message":{"content":"not json"}}]}`), nil
	})
	service := NewServiceWithLLM(NewLLMPlanner("test-key", "https://api.example.com", "test-model", client))

	plan, err := service.BuildPlan(context.Background(), "测试", "生成方案和PPT")
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if plan.Summary == "" || plan.DocTitle == "" || len(plan.Slides) == 0 {
		t.Fatalf("expected fallback plan, got %#v", plan)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Header:     make(http.Header),
	}
}
