package planner

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"agentpilot/backend/internal/domain"
)

type Builder interface {
	BuildPlan(ctx context.Context, title, instruction string) (domain.Plan, error)
}

type llmBuilder interface {
	Builder
	Enabled() bool
}

type Service struct {
	llm llmBuilder
}

func NewService() *Service {
	return &Service{llm: NewLLMPlannerFromEnv()}
}

func NewServiceWithPlanner(llm llmBuilder) *Service {
	return &Service{llm: llm}
}

func NewServiceWithLLM(llm llmBuilder) *Service {
	return NewServiceWithPlanner(llm)
}

func (s *Service) BuildPlan(ctx context.Context, title, instruction string) (domain.Plan, error) {
	instruction = strings.TrimSpace(instruction)
	if instruction == "" {
		return domain.Plan{}, fmt.Errorf("instruction is required")
	}

	if s.llm != nil && s.llm.Enabled() {
		plan, err := s.llm.BuildPlan(ctx, title, instruction)
		if err == nil && validPlan(plan) {
			plan.PlannerSource = "llm"
			return plan, nil
		}
		if err == nil {
			err = fmt.Errorf("llm planner returned incomplete plan")
		}
		if err != nil {
			plan, fallbackErr := buildHeuristicPlan(title, instruction)
			if fallbackErr != nil {
				return domain.Plan{}, fallbackErr
			}
			plan.PlannerSource = "heuristic_fallback"
			plan.PlannerError = err.Error()
			return plan, nil
		}
	}

	return buildHeuristicPlan(title, instruction)
}

func buildHeuristicPlan(title, instruction string) (domain.Plan, error) {
	analysis := analyzeIntent(instruction)
	docTitle := title + " - 方案文档"
	slideTitle := title + " - 汇报演示稿"

	sections := buildDocumentSections(instruction, analysis)
	slides := buildSlides(title, instruction, analysis)
	steps := buildSteps(analysis)

	return domain.Plan{
		Summary:          summarize(title, analysis),
		PlannerSource:    "heuristic",
		Analysis:         analysis,
		Steps:            steps,
		DocTitle:         docTitle,
		SlideTitle:       slideTitle,
		DocumentSections: sections,
		Slides:           slides,
	}, nil
}

func validPlan(plan domain.Plan) bool {
	needsDoc := planNeedsDoc(plan)
	needsSlides := planNeedsSlides(plan)
	if hasDeliverable(plan.Analysis, "方案文档") && !needsDoc {
		return false
	}
	if hasDeliverable(plan.Analysis, "演示稿") && !needsSlides {
		return false
	}
	return plan.Summary != "" &&
		plan.Analysis.Objective != "" &&
		len(plan.Steps) > 0 &&
		(!needsDoc || (plan.DocTitle != "" && len(plan.DocumentSections) > 0)) &&
		(!needsSlides || (plan.SlideTitle != "" && len(plan.Slides) > 0))
}

func analyzeIntent(instruction string) domain.IntentAnalysis {
	lower := strings.ToLower(instruction)
	if isGreeting(instruction) {
		return domain.IntentAnalysis{
			Objective:      "识别到用户只是打招呼或测试在线状态，当前不需要启动文档或演示稿生成。",
			Audience:       "用户本人",
			Deliverables:   []string{},
			ContextNeeded:  false,
			Risks:          []string{"如果直接生成文档或演示稿，会偏离用户真实意图。"},
			ClarifyingHint: "可以直接回复问候，并提示用户输入 /assistant <具体需求> 来启动任务。",
		}
	}

	deliverables := make([]string, 0, 2)
	if matchAny(instruction, `文档|方案|需求|纪要|总结|报告`) {
		deliverables = append(deliverables, "方案文档")
	}
	if matchAny(instruction, `ppt|演示|演讲|汇报|slide|幻灯`) {
		deliverables = append(deliverables, "演示稿")
	}
	if len(deliverables) == 0 {
		deliverables = append(deliverables, "方案文档", "演示稿")
	}

	audience := "项目相关干系人"
	switch {
	case strings.Contains(instruction, "老板") || strings.Contains(instruction, "管理层") || strings.Contains(instruction, "领导"):
		audience = "管理层"
	case strings.Contains(instruction, "客户"):
		audience = "客户或外部评审方"
	case strings.Contains(instruction, "团队") || strings.Contains(instruction, "同事"):
		audience = "内部协作团队"
	}

	risks := []string{
		"输入信息可能不完整，需要在交付前补充关键事实和数据。",
		"生成内容应由负责人复核后再对外发布。",
	}
	if strings.Contains(lower, "群") || strings.Contains(instruction, "聊天") || strings.Contains(instruction, "讨论") {
		risks = append(risks, "群聊上下文可能存在噪声，需要聚焦结论、分歧和待办。")
	}

	return domain.IntentAnalysis{
		Objective:      objectiveFromInstruction(instruction),
		Audience:       audience,
		Deliverables:   deliverables,
		ContextNeeded:  matchAny(instruction, `群聊|聊天|讨论|上下文|最近|昨天|本周`),
		Risks:          risks,
		ClarifyingHint: clarifyingHint(instruction),
	}
}

