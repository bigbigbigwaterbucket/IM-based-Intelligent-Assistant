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
    {"id":"s3","tool":"doc.create","description":"创建文档产物","dependsOn":["s2"]},
    {"id":"s4","tool":"doc.append","description":"由执行 agent 写入完整文档内容","dependsOn":["s3"]},
    {"id":"s5","tool":"slide.generate","description":"由执行 agent 生成完整演示稿内容","dependsOn":["s4"]},
    {"id":"s6","tool":"archive.bundle","description":"汇总产物","dependsOn":["s3","s5"]}
  ]
}

约束：
1. 如果用户只是打招呼、闲聊、感谢、测试在线状态，steps 只能包含 intent.analyze、planner.build、sync.broadcast，不能包含 doc.create、doc.append、slide.generate、archive.bundle；deliverables 必须为空数组。
2. 只有用户明确要求文档、方案、报告、总结时，才加入 doc.create 和 doc.append。
3. 只有用户明确要求 PPT、演示稿、汇报材料、幻灯片时，才加入 slide.generate。
4. 只有存在文档、演示稿或其他产物时，才加入 archive.bundle。
5. 除非用户明确要求不参考群聊消息自由发挥，否则contextNeeded=true，并在 steps 中加入 im.fetch_thread。
6. 规划器可以输出 docTitle，但不要输出 documentSections 或任何文档正文片段，文档正文由后续执行 agent 调用工具时生成。
7. 规划器可以输出 slideTitle，但不要输出 slides 或任何演示稿页面片段，演示稿内容由后续执行 agent 调用工具时生成。
8. 不要虚构具体日期、金额、姓名；缺失信息放到 clarifyingHint 或 risks 里。`

const revisionPlannerSystemPrompt = `你是 IM-based Intelligent Assistant 的更新任务规划器。用户正在对一个已经生成过的任务产物提出修改要求。

你只能使用以下工具名称来规划步骤：
- intent.analyze：分析更新意图
- im.fetch_thread：读取并整理当前群聊上下文
- doc.update：更新已有文档
- slide.regenerate：更新已有 PPTX 演示稿
- sync.broadcast：记录无需产物变更的请求

不要使用 doc.create、doc.append、doc.generate、slide.generate、slide.rehearse、archive.bundle 或其他未列出的工具。

只输出严格 JSON，不要输出 Markdown，不要包裹代码块。JSON 结构如下：
{
  "summary": "一句话总结更新规划结果",
  "analysis": {
    "objective": "更新目标",
    "audience": "目标受众",
    "deliverables": ["document", "slides"],
    "contextNeeded": false,
    "risks": ["风险1"],
    "clarifyingHint": "需要用户确认的关键事项"
  },
  "steps": [
    {"id":"r1","tool":"intent.analyze","description":"分析更新意图","dependsOn":[]},
    {"id":"r2","tool":"im.fetch_thread","description":"重新读取群聊最新上下文","dependsOn":[]},
    {"id":"r3","tool":"doc.update","description":"更新已有文档","args":{"revision":"用户更新要求"},"dependsOn":["r1"]},
    {"id":"r4","tool":"slide.regenerate","description":"更新已有 PPTX","args":{"revision":"用户更新要求"},"dependsOn":["r2"]}
  ]
}

