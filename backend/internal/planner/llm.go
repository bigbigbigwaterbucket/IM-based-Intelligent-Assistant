package planner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	openai "github.com/cloudwego/eino-ext/components/model/openai"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"agentpilot/backend/internal/domain"
)

const plannerSystemPrompt = `你是 IM-based Intelligent Assistant 的任务规划器。用户会在飞书 IM 中用自然语言发起任务。

你只能使用以下工具名称来规划步骤：
- intent.analyze：分析用户意图
- planner.build：规划任务
- im.fetch_thread：读取并整理当前群聊上下文
- doc.create：创建方案文档
- doc.append：向文档写入内容
- slide.generate：生成演示稿
- slide.rehearse：生成演讲稿/备注
- archive.bundle：汇总产物
- sync.broadcast：广播状态

不要使用未在上面列出的工具名称。

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
    {"id":"s3","tool":"doc.create","description":"创建文档","args":{"title":"..."},"dependsOn":["s2"]},
    {"id":"s4","tool":"doc.append","description":"写入文档内容","args":{},"dependsOn":["s3"]},
    {"id":"s5","tool":"slide.generate","description":"生成演示稿","args":{"title":"..."},"dependsOn":["s4"]},
    {"id":"s6","tool":"archive.bundle","description":"汇总产物","dependsOn":["s3","s5"]}
  ],
  "docTitle": "文档标题",
  "slideTitle": "演示稿标题",
  "documentSections
    {"heading":"背景与目标","bullets":["要点1","要点2"]},
    {"heading":"执行方案","bullets":["要点1","要点2"]},
    {"heading":"风险与建议","bullets":["要点1","要点2"]}
  ],
  "slides": [
    {"title":"首页","bullets":["要点1","要点2"],"speakerNote":"演讲备注"}
  ]
}

约束：
1. 如果用户只是打招呼、闲聊、感谢、测试在线状态，steps 只能包含 intent.analyze、planner.build、sync.broadcast，不能包含 doc.create、doc.append、slide.generate、slide.rehearse、archive.bundle；deliverables 必须为空数组，documentSections 和 slides 必须为空数组。
2. 只有用户明确要求文档、方案、报告、总结时，才加入 doc.create 和 doc.append。
3. 只有用户明确要求 PPT、演示稿、汇报材料、幻灯片时，才加入 slide.generate 和 slide.rehearse。
4. 只有存在文档、演示稿或其他产物时，才加入 archive.bundle。
5. 如果用户提到群聊、讨论、最近、本周，contextNeeded=true，并在 steps 中加入 im.fetch_thread。
6. documentSections 和 slides 只在对应产物需要生成时填写。
7. 不要虚构具体日期、金额、姓名；缺失信息放到 clarifyingHint 或 risks 里。`

type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type chatModel interface {
	Generate(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.Message, error)
}

type chatModelFactory func(context.Context, *openai.ChatModelConfig) (chatModel, error)

type plannerModelConfig struct {
	APIKey     string
	BaseURL    string
	Model      string
	Timeout    time.Duration
	HTTPClient *http.Client
}

func (c plannerModelConfig) enabled() bool {
	return c.APIKey != "" && c.BaseURL != "" && c.Model != ""
}

type EinoPlanner struct {
	config       plannerModelConfig
	newChatModel chatModelFactory
}

func NewLLMPlannerFromEnv() *EinoPlanner {
	return newEinoPlanner(plannerConfigFromEnv(), openAIChatModelFactory)
}

func NewEinoPlanner(apiKey, baseURL, model string, client *http.Client) *EinoPlanner {
	return newEinoPlanner(newPlannerModelConfig(apiKey, baseURL, model, client), openAIChatModelFactory)
}

func NewLLMPlanner(apiKey, baseURL, model string, client HTTPDoer) *EinoPlanner {
	return newEinoPlanner(newPlannerModelConfig(apiKey, baseURL, model, httpClientFromDoer(client)), openAIChatModelFactory)
}

func newEinoPlanner(config plannerModelConfig, factory chatModelFactory) *EinoPlanner {
	if factory == nil {
		factory = openAIChatModelFactory
	}
	return &EinoPlanner{
		config:       config,
		newChatModel: factory,
	}
}

func (p *EinoPlanner) Enabled() bool {
	return p != nil && p.config.enabled() && p.newChatModel != nil
}

func (p *EinoPlanner) BuildPlan(ctx context.Context, title, instruction string) (domain.Plan, error) {
	if !p.Enabled() {
		return domain.Plan{}, errors.New("llm planner is not configured")
	}

	model, err := p.newChatModel(ctx, p.chatModelConfig())
	if err != nil {
		return domain.Plan{}, err
	}

	resp, err := model.Generate(ctx, plannerMessages(title, instruction))
	if err != nil {
		return domain.Plan{}, err
	}

	content := plannerResponseText(resp)
	if content == "" {
		return domain.Plan{}, errors.New("llm planner returned empty content")
	}

	plan, err := parseLLMPlan(content)
	if err != nil {
		return domain.Plan{}, err
	}
	normalizeLLMPlan(&plan, title, instruction)
	return plan, nil
}

