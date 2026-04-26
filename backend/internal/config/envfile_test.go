package config

import "testing"

func TestParseLine(t *testing.T) {
	t.Parallel()

	key, value, ok := parseLine(`export FEISHU_APP_ID="cli_xxx"`)
	if !ok {
		t.Fatal("expected parsed line")
	}
	if key != "FEISHU_APP_ID" {
		t.Fatalf("unexpected key: %s", key)
	}
	if value != "cli_xxx" {
		t.Fatalf("unexpected value: %s", value)
	}
}

func TestParseLineIgnoresComments(t *testing.T) {
	t.Parallel()

	if _, _, ok := parseLine("# comment"); ok {
		t.Fatal("did not expect comment to parse")
	}
}
