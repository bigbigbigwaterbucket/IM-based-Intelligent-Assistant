package larkbot

import (
	"context"
	"log"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

type Bot struct {
	config   Config
	wsClient *larkws.Client
	handler  *Handler
}

func New(config Config, launcher TaskLauncher) (*Bot, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if !config.Enabled {
		return nil, nil
	}

	client := lark.NewClient(config.AppID, config.AppSecret)
	messenger := NewSDKMessenger(client)
	handler := NewHandler(launcher, messenger, config.PublicBaseURL)
	if err := handler.validate(); err != nil {
		return nil, err
	}

	eventHandler := dispatcher.NewEventDispatcher(config.VerificationToken, config.EventEncryptKey).
		OnP2MessageReceiveV1(handler.HandleMessage)
	wsClient := larkws.NewClient(
		config.AppID,
		config.AppSecret,
		larkws.WithEventHandler(eventHandler),
		larkws.WithLogLevel(larkcore.LogLevelInfo),
	)

	return &Bot{
		config:   config,
		wsClient: wsClient,
		handler:  handler,
	}, nil
}

func (b *Bot) Start(ctx context.Context) {
	if b == nil || b.wsClient == nil {
		return
	}

	go func() {
		log.Printf("feishu bot websocket starting")
		if err := b.wsClient.Start(ctx); err != nil {
			log.Printf("feishu bot websocket stopped: %v", err)
		}
	}()
}