func (p *EinoPlanner) chatModelConfig() *openai.ChatModelConfig {
	temperature := float32(0.2)
	maxTokens := 4096
	return &openai.ChatModelConfig{
		APIKey:      p.config.APIKey,
		BaseURL:     p.config.BaseURL,
		Model:       p.config.Model,
		Timeout:     p.config.Timeout,
		HTTPClient:  p.config.HTTPClient,
		Temperature: &temperature,
		MaxTokens:   &maxTokens,
	}
}

func plannerConfigFromEnv() plannerModelConfig {
	arkConfig := newPlannerModelConfig(
		os.Getenv("DEEPSEEK_API_KEY"),
		os.Getenv("DEEPSEEK_BASE_URL"),
		os.Getenv("DEEPSEEK_MODEL"),
		nil,
	)
	if arkConfig.enabled() {
		return arkConfig
	}

	deepseekBaseURL := strings.TrimSpace(os.Getenv("DEEPSEEK_BASE_URL"))
	if deepseekBaseURL == "" {
		deepseekBaseURL = "https://api.deepseek.com"
	}
	deepseekModel := strings.TrimSpace(os.Getenv("DEEPSEEK_MODEL"))
	if deepseekModel == "" {
		deepseekModel = "deepseek-chat"
	}

	return newPlannerModelConfig(
		os.Getenv("DEEPSEEK_API_KEY"),
		deepseekBaseURL,
		deepseekModel,
		nil,
	)
}

func newPlannerModelConfig(apiKey, baseURL, model string, httpClient *http.Client) plannerModelConfig {
	return plannerModelConfig{
		APIKey:     strings.TrimSpace(apiKey),
		BaseURL:    normalizeBaseURL(baseURL),
		Model:      strings.TrimSpace(model),
		Timeout:    30 * time.Second,
		HTTPClient: httpClient,
	}
}

func plannerMessages(title, instruction string) []*schema.Message {
	return []*schema.Message{
		schema.SystemMessage(plannerSystemPrompt),
		schema.UserMessage(fmt.Sprintf("任务标题：%s\n用户需求：%s", title, instruction)),
	}
}

func plannerResponseText(msg *schema.Message) string {
	if msg == nil {
		return ""
	}
	if text := strings.TrimSpace(msg.Content); text != "" {
		return text
	}

	parts := make([]string, 0, len(msg.AssistantGenMultiContent))
	for _, part := range msg.AssistantGenMultiContent {
		if text := strings.TrimSpace(part.Text); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func normalizeBaseURL(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		return ""
	}
	base = strings.TrimRight(base, "/")
	if strings.HasSuffix(base, "/chat/completions") {
		base = strings.TrimSuffix(base, "/chat/completions")
	}

	parsed, err := url.Parse(base)
	if err != nil {
		return base
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if strings.HasSuffix(parsed.Path, "/chat/completions") {
		parsed.Path = strings.TrimSuffix(parsed.Path, "/chat/completions")
	}
	parsed.RawPath = parsed.Path
	return strings.TrimRight(parsed.String(), "/")
}

func openAIChatModelFactory(ctx context.Context, config *openai.ChatModelConfig) (chatModel, error) {
	return openai.NewChatModel(ctx, config)
}

func httpClientFromDoer(doer HTTPDoer) *http.Client {
	if doer == nil {
		return nil
	}
	if client, ok := doer.(*http.Client); ok {
		return client
	}
	return &http.Client{Transport: httpDoerRoundTripper{doer: doer}}
}

type httpDoerRoundTripper struct {
	doer HTTPDoer
}

func (rt httpDoerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return rt.doer.Do(req)
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
	if plan.DocTitle == "" && planNeedsDoc(*plan) {
		plan.DocTitle = title + " - 方案文档"
	}
	if plan.SlideTitle == "" && planNeedsSlides(*plan) {
		plan.SlideTitle = title + " - 汇报演示稿"
	}
	if (plan.Analysis.Deliverables == nil || len(plan.Analysis.Deliverables) == 0) && (planNeedsDoc(*plan) || planNeedsSlides(*plan)) {
		plan.Analysis.Deliverables = []string{"方案文档", "演示稿"}
	}
	if plan.Summary == "" {
		plan.Summary = summarize(title, plan.Analysis)
	}
	if len(plan.Steps) == 0 {
		plan.Steps = buildSteps(plan.Analysis)
	}
	if len(plan.DocumentSections) == 0 && planNeedsDoc(*plan) {
		plan.DocumentSections = buildDocumentSections(instruction, plan.Analysis)
	}
	if len(plan.Slides) == 0 && planNeedsSlides(*plan) {
		plan.Slides = buildSlides(title, instruction, plan.Analysis)
	}
}

func planNeedsDoc(plan domain.Plan) bool {
	for _, step := range plan.Steps {
		if step.Tool == "doc.create" || step.Tool == "doc.append" || step.Tool == "doc.generate" {
			return true
		}
	}
	return false
}

func planNeedsSlides(plan domain.Plan) bool {
	for _, step := range plan.Steps {
		if step.Tool == "slide.generate" || step.Tool == "slide.rehearse" {
			return true
		}
	}
	return false
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
