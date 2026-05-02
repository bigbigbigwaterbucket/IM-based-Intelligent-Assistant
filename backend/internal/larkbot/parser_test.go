package larkbot

import "testing"

func TestParseTextContentAssistant(t *testing.T) {
	t.Parallel()

	cmd, ok := ParseTextContent("text", `{"text":"/assistant 把群聊消息总结成方案+ppt"}`)
	if !ok {
		t.Fatal("expected command")
	}
	if cmd.Name != AssistantCommand {
		t.Fatalf("unexpected command name: %s", cmd.Name)
	}
	if cmd.Intent != "把群聊消息总结成方案+ppt" {
		t.Fatalf("unexpected intent: %q", cmd.Intent)
	}
	if cmd.Help {
		t.Fatal("did not expect help command")
	}
}

func TestParseTextContentAssistantAliasAndMention(t *testing.T) {
	t.Parallel()

	cmd, ok := ParseTextContent("text", `{"text":"<at user_id=\"ou_x\">bot</at> assistant 生成周报"}`)
	if !ok {
		t.Fatal("expected command")
	}
	if cmd.Intent != "生成周报" {
		t.Fatalf("unexpected intent: %q", cmd.Intent)
	}
}

func TestParseTextContentAssistantMentionUserPrefix(t *testing.T) {
	t.Parallel()

	cmd, ok := ParseTextContent("text", `{"text":"@_user_1 /assistant 修改文档标题"}`)
	if !ok {
		t.Fatal("expected command")
	}
	if cmd.Intent != "修改文档标题" {
		t.Fatalf("unexpected intent: %q", cmd.Intent)
	}
}

func TestParseTextContentAssistantNewCommand(t *testing.T) {
	t.Parallel()

	cmd, ok := ParseTextContent("text", `{"text":"/assistant new 重新生成方案"}`)
	if !ok {
		t.Fatal("expected command")
	}
	if !cmd.New {
		t.Fatal("expected new command")
	}
	if cmd.Intent != "重新生成方案" {
		t.Fatalf("unexpected intent: %q", cmd.Intent)
	}
}

func TestParseTextContentHelp(t *testing.T) {
	t.Parallel()

	cmd, ok := ParseTextContent("text", `{"text":"/assistant"}`)
	if !ok {
		t.Fatal("expected command")
	}
	if !cmd.Help {
		t.Fatal("expected help command")
	}
}

func TestParseTextContentIgnoresPilot(t *testing.T) {
	t.Parallel()

	if _, ok := ParseTextContent("text", `{"text":"/pilot 旧入口"}`); ok {
		t.Fatal("did not expect /pilot to be parsed")
	}
}

func TestParseTextContentIgnoresNonText(t *testing.T) {
	t.Parallel()

	if _, ok := ParseTextContent("image", `{"text":"/assistant 生成周报"}`); ok {
		t.Fatal("did not expect non-text message to be parsed")
	}
}
