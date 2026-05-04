package proactive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	openai "github.com/cloudwego/eino-ext/components/model/openai"
	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"agentpilot/backend/internal/domain"
)

const judgeSystemPrompt = `你是企业 IM 群聊中的主动任务识别器。判断最近群聊片段是否构成真实、可推进的办公任务。

只输出严格 JSON，不要 Markdown，不要代码块。JSON 结构：
{
  "isTask": true,
  "title": "10-30字任务标题",
  "goal": "一句话说明任务目标",
  "taskType": "doc|ppt|report|summary|review|other",
  "themeKey": "稳定去重主题，10-24个中文或英文字符",
  "confidence": 0.0,
  "reason": "简短判断理由"
}

判断要保守：
- 明确出现整理、汇总、方案、报告、PPT、复盘、评审、交付、截止时间、资料链接等办公任务信号，才标 isTask=true。
- 闲聊、打招呼、单纯问答、技术讨论、吐槽、通知但无人要求产出，不算任务。
- 信息不足时 confidence 低于 0.55。`

const previousThemePrompt = `上一次群聊片段的 themeKey：%s
最近群聊片段：
%s

如果最近群聊片段和上一次属于同一个任务主题，请沿用该 themeKey，且不要输出isTask=true，以防发起重复任务；
否则，请生成新的符合最近群聊片段的 themeKey
`

type Config struct {
	Enabled       bool
	RuleThreshold float64
	LLMConfidence float64
	Cooldown      time.Duration
	CacheLimit    int
}

func ConfigFromEnv() Config {
	return Config{
		Enabled:       envFlag("ENABLE_PROACTIVE_DETECTION"),
		RuleThreshold: envFloat("PROACTIVE_RULE_THRESHOLD", 0.40),
		LLMConfidence: envFloat("PROACTIVE_LLM_CONFIDENCE", 0.55),
		Cooldown:      time.Duration(envInt("PROACTIVE_COOLDOWN_SECONDS", 3600)) * time.Second,
		CacheLimit:    envInt("PROACTIVE_CACHE_LIMIT", 30),
	}
}

func (c Config) normalized() Config {
	if c.RuleThreshold <= 0 {
		c.RuleThreshold = 0.40
	}
	if c.LLMConfidence <= 0 {
		c.LLMConfidence = 0.55
	}
	if c.Cooldown <= 0 {
		c.Cooldown = time.Hour
	}
	if c.CacheLimit <= 0 {
		c.CacheLimit = 30
	}
	return c
}

type Detector struct {
	config Config
	judge  Judge
}

type Judge interface {
	Judge(ctx context.Context, messages []domain.ChatMessage, previousThemeKey string) (Judgement, error)
}

