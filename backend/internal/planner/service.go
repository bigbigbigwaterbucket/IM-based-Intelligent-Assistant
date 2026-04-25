package planner

import (
	"context"
	"fmt"

	"agentpilot/backend/internal/domain"
)

type Service struct{}

func NewService() *Service {
	return &Service{}
}

func (s *Service) BuildPlan(_ context.Context, title, instruction string) (domain.Plan, error) {
	return domain.Plan{
		Summary:  fmt.Sprintf("围绕“%s”生成一份结构化方案文档，并产出适合汇报的演示稿。", title),
		DocTitle: title + " - 方案文档",
		Steps: []string{
			"解析需求并生成执行计划",
			"创建方案文档",
			"生成演示稿大纲与页面内容",
			"创建演示稿并整理交付链接",
		},
	}, nil
}
