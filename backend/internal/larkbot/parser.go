package larkbot

import (
	"encoding/json"
	"regexp"
	"strings"
)

const AssistantCommand = "assistant_run"

var atTagPattern = regexp.MustCompile(`(?is)<at\b[^>]*>.*?</at>`)

type Command struct {
	Name   string
	Intent string
	Help   bool
}

func ParseTextContent(messageType, content string) (Command, bool) {
	if messageType != "text" {
		return Command{}, false
	}

	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return Command{}, false
	}
	return ParseCommandText(payload.Text)
}

func ParseCommandText(text string) (Command, bool) {
	text = normalizeCommandText(text)
	if text == "" {
		return Command{}, false
	}

	lower := strings.ToLower(text)
	for _, prefix := range []string{"/assistant", "assistant"} {
		if lower == prefix {
			return Command{Name: AssistantCommand, Help: true}, true
		}
		if strings.HasPrefix(lower, prefix+" ") || strings.HasPrefix(lower, prefix+"\n") || strings.HasPrefix(lower, prefix+"\t") {
			intent := strings.TrimSpace(text[len(prefix):])
			return Command{Name: AssistantCommand, Intent: intent, Help: intent == ""}, true
		}
	}

	return Command{}, false
}

func normalizeCommandText(text string) string {
	text = atTagPattern.ReplaceAllString(text, " ")
	return strings.TrimSpace(strings.Join(strings.Fields(text), " "))
}