type Judgement struct {
	IsTask     bool    `json:"isTask"`
	Title      string  `json:"title"`
	Goal       string  `json:"goal"`
	TaskType   string  `json:"taskType"`
	ThemeKey   string  `json:"themeKey"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
}

type RuleHit struct {
	KeywordHits       []string
	SemanticHits      []string
	HasTimeSignal     bool
	HasResourceSignal bool
	MultiSpeaker      bool
	ConsecutiveMsgs   int
	Score             float64
}

type Candidate struct {
	Ready       bool
	RuleHit     RuleHit
	Judgement   Judgement
	Title       string
	Instruction string
	ThemeKey    string
	ContextJSON string
	Reason      string
}

func NewDetector(config Config, judge Judge) *Detector {
	config = config.normalized()
	if judge == nil {
		judge = NewEinoJudgeFromEnv()
	}
	return &Detector{config: config, judge: judge}
}

func (d *Detector) Detect(ctx context.Context, messages []domain.ChatMessage) (Candidate, error) {
	return d.DetectWithPreviousThemeKey(ctx, messages, "")
}

func (d *Detector) DetectWithPreviousThemeKey(ctx context.Context, messages []domain.ChatMessage, previousThemeKey string) (Candidate, error) {
	if d == nil {
		return Candidate{}, errors.New("proactive detector is nil")
	}
	if len(messages) == 0 {
		return Candidate{}, nil
	}

	hit := DetectRules(messages)
	if hit.Score < d.config.RuleThreshold {
		return Candidate{RuleHit: hit}, nil
	}
	if d.judge == nil {
		return Candidate{RuleHit: hit}, nil
	}
	judgement, err := d.judge.Judge(ctx, messages, strings.TrimSpace(previousThemeKey))
	if err != nil {
		return Candidate{RuleHit: hit}, err
	}
	normalizeJudgement(&judgement, messages)
	if !judgement.IsTask || judgement.Confidence < d.config.LLMConfidence {
		return Candidate{RuleHit: hit, Judgement: judgement}, nil
	}

	title := strings.TrimSpace(judgement.Title)
	if title == "" {
		title = judgement.Goal
	}
	if title == "" {
		title = taskTitleFromMessages(messages)
	}
	instruction := BuildInstruction(judgement, messages)
	contextJSON, _ := json.Marshal(messages)
	return Candidate{
		Ready:       true,
		RuleHit:     hit,
		Judgement:   judgement,
		Title:       limitRunes(title, 48),
		Instruction: instruction,
		ThemeKey:    normalizeTheme(firstNonEmpty(judgement.ThemeKey, judgement.Goal, title)),
		ContextJSON: string(contextJSON),
		Reason:      judgement.Reason,
	}, nil
}

func DetectRules(messages []domain.ChatMessage) RuleHit {
	if len(messages) == 0 {
		return RuleHit{}
	}
	recent := lastMessages(messages, 5)
	wide := lastMessages(messages, 10)
	text := strings.Join(messageTexts(recent), "\n")
	lower := strings.ToLower(text)

	hit := RuleHit{}
	for _, kw := range officeKeywords {
		if strings.Contains(lower, strings.ToLower(kw)) {
			hit.KeywordHits = append(hit.KeywordHits, kw)
		}
	}
	for _, verb := range semanticVerbs {
		if strings.Contains(text, verb) || strings.Contains(lower, strings.ToLower(verb)) {
			hit.SemanticHits = append(hit.SemanticHits, verb)
		}
	}
	for _, word := range timeKeywords {
		if strings.Contains(lower, strings.ToLower(word)) {
			hit.HasTimeSignal = true
			break
		}
	}
	for _, pattern := range resourcePatterns {
		if pattern.MatchString(text) {
			hit.HasResourceSignal = true
			break
		}
	}
	senders := map[string]struct{}{}
	for _, message := range wide {
		if sender := firstNonEmpty(message.SenderOpenID, message.SenderUserID, message.SenderUnionID); sender != "" {
			senders[sender] = struct{}{}
		}
	}
	hit.MultiSpeaker = len(senders) >= 2
	hit.ConsecutiveMsgs = len(lastMessages(messages, 20))

	score := 0.0
	score += minFloat(0.4*float64(len(hit.KeywordHits)), 0.6)
	score += minFloat(0.15*float64(len(hit.SemanticHits)), 0.30)
	if hit.HasTimeSignal {
		score += 0.20
	}
	if hit.HasResourceSignal {
		score += 0.15
	}
	if hit.MultiSpeaker {
		score += 0.15
	}
	if hit.ConsecutiveMsgs >= 5 {
		score += 0.10
	}
	hit.Score = minFloat(score, 1.0)
	sort.Strings(hit.KeywordHits)
	sort.Strings(hit.SemanticHits)
	return hit
}

func BuildInstruction(judgement Judgement, messages []domain.ChatMessage) string {
	var b strings.Builder
	goal := firstNonEmpty(judgement.Goal, judgement.Title, taskTitleFromMessages(messages))
	b.WriteString("主动识别到群聊中可能需要推进的办公任务。\n")
	b.WriteString("任务目标：" + goal + "\n")
	if judgement.TaskType != "" {
		b.WriteString("任务类型：" + judgement.TaskType + "\n")
	}
	b.WriteString("\n最近群聊上下文：\n")
	for _, message := range lastMessages(messages, 15) {
		sender := firstNonEmpty(message.SenderOpenID, message.SenderUserID, "unknown")
		b.WriteString(fmt.Sprintf("- %s: %s\n", sender, strings.TrimSpace(message.Content)))
	}
	return strings.TrimSpace(b.String())
}

type EinoJudge struct {
	config       modelConfig
	newChatModel chatModelFactory
}

type chatModel interface {
	Generate(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.Message, error)
}

type chatModelFactory func(context.Context, *openai.ChatModelConfig) (chatModel, error)

type modelConfig struct {
	APIKey  string
	BaseURL string
	Model   string
	Timeout time.Duration
	Client  *http.Client
}

func NewEinoJudgeFromEnv() *EinoJudge {
	return &EinoJudge{config: modelConfigFromEnv(), newChatModel: openAIChatModelFactory}
}

func NewEinoJudge(apiKey, baseURL, model string, client *http.Client) *EinoJudge {
	return &EinoJudge{
		config: modelConfig{
			APIKey:  strings.TrimSpace(apiKey),
			BaseURL: normalizeBaseURL(baseURL),
			Model:   strings.TrimSpace(model),
			Timeout: 15 * time.Second,
			Client:  client,
		},
		newChatModel: openAIChatModelFactory,
	}
}

func (j *EinoJudge) Judge(ctx context.Context, messages []domain.ChatMessage, previousThemeKey string) (Judgement, error) {
	if j == nil || !j.config.enabled() || j.newChatModel == nil {
		return Judgement{}, errors.New("proactive llm judge is not configured")
	}
	model, err := j.newChatModel(ctx, j.chatModelConfig())
	if err != nil {
		return Judgement{}, err
	}
	resp, err := model.Generate(ctx, []*schema.Message{
		schema.SystemMessage(judgeSystemPrompt),
		schema.UserMessage(messagesForPromptWithPreviousTheme(messages, previousThemeKey)),
	})
	if err != nil {
		return Judgement{}, err
	}
	content := messageText(resp)
	if content == "" {
		return Judgement{}, errors.New("proactive llm judge returned empty content")
	}
	return ParseJudgement(content)
}

func (j *EinoJudge) chatModelConfig() *openai.ChatModelConfig {
	temperature := float32(0.1)
	maxTokens := 800
	return &openai.ChatModelConfig{
		APIKey:      j.config.APIKey,
		BaseURL:     j.config.BaseURL,
		Model:       j.config.Model,
		Timeout:     j.config.Timeout,
		HTTPClient:  j.config.Client,
		Temperature: &temperature,
		MaxTokens:   &maxTokens,
	}
}

func ParseJudgement(raw string) (Judgement, error) {
	raw = stripCodeFence(strings.TrimSpace(raw))
	var judgement Judgement
	if err := json.Unmarshal([]byte(raw), &judgement); err != nil {
		return Judgement{}, err
	}
	return judgement, nil
}

func normalizeJudgement(judgement *Judgement, messages []domain.ChatMessage) {
	if judgement == nil {
		return
	}
	judgement.Title = limitRunes(strings.TrimSpace(judgement.Title), 48)
	judgement.Goal = limitRunes(strings.TrimSpace(judgement.Goal), 160)
	judgement.TaskType = limitRunes(strings.TrimSpace(judgement.TaskType), 30)
	judgement.ThemeKey = normalizeTheme(firstNonEmpty(judgement.ThemeKey, judgement.Goal, judgement.Title, taskTitleFromMessages(messages)))
	judgement.Reason = limitRunes(strings.TrimSpace(judgement.Reason), 160)
	if judgement.Confidence < 0 {
		judgement.Confidence = 0
	}
	if judgement.Confidence > 1 {
		judgement.Confidence = 1
	}
}

func messagesForPrompt(messages []domain.ChatMessage) string {
	var b strings.Builder
	for _, message := range lastMessages(messages, 15) {
		sender := firstNonEmpty(message.SenderOpenID, message.SenderUserID, "unknown")
		b.WriteString(fmt.Sprintf("[%s] %s\n", sender, strings.TrimSpace(message.Content)))
	}
	return strings.TrimSpace(b.String())
}

func messagesForPromptWithPreviousTheme(messages []domain.ChatMessage, previousThemeKey string) string {
	body := messagesForPrompt(messages)
	previousThemeKey = normalizeTheme(previousThemeKey)
	if previousThemeKey == "" || previousThemeKey == "task" {
		return body
	}
	return fmt.Sprintf(previousThemePrompt, previousThemeKey, body)
}

func messageText(msg *schema.Message) string {
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

func modelConfigFromEnv() modelConfig {
	baseURL := strings.TrimSpace(os.Getenv("DEEPSEEK_BASE_URL"))
	if baseURL == "" {
		baseURL = "https://api.deepseek.com"
	}
	model := strings.TrimSpace(os.Getenv("DEEPSEEK_MODEL"))
	if model == "" {
		model = "deepseek-chat"
	}
	return modelConfig{
		APIKey:  strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY")),
		BaseURL: normalizeBaseURL(baseURL),
		Model:   model,
		Timeout: 15 * time.Second,
	}
}

func (c modelConfig) enabled() bool {
	return c.APIKey != "" && c.BaseURL != "" && c.Model != ""
}

func openAIChatModelFactory(ctx context.Context, config *openai.ChatModelConfig) (chatModel, error) {
	return openai.NewChatModel(ctx, config)
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

func stripCodeFence(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "```") {
		return raw
	}
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	return strings.TrimSpace(raw)
}

