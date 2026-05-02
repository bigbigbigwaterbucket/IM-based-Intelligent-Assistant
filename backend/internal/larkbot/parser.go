package larkbot

import (
	"encoding/json"
	"regexp"
	"strings"
)

const AssistantCommand = "assistant_run"

var atTagPattern = regexp.MustCompile(`(?is)<at\b[^>]*>.*?</at>`)
var leadingMentionPattern = regexp.MustCompile(`(?i)^(?:@\S+\s+)+`)

type Command struct {
	Name   string
	Intent string
	Help   bool
	New    bool
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
			cmd := Command{Name: AssistantCommand, Intent: intent, Help: intent == ""}
			if strings.EqualFold(intent, "new") || strings.HasPrefix(strings.ToLower(intent), "new ") {
				cmd.New = true
				cmd.Intent = strings.TrimSpace(intent[len("new"):])
				cmd.Help = false
			}
			return cmd, true
		}
	}

	return Command{}, false
}

func normalizeCommandText(text string) string {
	text = atTagPattern.ReplaceAllString(text, " ")
	text = leadingMentionPattern.ReplaceAllString(strings.TrimSpace(text), "")
	return strings.TrimSpace(strings.Join(strings.Fields(text), " "))
}
