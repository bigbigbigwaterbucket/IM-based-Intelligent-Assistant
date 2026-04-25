package tools

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type Config struct {
	LarkCLIPath     string
	EnableLarkTools bool
}

type Runner struct {
	config Config
}

type Result struct {
	Success        bool
	StepName       string
	PayloadSummary string
	Retryable      bool
	ErrorMessage   string
	ArtifactURL    string
}

func NewRunner(config Config) *Runner {
	return &Runner{config: config}
}

func (r *Runner) CreateDoc(ctx context.Context, title, instruction string) Result {
	if !r.config.EnableLarkTools {
		slug := sanitize(title)
		return Result{
			Success:        true,
			StepName:       "create_doc",
			PayloadSummary: "使用占位模式生成文档链接",
			ArtifactURL:    fmt.Sprintf("https://placeholder.local/docs/%s?content=%s", slug, sanitize(instruction)),
		}
	}

	command := exec.CommandContext(
		ctx,
		r.config.LarkCLIPath,
		"docs",
		"+create",
		"--api-version",
		"v2",
		"--as",
		"user",
		"--title",
		title,
		"--content",
		fmt.Sprintf("<title>%s</title><p>%s</p>", escapeXML(title), escapeXML(instruction)),
	)

	output, err := command.CombinedOutput()
	if err != nil {
		return Result{
			StepName:       "create_doc",
			ErrorMessage:   err.Error(),
			Retryable:      true,
			PayloadSummary: string(output),
		}
	}

	return Result{
		Success:        true,
		StepName:       "create_doc",
		PayloadSummary: string(output),
		ArtifactURL:    extractFirstURL(string(output)),
	}
}

func (r *Runner) CreateSlides(ctx context.Context, title, summary string) Result {
	if !r.config.EnableLarkTools {
		slug := sanitize(title)
		return Result{
			Success:        true,
			StepName:       "create_slides",
			PayloadSummary: "使用占位模式生成演示稿链接",
			ArtifactURL:    fmt.Sprintf("https://placeholder.local/slides/%s?summary=%s", slug, sanitize(summary)),
		}
	}

	slideXML := fmt.Sprintf(
		`["<slide xmlns=\"http://www.larkoffice.com/sml/2.0\"><data><shape type=\"text\" topLeftX=\"80\" topLeftY=\"80\" width=\"800\" height=\"120\"><content textType=\"title\"><p>%s</p></content></shape><shape type=\"text\" topLeftX=\"80\" topLeftY=\"220\" width=\"800\" height=\"260\"><content textType=\"body\"><p>%s</p></content></shape></data></slide>"]`,
		escapeXML(title),
		escapeXML(summary),
	)

	command := exec.CommandContext(
		ctx,
		r.config.LarkCLIPath,
		"slides",
		"+create",
		"--as",
		"user",
		"--title",
		title,
		"--slides",
		slideXML,
	)

	output, err := command.CombinedOutput()
	if err != nil {
		return Result{
			StepName:       "create_slides",
			ErrorMessage:   err.Error(),
			Retryable:      true,
			PayloadSummary: string(output),
		}
	}

	return Result{
		Success:        true,
		StepName:       "create_slides",
		PayloadSummary: string(output),
		ArtifactURL:    extractFirstURL(string(output)),
	}
}

func sanitize(value string) string {
	replacer := strings.NewReplacer(" ", "-", "?", "", "&", "", "\n", "-")
	return strings.ToLower(replacer.Replace(value))
}

func escapeXML(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;")
	return replacer.Replace(value)
}

func extractFirstURL(output string) string {
	for _, field := range strings.Fields(output) {
		if strings.HasPrefix(field, "http://") || strings.HasPrefix(field, "https://") {
			return strings.Trim(field, "\"")
		}
	}
	return ""
}
