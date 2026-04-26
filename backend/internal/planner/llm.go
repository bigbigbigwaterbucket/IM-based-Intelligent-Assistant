package planner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"agentpilot/backend/internal/domain"
)

const plannerSystemPrompt = `你是 IM-based Intelligent Assistant 的任务规划器。
用户会在飞书 IM 里用自然语言发起任务。你要做意图分析、理解任务，并规划文档与演示稿生成流程。

只输出严格 JSON，不要输出 Markdown，不要包裹代码块。JSON 结构如下：
{
  "summary": "一句话总结规划结果",
  "analysis": {
    "objective": "任务目标",
    "audience": "目标受众",
    "deliverables": ["方案文档", "演示稿"],
    "contextNeeded": true,
    "risks": ["风险1", "风险2"],
    "clarifyingHint": "需要用户确认的关键事项"
  },
  "steps": [
    {"id":"s1","tool":"intent.analyze","description":"分析意图","dependsOn":[]},
    {"id":"s2","tool":"planner.build","description":"规划任务","dependsOn":["s1"]},
    {"id":"s3","tool":"doc.generate","description":"生成文档","dependsOn":["s2"]},
    {"id":"s4","tool":"slide.generate","description":"生成演示稿","dependsOn":["s3"]},
    {"id":"s5","tool":"archive.bundle","description":"汇总产物","dependsOn":["s3","s4"]}
  ],
  "docTitle": "文档标题",
  "slideTitle": "演示稿标题",
  "documentSections": [
    {"heading":"背景与目标","bullets":["要点1","要点2"]},
    {"heading":"执行方案","bullets":["要点1","要点2"]},
    {"heading":"风险与建议","bullets":["要点1","要点2"]}
  ],
  "slides": [
    {"title":"首页","bullets":["要点1","要点2"],"speakerNote":"演讲备注"}
  ]
}

约束：
1. steps 必须形成从意图分析到 archive.bundle 的完整链路。
2. documentSections 至少 4 节，每节 2-5 个要点。
3. slides 至少 5 页，每页 2-5 个要点，并生成 speakerNote。
4. 不要虚构具体日期、金额、人名；缺失信息放到 clarifyingHint 或风险里。
5. 如果用户提到群聊/讨论/最近/本周，contextNeeded=true，并在 steps 中加入 im.context_summarize。`

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type LLMPlanner struct {
	apiKey  string
	baseURL string
	model   string
	client  HTTPDoer
}

func NewLLMPlannerFromEnv() *LLMPlanner {
	apiKey := firstEnv("DEEPSEEK_API_KEY", "ARK_API_KEY")
	baseURL := firstEnv("DEEPSEEK_BASE_URL", "ARK_BASE_URL")
	model := firstEnv("DEEPSEEK_MODEL", "ARK_MODEL")
	if baseURL == "" {
		baseURL = "https://api.deepseek.com"
	}
	if model == "" {
		model = "deepseek-chat"
	}
	return NewLLMPlanner(apiKey, baseURL, model, &http.Client{Timeout: 30 * time.Second})
}

func NewLLMPlanner(apiKey, baseURL, model string, client HTTPDoer) *LLMPlanner {
	return &LLMPlanner{
		apiKey:  strings.TrimSpace(apiKey),
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		model:   strings.TrimSpace(model),
		client:  client,
	}
}

func (p *LLMPlanner) Enabled() bool {
	return p != nil && p.apiKey != "" && p.baseURL != "" && p.model != "" && p.client != nil
}

func (p *LLMPlanner) BuildPlan(ctx context.Context, title, instruction string) (domain.Plan, error) {
	if !p.Enabled() {
		return domain.Plan{}, errors.New("llm planner is not configured")
	}

	body := chatRequest{
		Model: p.model,
		Messages: []chatMessage{
			{Role: "system", Content: plannerSystemPrompt},
			{Role: "user", Content: fmt.Sprintf("任务标题：%s\n用户需求：%s", title, instruction)},
		},
		Temperature: 0.2,
		MaxTokens:   4096,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return domain.Plan{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL, bytes.NewReader(payload))
	if err != nil {
		return domain.Plan{}, err
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return domain.Plan{}, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return domain.Plan{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return domain.Plan{}, fmt.Errorf("llm planner http %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}

	var out chatResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return domain.Plan{}, err
	}
	if len(out.Choices) == 0 {
		return domain.Plan{}, errors.New("llm planner returned no choices")
	}

	plan, err := parseLLMPlan(out.Choices[0].Message.Content)
	if err != nil {
		return domain.Plan{}, err
	}
	normalizeLLMPlan(&plan, title, instruction)
	return plan, nil
}

func parseLLMPlan(raw string) (domain.Plan, error) {
	raw = stripCodeFence(strings.TrimSpace(raw))
	var plan domain.Plan
	if err := json.Unmarshal([]byte(raw), &plan); err != nil {
		return domain.Plan{}, err
	}
	return plan, nil
}

func normalizeLLMPlan(plan *domain.Plan, title, instruction string) {
	if plan.DocTitle == "" {
		plan.DocTitle = title + " - 方案文档"
	}
	if plan.SlideTitle == "" {
		plan.SlideTitle = title + " - 汇报演示稿"
	}
	if plan.Analysis.Deliverables == nil || len(plan.Analysis.Deliverables) == 0 {
		plan.Analysis.Deliverables = []string{"方案文档", "演示稿"}
	}
	if plan.Summary == "" {
		plan.Summary = summarize(title, plan.Analysis)
	}
	if len(plan.Steps) == 0 {
		plan.Steps = buildSteps(plan.Analysis)
	}
	if len(plan.DocumentSections) == 0 {
		plan.DocumentSections = buildDocumentSections(instruction, plan.Analysis)
	}
	if len(plan.Slides) == 0 {
		plan.Slides = buildSlides(title, instruction, plan.Analysis)
	}
}

func stripCodeFence(raw string) string {
	if strings.HasPrefix(raw, "```") {
		raw = strings.TrimPrefix(raw, "```json")
		raw = strings.TrimPrefix(raw, "```")
		raw = strings.TrimSpace(raw)
		if idx := strings.LastIndex(raw, "```"); idx >= 0 {
			raw = raw[:idx]
		}
	}
	return strings.TrimSpace(raw)
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := os.Getenv(key); strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
}
