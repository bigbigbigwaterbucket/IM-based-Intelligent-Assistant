package store

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"agentpilot/backend/internal/domain"
)

func TestSQLiteChatMessageCacheKeepsRecentMessages(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", "file:keep-chat-cache?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	store, err := NewSQLiteStore(db)
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}

	for i := 0; i < 4; i++ {
		err := store.AppendChatMessage(context.Background(), domain.ChatMessage{
			MessageID:    string(rune('a' + i)),
			ChatID:       "oc_test",
			SenderOpenID: "ou_test",
			Content:      "message",
			CreatedAt:    time.Now().Add(time.Duration(i) * time.Second),
		}, 3)
		if err != nil {
			t.Fatalf("append chat message: %v", err)
		}
	}

	messages, err := store.ListRecentChatMessages(context.Background(), "oc_test", 10)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(messages))
	}
	if messages[0].MessageID != "b" || messages[2].MessageID != "d" {
		t.Fatalf("unexpected message order: %#v", messages)
	}
}

func TestSQLiteConsumeChatMessagesDeletesThroughSourceMessage(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", "file:consume-chat-cache?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	store, err := NewSQLiteStore(db)
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}

	base := time.Now()
	for i, id := range []string{"before", "source", "after"} {
		err := store.AppendChatMessage(context.Background(), domain.ChatMessage{
			MessageID:    id,
			ChatID:       "oc_test",
			SenderOpenID: "ou_test",
			Content:      id,
			CreatedAt:    base.Add(time.Duration(i) * time.Second),
		}, 10)
		if err != nil {
			t.Fatalf("append chat message: %v", err)
		}
	}

	if err := store.ConsumeChatMessages(context.Background(), "oc_test", "source"); err != nil {
		t.Fatalf("consume messages: %v", err)
	}

	messages, err := store.ListRecentChatMessages(context.Background(), "oc_test", 10)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	if len(messages) != 1 || messages[0].MessageID != "after" {
		t.Fatalf("expected only messages after source to remain, got %#v", messages)
	}
}

func TestSQLiteProactiveCandidateCooldown(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", "file:proactive-candidate-cooldown?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	store, err := NewSQLiteStore(db)
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}

	now := time.Now()
	candidate := domain.ProactiveCandidate{
		CandidateID:     "cand-1",
		ChatID:          "oc_test",
		SourceMessageID: "om_test",
		Title:           "项目复盘",
		Instruction:     "整理项目复盘",
		ThemeKey:        "项目复盘",
		Status:          domain.CandidatePending,
		ExpiresAt:       now.Add(time.Hour),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if _, err := store.CreateProactiveCandidate(context.Background(), candidate); err != nil {
		t.Fatalf("create candidate: %v", err)
	}

	ok, err := store.HasRecentProactiveCandidate(context.Background(), "oc_test", "项目复盘", now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("check cooldown: %v", err)
	}
	if !ok {
		t.Fatal("expected cooldown hit")
	}

	updated, err := store.UpdateProactiveCandidateStatus(context.Background(), "cand-1", domain.CandidateIgnored)
	if err != nil {
		t.Fatalf("update candidate: %v", err)
	}
	if updated.Status != domain.CandidateIgnored {
		t.Fatalf("unexpected status: %s", updated.Status)
	}

	latestTheme, err := store.LatestProactiveThemeKey(context.Background(), "oc_test")
	if err != nil {
		t.Fatalf("latest theme: %v", err)
	}
	if latestTheme != "项目复盘" {
		t.Fatalf("unexpected latest theme: %q", latestTheme)
	}
}
