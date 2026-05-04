package larkbot

import (
	"errors"
	"os"
	"strings"
)

type Config struct {
	Enabled           bool
	AppID             string
	AppSecret         string
	VerificationToken string
	EventEncryptKey   string
	PublicBaseURL     string
	ProactiveEnabled  bool
}

func ConfigFromEnv() Config {
	return Config{
		Enabled:           strings.EqualFold(os.Getenv("ENABLE_FEISHU_BOT"), "true"),
		AppID:             os.Getenv("FEISHU_APP_ID"),
		AppSecret:         os.Getenv("FEISHU_APP_SECRET"),
		VerificationToken: os.Getenv("FEISHU_VERIFICATION_TOKEN"),
		EventEncryptKey:   os.Getenv("FEISHU_EVENT_ENCRYPT_KEY"),
		PublicBaseURL:     strings.TrimRight(os.Getenv("PUBLIC_BASE_URL"), "/"),
		ProactiveEnabled:  strings.EqualFold(os.Getenv("ENABLE_PROACTIVE_DETECTION"), "true") || os.Getenv("ENABLE_PROACTIVE_DETECTION") == "1",
	}
}

func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.AppID == "" {
		return errors.New("FEISHU_APP_ID is required when ENABLE_FEISHU_BOT=true")
	}
	if c.AppSecret == "" {
		return errors.New("FEISHU_APP_SECRET is required when ENABLE_FEISHU_BOT=true")
	}
	return nil
}
