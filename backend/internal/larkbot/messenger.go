package larkbot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type TextMessenger interface {
	ReplyText(ctx context.Context, messageID, text string) error
	SendText(ctx context.Context, receiveID, idType, text string) error
}

type SDKMessenger struct {
	client *lark.Client
}

func NewSDKMessenger(client *lark.Client) *SDKMessenger {
	return &SDKMessenger{client: client}
}

func (m *SDKMessenger) ReplyText(ctx context.Context, messageID, text string) error {
	if strings.TrimSpace(messageID) == "" {
		return errors.New("message id is required")
	}
	content, err := textContent(text)
	if err != nil {
		return err
	}

	body := larkim.NewReplyMessageReqBodyBuilder().
		MsgType("text").
		Content(content).
		Uuid(uuid.NewString()).
		Build()
	req := larkim.NewReplyMessageReqBuilder().
		MessageId(messageID).
		Body(body).
		Build()

	resp, err := m.client.Im.V1.Message.Reply(ctx, req)
	if err != nil {
		return err
	}
	if resp == nil {
		return errors.New("reply text failed: empty response")
	}
	if !resp.Success() {
		return fmt.Errorf("reply text failed: code=%d msg=%s", resp.Code, resp.Msg)
	}
	return nil
}

func (m *SDKMessenger) SendText(ctx context.Context, receiveID, idType, text string) error {
	if strings.TrimSpace(receiveID) == "" {
		return errors.New("receive id is required")
	}
	if strings.TrimSpace(idType) == "" {
		idType = "open_id"
	}
	content, err := textContent(text)
	if err != nil {
		return err
	}

	body := larkim.NewCreateMessageReqBodyBuilder().
		ReceiveId(receiveID).
		MsgType("text").
		Content(content).
		Uuid(uuid.NewString()).
		Build()
	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType(idType).
		Body(body).
		Build()

	resp, err := m.client.Im.V1.Message.Create(ctx, req)
	if err != nil {
		return err
	}
	if resp == nil {
		return errors.New("send text failed: empty response")
	}
	if !resp.Success() {
		return fmt.Errorf("send text failed: code=%d msg=%s", resp.Code, resp.Msg)
	}
	return nil
}

func textContent(text string) (string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("text is required")
	}
	content, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return "", err
	}
	return string(content), nil
}
