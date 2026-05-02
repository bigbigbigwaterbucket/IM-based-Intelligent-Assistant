package planner

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"agentpilot/backend/internal/domain"
)

func TestBuildPlanProducesFullArtifactFlow(t *testing.T) {
	t.Parallel()

	plan, err := NewServiceWithLLM(nil).BuildPlan(context.Background(), "群聊总结", "把群聊消息总结成方案+ppt")
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

func TestBuildPlanForGreetingDoesNotRequestArtifacts(t *testing.T) {
	t.Parallel()

	plan, err := NewServiceWithLLM(nil).BuildPlan(context.Background(), "你好", "你好")
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if len(plan.Analysis.Deliverables) != 0 {
		t.Fatalf("did not expect deliverables for greeting, got %#v", plan.Analysis.Deliverables)
	}
	for _, step := range plan.Steps {
		switch step.Tool {
		case "doc.create", "doc.append", "slide.generate", "slide.rehearse", "archive.bundle":
			t.Fatalf("did not expect artifact step for greeting: %#v", step)
		}
	}
}

func TestLLMGreetingPlanIsValidWithoutArtifacts(t *testing.T) {
	t.Parallel()

	plan := domain.Plan{
		Summary: "用户打招呼",
		Analysis: domain.IntentAnalysis{
			Objective:    "确认在线状态",
			Audience:     "用户本人",
			Deliverables: []string{},
		},
		Steps: []domain.PlanStep{
			{ID: "s1", Tool: "intent.analyze", Description: "分析意图"},
			{ID: "s2", Tool: "planner.build", Description: "无需生成产物"},
		},
	}
	if !validPlan(plan) {
		t.Fatal("expected greeting plan without artifacts to be valid")
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
			"id":"chatcmpl-test",
			"object":"chat.completion",
			"created":1,
			"model":"test-model",
			"choices":[
				{
					"index":0,
					"finish_reason":"stop",
					"message":{
						"role":"assistant",
						"content":"{\"summary\":\"LLM规划完成\",\"analysis\":{\"objective\":\"生成可评审方案\",\"audience\":\"管理层\",\"deliverables\":[\"方案文档\",\"演示稿\"],\"contextNeeded\":true,\"risks\":[\"信息不足\"],\"clarifyingHint\":\"确认截止时间\"},\"steps\":[{\"id\":\"s1\",\"tool\":\"intent.analyze\",\"description\":\"分析意图\",\"dependsOn\":[]},{\"id\":\"s2\",\"tool\":\"doc.create\",\"description\":\"创建文档\",\"dependsOn\":[\"s1\"]},{\"id\":\"s3\",\"tool\":\"doc.append\",\"description\":\"写入文档\",\"dependsOn\":[\"s2\"]},{\"id\":\"s4\",\"tool\":\"slide.generate\",\"description\":\"生成演示稿\",\"dependsOn\":[\"s3\"]},{\"id\":\"s5\",\"tool\":\"slide.rehearse\",\"description\":\"生成演讲稿\",\"dependsOn\":[\"s4\"]},{\"id\":\"s6\",\"tool\":\"archive.bundle\",\"description\":\"汇总产物\",\"dependsOn\":[\"s3\",\"s5\"]}],\"docTitle\":\"LLM文档\",\"slideTitle\":\"LLM演示稿\",\"documentSections\":[{\"heading\":\"背景\",\"bullets\":[\"A\",\"B\"]}],\"slides\":[{\"title\":\"首页\",\"bullets\":[\"A\",\"B\"],\"speakerNote\":\"讲解首页\"}]}"
					}
				}
			],
			"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}
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
	if plan.PlannerSource != "llm" {
		t.Fatalf("expected planner source llm, got %s", plan.PlannerSource)
	}
}

func TestBuildPlanFallsBackWhenLLMInvalid(t *testing.T) {
	t.Parallel()

	client := roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return jsonResponse(`{
			"id":"chatcmpl-test",
			"object":"chat.completion",
			"created":1,
			"model":"test-model",
			"choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"not json"}}]
		}`), nil
	})
	service := NewServiceWithLLM(NewLLMPlanner("test-key", "https://api.example.com", "test-model", client))

	plan, err := service.BuildPlan(context.Background(), "测试", "生成方案和PPT")
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if plan.PlannerSource != "heuristic_fallback" {
		t.Fatalf("expected fallback source, got %s", plan.PlannerSource)
	}
	if plan.PlannerError == "" {
		t.Fatal("expected planner error to be recorded")
	}
	if plan.Summary == "" || plan.DocTitle == "" || len(plan.Slides) == 0 {
		t.Fatalf("expected fallback plan, got %#v", plan)
	}
}

func TestNormalizeBaseURLStripsChatCompletionsSuffix(t *testing.T) {
	t.Parallel()

	got := normalizeBaseURL("https://api.example.com/v1/chat/completions")
	if got != "https://api.example.com/v1" {
		t.Fatalf("unexpected normalized base url: %s", got)
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