func buildSteps(analysis domain.IntentAnalysis) []domain.PlanStep {
	steps := []domain.PlanStep{
		{ID: "s1", Tool: "intent.analyze", Description: "分析用户意图、受众、交付物和风险"},
		{ID: "s2", Tool: "planner.build", Description: "拆解文档与演示稿生成计划", DependsOn: []string{"s1"}},
	}
	if len(analysis.Deliverables) == 0 {
		return append(steps, domain.PlanStep{
			ID:          "s3",
			Tool:        "sync.broadcast",
			Description: "回复问候并等待用户输入具体任务",
			DependsOn:   []string{"s2"},
		})
	}

	if analysis.ContextNeeded {
		steps = append(steps, domain.PlanStep{
			ID:          "s3",
			Tool:        "im.fetch_thread",
			Description: "整理群聊或对话上下文，提取背景、结论和待办",
			DependsOn:   []string{"s2"},
		})
	}

	last := steps[len(steps)-1].ID
	artifactDeps := make([]string, 0, 2)
	nextID := len(steps) + 1
	if hasDeliverable(analysis, "方案文档") {
		docCreateID := fmt.Sprintf("s%d", nextID)
		nextID++
		steps = append(steps, domain.PlanStep{ID: docCreateID, Tool: "doc.create", Description: "创建结构化方案文档", DependsOn: []string{last}})
		docAppendID := fmt.Sprintf("s%d", nextID)
		nextID++
		steps = append(steps, domain.PlanStep{ID: docAppendID, Tool: "doc.append", Description: "写入方案文档内容", DependsOn: []string{docCreateID}})
		artifactDeps = append(artifactDeps, docAppendID)
	}
	if hasDeliverable(analysis, "演示稿") {
		slideID := fmt.Sprintf("s%d", nextID)
		nextID++
		deps := []string{last}
		if len(artifactDeps) > 0 {
			deps = []string{artifactDeps[len(artifactDeps)-1]}
		}
		steps = append(steps, domain.PlanStep{ID: slideID, Tool: "slide.generate", Description: "生成 Slidev 演示稿 Markdown", DependsOn: deps})
		rehearseID := fmt.Sprintf("s%d", nextID)
		nextID++
		steps = append(steps, domain.PlanStep{ID: rehearseID, Tool: "slide.rehearse", Description: "生成演讲稿", DependsOn: []string{slideID}})
		artifactDeps = append(artifactDeps, rehearseID)
	}
	if len(artifactDeps) > 0 {
		steps = append(steps, domain.PlanStep{ID: fmt.Sprintf("s%d", nextID), Tool: "archive.bundle", Description: "汇总所有产物并生成 manifest", DependsOn: artifactDeps})
	}
	return steps
}

func buildDocumentSections(instruction string, analysis domain.IntentAnalysis) []domain.DocumentSection {
	deliverables := strings.Join(analysis.Deliverables, "、")
	return []domain.DocumentSection{
		{
			Heading: "背景与目标",
			Bullets: []string{
				analysis.Objective,
				"目标读者：" + analysis.Audience,
				"原始需求：" + instruction,
				"本次交付应产出可直接审阅的" + deliverables + "，并保留待确认事项。",
			},
		},
		{
			Heading: "输入理解与边界",
			Bullets: []string{
				"当前输入只包含任务指令，尚未包含真实群聊原文或附件。",
				"系统会先生成可评审初稿；涉及事实、数据、责任人和时间点的内容需要后续补齐。",
				"如果接入 IM 历史消息权限，下一版应把群聊内容归纳为：背景、共识、分歧、决策、待办。",
			},
		},
		{
			Heading: "执行方案",
			Bullets: []string{
				"第一步：完成意图分析，明确这次任务的受众、交付物、上下文依赖和风险。",
				"第二步：生成方案文档，按背景、目标、核心方案、执行计划、风险与待确认事项组织。",
				"第三步：基于文档生成演示稿，保证 PPT 不是重新发散，而是从文档中抽取汇报主线。",
				"第四步：生成 manifest，把文档、演示稿、演讲稿和规划信息统一归档。",
			},
		},
		{
			Heading: "建议的文档结构",
			Bullets: []string{
				"现状背景：说明讨论来源、业务问题和为什么现在需要整理。",
				"目标与范围：明确要解决什么、不解决什么，以及面向谁交付。",
				"方案主体：沉淀关键结论、执行路径、分工和里程碑。",
				"风险与待确认：列出事实缺口、依赖条件和需要人工确认的问题。",
			},
		},
		{
			Heading: "建议的演示稿结构",
			Bullets: []string{
				"第 1 页：标题与一句话结论。",
				"第 2 页：背景问题和整理目标。",
				"第 3 页：核心结论或推荐方案。",
				"第 4 页：执行路径、里程碑和协作方式。",
				"第 5 页：风险、待确认事项和下一步。",
			},
		},
		{
			Heading: "待确认事项",
			Bullets: []string{
				analysis.ClarifyingHint,
				"请补充真实群聊消息范围，例如最近 50 条、某个话题串、或指定时间段。",
				"请确认交付截止时间、汇报对象和是否需要引用具体数据。",
				"请确认产物是否需要发布到飞书云文档/演示稿，还是先用本地 Markdown 预览。",
			},
		},
		{
			Heading: "风险与建议",
			Bullets: analysis.Risks,
		},
	}
}