约束：
1. 如果用户明确只要求修改文档，不要加入 slide.regenerate；如果明确只要求修改 PPT/幻灯片，不要加入 doc.update。
2. 如果用户要求“同步修改”“都改”“全部更新”，应同时加入 doc.update 和 slide.regenerate。
3. doc.update 和 slide.regenerate 的 args.revision 必须保留用户最新更新要求。
4. 更新规划器可以输出 docTitle，但不要输出 documentSections 或任何文档正文片段，实际文档更新由后续执行 agent 读取已有文档并调用工具完成。
5. 更新规划器可以输出 slideTitle，但不要输出 slides 或任何演示稿页面片段，实际演示稿更新由后续执行 agent 完成。
6. 不要虚构具体事实；缺失信息放到 clarifyingHint 或 risks。`

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
	println("llm plan msg: ", content)
	plan, err := parseLLMPlan(content)
	if err != nil {
		return domain.Plan{}, err
	}
	normalizeLLMPlan(&plan, title, instruction)
	return plan, nil
}

func (p *EinoPlanner) BuildRevisionPlan(ctx context.Context, task domain.Task, instruction string) (domain.Plan, error) {
	if !p.Enabled() {
		return domain.Plan{}, errors.New("llm planner is not configured")
	}

	model, err := p.newChatModel(ctx, p.chatModelConfig())
	if err != nil {
		return domain.Plan{}, err
	}

	resp, err := model.Generate(ctx, revisionPlannerMessages(task, instruction))
	if err != nil {
		return domain.Plan{}, err
	}

	content := plannerResponseText(resp)
	if content == "" {
		return domain.Plan{}, errors.New("llm revision planner returned empty content")
	}
	println("llm revision msg: ", content)
	plan, err := parseLLMPlan(content)
	if err != nil {
		return domain.Plan{}, err
	}
	normalizeLLMRevisionPlan(&plan, task, instruction)
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

func revisionPlannerMessages(task domain.Task, instruction string) []*schema.Message {
	return []*schema.Message{
		schema.SystemMessage(revisionPlannerSystemPrompt),
		schema.UserMessage(fmt.Sprintf("任务ID：%s\n任务标题：%s\n原始/累计需求：%s\n最新更新要求：%s\nexistingDoc=%t\nexistingSlides=%t\n已有文档URL：%s\n已有演示稿URL：%s\n已有文档ID：%s",
			task.TaskID,
			task.Title,
			task.UserInstruction,
			instruction,
			task.DocURL != "" || task.DocID != "",
			task.SlidesURL != "",
			task.DocURL,
			task.SlidesURL,
			task.DocID,
		)),
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
	plan.DocumentSections = nil
	plan.Slides = nil
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
}

func normalizeLLMRevisionPlan(plan *domain.Plan, task domain.Task, instruction string) {
	plan.DocumentSections = nil
	plan.Slides = nil
	if plan.DocTitle == "" {
		plan.DocTitle = task.Title
	}
	if plan.SlideTitle == "" {
		plan.SlideTitle = task.Title
	}
	if plan.Summary == "" {
		plan.Summary = "按用户要求执行更新任务"
	}
	if plan.Analysis.Objective == "" {
		plan.Analysis.Objective = "Revise existing task artifacts based on user feedback."
	}
	if plan.Analysis.Audience == "" {
		plan.Analysis.Audience = "Existing task reviewers"
	}
	if len(plan.Analysis.Deliverables) == 0 {
		plan.Analysis.Deliverables = revisionDeliverables(task)
	}
	if len(plan.Steps) == 0 {
		fallback := BuildHeuristicRevisionPlan(task, instruction)
		plan.Steps = fallback.Steps
	}
	for idx := range plan.Steps {
		step := &plan.Steps[idx]
		if step.ID == "" {
			step.ID = fmt.Sprintf("r%d", idx+1)
		}
		if step.Args == nil && (step.Tool == "doc.update" || step.Tool == "slide.regenerate") {
			step.Args = map[string]any{"revision": instruction}
		}
	}
}

func planNeedsDoc(plan domain.Plan) bool {
	for _, step := range plan.Steps {
		if step.Tool == "doc.create" || step.Tool == "doc.append" || step.Tool == "doc.generate" || step.Tool == "doc.update" {
			return true
		}
	}
	return false
}

func planNeedsSlides(plan domain.Plan) bool {
	for _, step := range plan.Steps {
		if step.Tool == "slide.generate" || step.Tool == "slide.regenerate" {
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