func taskTitleFromMessages(messages []domain.ChatMessage) string {
	if len(messages) == 0 {
		return "主动识别任务"
	}
	last := strings.TrimSpace(messages[len(messages)-1].Content)
	if last == "" {
		return "主动识别任务"
	}
	return limitRunes(last, 48)
}

func normalizeTheme(theme string) string {
	theme = strings.ToLower(strings.TrimSpace(theme))
	var b strings.Builder
	for _, r := range theme {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Han, r) {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if out == "" {
		return "task"
	}
	return limitRunes(out, 24)
}

func limitRunes(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func lastMessages(messages []domain.ChatMessage, limit int) []domain.ChatMessage {
	if limit <= 0 || len(messages) <= limit {
		return messages
	}
	return messages[len(messages)-limit:]
}

func messageTexts(messages []domain.ChatMessage) []string {
	out := make([]string, 0, len(messages))
	for _, message := range messages {
		out = append(out, message.Content)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func envFlag(key string) bool {
	return strings.EqualFold(os.Getenv(key), "true") || os.Getenv(key) == "1"
}

func envFloat(key string, fallback float64) float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	var value float64
	if _, err := fmt.Sscanf(raw, "%f", &value); err != nil {
		return fallback
	}
	return value
}

func envInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	var value int
	if _, err := fmt.Sscanf(raw, "%d", &value); err != nil {
		return fallback
	}
	return value
}

var officeKeywords = []string{
	"整理一下", "汇总一下", "梳理一下", "拉齐一下", "沉淀一下", "形成材料",
	"做个方案", "出个文档", "起草", "计划书", "需求文档", "PRD",
	"生成 PPT", "做 PPT", "做PPT", "做演示", "下周汇报", "给老板看", "写汇报",
	"评审", "做汇报", "准备演示", "演讲稿", "复盘", "周报", "月报", "报告",
	"ppt", "deck", "presentation", "summary", "report", "proposal", "wrap up", "follow up",
}

var semanticVerbs = []string{
	"汇报", "对外", "对上", "评审", "演示", "归档", "形成", "整理",
	"结构化", "对齐", "结论", "材料", "成果", "交付", "总结", "复盘",
}

var timeKeywords = []string{
	"今天", "明天", "后天", "下周", "下下周", "本周", "本月", "下月",
	"周一", "周二", "周三", "周四", "周五", "deadline", "ddl", "截止",
}

var resourcePatterns = []*regexp.Regexp{
	regexp.MustCompile(`https?://[^\s]+`),
	regexp.MustCompile(`飞书(文档|文件|wiki|画板|表格)`),
	regexp.MustCompile(`\.(docx?|pptx?|xlsx?|pdf|md)(\s|$)`),
}