func buildSlides(title, instruction string, analysis domain.IntentAnalysis) []domain.Slide {
	return []domain.Slide{
		{
			Title: title,
			Bullets: []string{
				"需求：" + instruction,
				"交付物：" + strings.Join(analysis.Deliverables, "、"),
			},
			SpeakerNote: "开场说明本次任务的背景、目标和最终交付物。",
		},
		{
			Title: "任务理解",
			Bullets: []string{
				analysis.Objective,
				"受众：" + analysis.Audience,
			},
			SpeakerNote: "解释我们如何理解用户需求，以及为什么要用文档和演示稿双产物承接。",
		},
		{
			Title: "当前输入边界",
			Bullets: []string{
				"目前只有任务指令，没有真实群聊原文",
				"可先生成汇报初稿，但事实内容需要二次补齐",
				"接入 IM 历史后可自动提取共识、分歧、决策和待办",
			},
			SpeakerNote: "这一页要诚实说明产物边界：现在生成的是可编辑初稿，不是已经读取真实群聊后的最终纪要。",
		},
		{
			Title: "方案结构",
			Bullets: []string{
				"背景与目标",
				"输入理解与边界",
				"执行方案",
				"待确认事项与风险",
			},
			SpeakerNote: "用这一页让听众快速建立文档目录和后续汇报路线。",
		},
		{
			Title: "执行路径",
			Bullets: []string{
				"意图分析",
				"任务规划",
				"文档生成",
				"PPT 与演讲稿生成",
				"产物归档与回传",
			},
			SpeakerNote: "强调这是自动化链路，不是单次文本生成。",
		},
		{
			Title: "下一步接入",
			Bullets: []string{
				"接入飞书 IM 历史消息读取，补齐真实讨论内容",
				"用 Go OAPI 创建 Docx 和 Slides，替代本地 Markdown 预览",
				"在完成通知中返回飞书云文档链接",
			},
			SpeakerNote: "把下一步工程重点说清楚：真实群聊上下文和飞书云文档写入是产物质量继续提升的关键。",
		},
		{
			Title:       "风险与下一步",
			Bullets:     analysis.Risks,
			SpeakerNote: "收束到仍需人工确认的事项，并给出下一步行动建议。",
		},
	}
}

func summarize(title string, analysis domain.IntentAnalysis) string {
	return fmt.Sprintf("围绕“%s”完成意图分析，面向%s生成%s。", title, analysis.Audience, strings.Join(analysis.Deliverables, "和"))
}

func objectiveFromInstruction(instruction string) string {
	if matchAny(instruction, `总结|纪要|复盘`) {
		return "将分散信息整理为结构化结论，便于后续决策和汇报。"
	}
	if matchAny(instruction, `方案|规划|计划`) {
		return "形成可评审、可执行的方案框架，并明确下一步行动。"
	}
	if matchAny(instruction, `ppt|演示|汇报`) {
		return "把核心信息转化为适合口头汇报的演示材料。"
	}
	return "把自然语言需求转化为可交付的文档和演示稿。"
}

func clarifyingHint(instruction string) string {
	if len([]rune(instruction)) < 16 {
		return "当前需求较短，建议补充背景、受众、交付截止时间和期望格式。"
	}
	return "当前需求可先生成初稿，但发布前仍建议确认受众、数据来源和截止时间。"
}

func matchAny(value, pattern string) bool {
	return regexp.MustCompile(pattern).MatchString(value)
}

func isGreeting(instruction string) bool {
	normalized := strings.TrimSpace(strings.ToLower(instruction))
	normalized = strings.Trim(normalized, "。.!！?？~～ ")
	switch normalized {
	case "你好", "您好", "hello", "hi", "hey", "在吗", "测试", "test":
		return true
	default:
		return false
	}
}

func hasDeliverable(analysis domain.IntentAnalysis, value string) bool {
	for _, deliverable := range analysis.Deliverables {
		if strings.Contains(deliverable, value) {
			return true
		}
	}
	return false
}
