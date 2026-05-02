package larkbot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type TextMessenger interface {
	ReplyText(ctx context.Context, messageID, text string) error
	ReplyFile(ctx context.Context, messageID, filePath string) error
	SendText(ctx context.Context, receiveID, idType, text string) error
	SendInteractive(ctx context.Context, receiveID, idType, content string) error
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

func (m *SDKMessenger) ReplyFile(ctx context.Context, messageID, filePath string) error {
	if strings.TrimSpace(messageID) == "" {
		return errors.New("message id is required")
	}
	filePath = strings.TrimSpace(filePath)
	if filePath == "" {
		return errors.New("file path is required")
	}
	info, err := os.Stat(filePath)
	if err != nil {
		return err
	}
	if info.IsDir() || info.Size() == 0 {
		return fmt.Errorf("invalid file path: %s", filePath)
	}

	uploadBody, err := larkim.NewCreateFilePathReqBodyBuilder().
		FileType(fileTypeForPath(filePath)).
		FileName(filepath.Base(filePath)).
		FilePath(filePath).
		Build()
	if err != nil {
		return err
	}
	uploadReq := larkim.NewCreateFileReqBuilder().
		Body(uploadBody).
		Build()
	uploadResp, err := m.client.Im.V1.File.Create(ctx, uploadReq)
	if err != nil {
		return err
	}
	if uploadResp == nil {
		return errors.New("upload file failed: empty response")
	}
	if !uploadResp.Success() {
		return fmt.Errorf("upload file failed: code=%d msg=%s", uploadResp.Code, uploadResp.Msg)
	}
	if uploadResp.Data == nil || uploadResp.Data.FileKey == nil || strings.TrimSpace(*uploadResp.Data.FileKey) == "" {
		return errors.New("upload file failed: missing file_key")
	}

	content, err := (&larkim.MessageFile{FileKey: *uploadResp.Data.FileKey}).String()
	if err != nil {
		return err
	}
	body := larkim.NewReplyMessageReqBodyBuilder().
		MsgType(larkim.MsgTypeFile).
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
		return errors.New("reply file failed: empty response")
	}
	if !resp.Success() {
		return fmt.Errorf("reply file failed: code=%d msg=%s", resp.Code, resp.Msg)
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

func (m *SDKMessenger) SendInteractive(ctx context.Context, receiveID, idType, content string) error {
	if strings.TrimSpace(receiveID) == "" {
		return errors.New("receive id is required")
	}
	if strings.TrimSpace(idType) == "" {
		idType = "open_id"
	}
	if strings.TrimSpace(content) == "" {
		return errors.New("interactive content is required")
	}

	body := larkim.NewCreateMessageReqBodyBuilder().
		ReceiveId(receiveID).
		MsgType("interactive").
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
		return errors.New("send interactive failed: empty response")
	}
	if !resp.Success() {
		return fmt.Errorf("send interactive failed: code=%d msg=%s", resp.Code, resp.Msg)
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

func fileTypeForPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ppt", ".pptx":
		return larkim.FileTypePpt
	case ".doc", ".docx", ".md", ".txt":
		return larkim.FileTypeDoc
	case ".xls", ".xlsx", ".csv":
		return larkim.FileTypeXls
	case ".pdf":
		return larkim.FileTypePdf
	case ".mp4":
		return larkim.FileTypeMp4
	default:
		return larkim.FileTypeStream
	}
}
