package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"agentpilot/backend/internal/domain"
)

type TaskRepository interface {
	Create(ctx context.Context, task domain.Task) (domain.Task, error)
	Update(ctx context.Context, task domain.Task) (domain.Task, error)
	Get(ctx context.Context, taskID string) (domain.Task, error)
	List(ctx context.Context) ([]domain.Task, error)
}

type HistoryRepository interface {
	UpsertSession(ctx context.Context, session domain.Session) error
	GetSession(ctx context.Context, sessionID string) (domain.Session, error)
	AppendMessage(ctx context.Context, message domain.ConversationMessage) error
	AppendToolInvocation(ctx context.Context, invocation domain.ToolInvocation) error
	ListMessages(ctx context.Context, sessionID string, limit int) ([]domain.ConversationMessage, error)
}

type ActiveTaskRepository interface {
	FindActiveTaskBySession(ctx context.Context, sessionID string) (domain.Task, error)
	ListIdleWaitingTasks(ctx context.Context, cutoff time.Time) ([]domain.Task, error)
}

type ChatMessageRepository interface {
	AppendChatMessage(ctx context.Context, message domain.ChatMessage, keepLimit int) error
	ListRecentChatMessages(ctx context.Context, chatID string, limit int) ([]domain.ChatMessage, error)
	ConsumeChatMessages(ctx context.Context, chatID, throughMessageID string) error
}

type ProactiveCandidateRepository interface {
	CreateProactiveCandidate(ctx context.Context, candidate domain.ProactiveCandidate) (domain.ProactiveCandidate, error)
	GetProactiveCandidate(ctx context.Context, candidateID string) (domain.ProactiveCandidate, error)
	UpdateProactiveCandidateStatus(ctx context.Context, candidateID string, status domain.ProactiveCandidateStatus) (domain.ProactiveCandidate, error)
	HasRecentProactiveCandidate(ctx context.Context, chatID, themeKey string, since time.Time) (bool, error)
	LatestProactiveThemeKey(ctx context.Context, chatID string) (string, error)
}

type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(db *sql.DB) (*SQLiteStore, error) {
	store := &SQLiteStore{db: db}
	if err := store.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return store, nil
}

func (s *SQLiteStore) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS tasks (
			task_id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			user_instruction TEXT NOT NULL,
			source TEXT NOT NULL,
			chat_id TEXT NOT NULL DEFAULT '',
			thread_id TEXT NOT NULL DEFAULT '',
			message_id TEXT NOT NULL DEFAULT '',
			initiator_user_id TEXT NOT NULL DEFAULT '',
			initiator_open_id TEXT NOT NULL DEFAULT '',
			initiator_union_id TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			current_step TEXT NOT NULL,
			progress_text TEXT NOT NULL,
			doc_url TEXT NOT NULL,
			slides_url TEXT NOT NULL,
			doc_id TEXT NOT NULL DEFAULT '',
			doc_artifact_path TEXT NOT NULL DEFAULT '',
			slides_artifact_path TEXT NOT NULL DEFAULT '',
			summary TEXT NOT NULL,
			requires_action INTEGER NOT NULL,
			error_message TEXT NOT NULL,
			version INTEGER NOT NULL,
			last_actor TEXT NOT NULL,
			last_interaction_at TEXT NOT NULL DEFAULT '',
			idle_prompted_at TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			steps_json TEXT NOT NULL
		);
	`)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS sessions (
			session_id TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			chat_id TEXT NOT NULL DEFAULT '',
			thread_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS conversation_messages (
			message_id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			metadata TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_conversation_messages_session_created
			ON conversation_messages(session_id, created_at);
		CREATE TABLE IF NOT EXISTS tool_invocations (
			invocation_id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			task_id TEXT NOT NULL,
			step_id TEXT NOT NULL,
			tool_name TEXT NOT NULL,
			arguments_json TEXT NOT NULL,
			result_summary TEXT NOT NULL,
			result_json TEXT NOT NULL,
			error_message TEXT NOT NULL DEFAULT '',
			artifact_url TEXT NOT NULL DEFAULT '',
			artifact_path TEXT NOT NULL DEFAULT '',
			started_at TEXT NOT NULL,
			completed_at TEXT NOT NULL,
			duration_millis INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_tool_invocations_session
			ON tool_invocations(session_id, started_at);
		CREATE TABLE IF NOT EXISTS chat_message_cache (
			message_id TEXT PRIMARY KEY,
			chat_id TEXT NOT NULL,
			thread_id TEXT NOT NULL DEFAULT '',
			sender_user_id TEXT NOT NULL DEFAULT '',
			sender_open_id TEXT NOT NULL DEFAULT '',
			sender_union_id TEXT NOT NULL DEFAULT '',
			content TEXT NOT NULL,
			chat_type TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_chat_message_cache_chat_created
			ON chat_message_cache(chat_id, created_at);
		CREATE TABLE IF NOT EXISTS proactive_candidates (
			candidate_id TEXT PRIMARY KEY,
			chat_id TEXT NOT NULL,
			thread_id TEXT NOT NULL DEFAULT '',
			source_message_id TEXT NOT NULL DEFAULT '',
			title TEXT NOT NULL,
			instruction TEXT NOT NULL,
			context_json TEXT NOT NULL DEFAULT '',
			theme_key TEXT NOT NULL,
			status TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_proactive_candidates_chat_theme
			ON proactive_candidates(chat_id, theme_key, created_at);
	`); err != nil {
		return err
	}
	for _, column := range []struct {
		name string
		ddl  string
	}{
		{name: "chat_id", ddl: "ALTER TABLE tasks ADD COLUMN chat_id TEXT NOT NULL DEFAULT ''"},
		{name: "thread_id", ddl: "ALTER TABLE tasks ADD COLUMN thread_id TEXT NOT NULL DEFAULT ''"},
		{name: "message_id", ddl: "ALTER TABLE tasks ADD COLUMN message_id TEXT NOT NULL DEFAULT ''"},
		{name: "initiator_user_id", ddl: "ALTER TABLE tasks ADD COLUMN initiator_user_id TEXT NOT NULL DEFAULT ''"},
		{name: "initiator_open_id", ddl: "ALTER TABLE tasks ADD COLUMN initiator_open_id TEXT NOT NULL DEFAULT ''"},
		{name: "initiator_union_id", ddl: "ALTER TABLE tasks ADD COLUMN initiator_union_id TEXT NOT NULL DEFAULT ''"},
		{name: "doc_id", ddl: "ALTER TABLE tasks ADD COLUMN doc_id TEXT NOT NULL DEFAULT ''"},
		{name: "doc_artifact_path", ddl: "ALTER TABLE tasks ADD COLUMN doc_artifact_path TEXT NOT NULL DEFAULT ''"},
		{name: "slides_artifact_path", ddl: "ALTER TABLE tasks ADD COLUMN slides_artifact_path TEXT NOT NULL DEFAULT ''"},
		{name: "last_interaction_at", ddl: "ALTER TABLE tasks ADD COLUMN last_interaction_at TEXT NOT NULL DEFAULT ''"},
		{name: "idle_prompted_at", ddl: "ALTER TABLE tasks ADD COLUMN idle_prompted_at TEXT NOT NULL DEFAULT ''"},
	} {
		ok, err := s.hasColumn("tasks", column.name)
		if err != nil {
			return err
		}
		if !ok {
			if _, err := s.db.Exec(column.ddl); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *SQLiteStore) Create(ctx context.Context, task domain.Task) (domain.Task, error) {
	stepsJSON, err := json.Marshal(task.Steps)
	if err != nil {
		return domain.Task{}, err
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO tasks (
			task_id, title, user_instruction, source, status, current_step, progress_text,
			chat_id, thread_id, message_id, initiator_user_id, initiator_open_id, initiator_union_id,
			doc_url, slides_url, doc_id, doc_artifact_path, slides_artifact_path,
			summary, requires_action, error_message, version,
			last_actor, last_interaction_at, idle_prompted_at, created_at, updated_at, steps_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		task.TaskID, task.Title, task.UserInstruction, task.Source, task.Status, task.CurrentStep, task.ProgressText,
		task.ChatID, task.ThreadID, task.MessageID, task.InitiatorUserID, task.InitiatorOpenID, task.InitiatorUnionID,
		task.DocURL, task.SlidesURL, task.DocID, task.DocArtifactPath, task.SlidesArtifactPath,
		task.Summary, boolToInt(task.RequiresAction), task.ErrorMessage, task.Version,
		task.LastActor, formatTimePtr(task.LastInteractionAt), formatTimePtr(task.IdlePromptedAt),
		task.CreatedAt.Format(time.RFC3339Nano), task.UpdatedAt.Format(time.RFC3339Nano), string(stepsJSON),
	)
	if err != nil {
		return domain.Task{}, err
	}
	return task, nil
}

func (s *SQLiteStore) Update(ctx context.Context, task domain.Task) (domain.Task, error) {
	stepsJSON, err := json.Marshal(task.Steps)
	if err != nil {
		return domain.Task{}, err
	}

	result, err := s.db.ExecContext(ctx, `
		UPDATE tasks
		SET title = ?, user_instruction = ?, source = ?, status = ?, current_step = ?, progress_text = ?,
			chat_id = ?, thread_id = ?, message_id = ?, initiator_user_id = ?, initiator_open_id = ?, initiator_union_id = ?,
			doc_url = ?, slides_url = ?, doc_id = ?, doc_artifact_path = ?, slides_artifact_path = ?,
			summary = ?, requires_action = ?, error_message = ?,
			version = ?, last_actor = ?, last_interaction_at = ?, idle_prompted_at = ?,
			created_at = ?, updated_at = ?, steps_json = ?
		WHERE task_id = ?
	`,
		task.Title, task.UserInstruction, task.Source, task.Status, task.CurrentStep, task.ProgressText,
		task.ChatID, task.ThreadID, task.MessageID, task.InitiatorUserID, task.InitiatorOpenID, task.InitiatorUnionID,
		task.DocURL, task.SlidesURL, task.DocID, task.DocArtifactPath, task.SlidesArtifactPath,
		task.Summary, boolToInt(task.RequiresAction), task.ErrorMessage,
		task.Version, task.LastActor, formatTimePtr(task.LastInteractionAt), formatTimePtr(task.IdlePromptedAt),
		task.CreatedAt.Format(time.RFC3339Nano), task.UpdatedAt.Format(time.RFC3339Nano),
		string(stepsJSON), task.TaskID,
	)
	if err != nil {
		return domain.Task{}, err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return domain.Task{}, errors.New("task not found")
	}
	return task, nil
}

func (s *SQLiteStore) Get(ctx context.Context, taskID string) (domain.Task, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT task_id, title, user_instruction, source, status, current_step, progress_text,
			chat_id, thread_id, message_id, initiator_user_id, initiator_open_id, initiator_union_id,
			doc_url, slides_url, doc_id, doc_artifact_path, slides_artifact_path,
			summary, requires_action, error_message, version,
			last_actor, last_interaction_at, idle_prompted_at, created_at, updated_at, steps_json
		FROM tasks
		WHERE task_id = ?
	`, taskID)
	return scanTask(row)
}

func (s *SQLiteStore) List(ctx context.Context) ([]domain.Task, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT task_id, title, user_instruction, source, status, current_step, progress_text,
			chat_id, thread_id, message_id, initiator_user_id, initiator_open_id, initiator_union_id,
			doc_url, slides_url, doc_id, doc_artifact_path, slides_artifact_path,
			summary, requires_action, error_message, version,
			last_actor, last_interaction_at, idle_prompted_at, created_at, updated_at, steps_json
		FROM tasks
		ORDER BY updated_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []domain.Task
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func (s *SQLiteStore) UpsertSession(ctx context.Context, session domain.Session) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (session_id, task_id, chat_id, thread_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			task_id = excluded.task_id,
			chat_id = excluded.chat_id,
			thread_id = excluded.thread_id,
			updated_at = excluded.updated_at
	`,
		session.SessionID, session.TaskID, session.ChatID, session.ThreadID,
		session.CreatedAt.Format(time.RFC3339Nano), session.UpdatedAt.Format(time.RFC3339Nano),
	)
	return err
}

func (s *SQLiteStore) GetSession(ctx context.Context, sessionID string) (domain.Session, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT session_id, task_id, chat_id, thread_id, created_at, updated_at
		FROM sessions
		WHERE session_id = ?
	`, sessionID)

	var session domain.Session
	var createdAt string
	var updatedAt string
	if err := row.Scan(&session.SessionID, &session.TaskID, &session.ChatID, &session.ThreadID, &createdAt, &updatedAt); err != nil {
		return domain.Session{}, err
	}
	session.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	session.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return session, nil
}

func (s *SQLiteStore) FindActiveTaskBySession(ctx context.Context, sessionID string) (domain.Task, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT t.task_id, t.title, t.user_instruction, t.source, t.status, t.current_step, t.progress_text,
			t.chat_id, t.thread_id, t.message_id, t.initiator_user_id, t.initiator_open_id, t.initiator_union_id,
			t.doc_url, t.slides_url, t.doc_id, t.doc_artifact_path, t.slides_artifact_path,
			t.summary, t.requires_action, t.error_message, t.version,
			t.last_actor, t.last_interaction_at, t.idle_prompted_at, t.created_at, t.updated_at, t.steps_json
		FROM sessions s
		JOIN tasks t ON t.task_id = s.task_id
		WHERE s.session_id = ?
			AND t.status IN (?, ?, ?, ?)
		ORDER BY t.updated_at DESC
		LIMIT 1
	`, sessionID, domain.StatusCreated, domain.StatusPlanning, domain.StatusExecuting, domain.StatusWaitingAction)
	return scanTask(row)
}

func (s *SQLiteStore) ListIdleWaitingTasks(ctx context.Context, cutoff time.Time) ([]domain.Task, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT task_id, title, user_instruction, source, status, current_step, progress_text,
			chat_id, thread_id, message_id, initiator_user_id, initiator_open_id, initiator_union_id,
			doc_url, slides_url, doc_id, doc_artifact_path, slides_artifact_path,
			summary, requires_action, error_message, version,
			last_actor, last_interaction_at, idle_prompted_at, created_at, updated_at, steps_json
		FROM tasks
		WHERE status = ?
			AND chat_id <> ''
			AND idle_prompted_at = ''
			AND COALESCE(NULLIF(last_interaction_at, ''), updated_at) <= ?
		ORDER BY updated_at ASC
	`, domain.StatusWaitingAction, cutoff.Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tasks []domain.Task
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func (s *SQLiteStore) AppendMessage(ctx context.Context, message domain.ConversationMessage) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO conversation_messages (message_id, session_id, role, content, metadata, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`,
		message.MessageID, message.SessionID, message.Role, message.Content, message.Metadata,
		message.CreatedAt.Format(time.RFC3339Nano),
	)
	return err
}

func (s *SQLiteStore) AppendToolInvocation(ctx context.Context, invocation domain.ToolInvocation) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO tool_invocations (
			invocation_id, session_id, task_id, step_id, tool_name, arguments_json,
			result_summary, result_json, error_message, artifact_url, artifact_path,
			started_at, completed_at, duration_millis
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		invocation.InvocationID, invocation.SessionID, invocation.TaskID, invocation.StepID, invocation.ToolName,
		invocation.ArgumentsJSON, invocation.ResultSummary, invocation.ResultJSON, invocation.ErrorMessage,
		invocation.ArtifactURL, invocation.ArtifactPath, invocation.StartedAt.Format(time.RFC3339Nano),
		invocation.CompletedAt.Format(time.RFC3339Nano), invocation.DurationMillis,
	)
	return err
}

func (s *SQLiteStore) ListMessages(ctx context.Context, sessionID string, limit int) ([]domain.ConversationMessage, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT message_id, session_id, role, content, metadata, created_at
		FROM conversation_messages
		WHERE session_id = ?
		ORDER BY created_at DESC
		LIMIT ?
	`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	reversed := make([]domain.ConversationMessage, 0, limit)
	for rows.Next() {
		var message domain.ConversationMessage
		var createdAt string
		if err := rows.Scan(&message.MessageID, &message.SessionID, &message.Role, &message.Content, &message.Metadata, &createdAt); err != nil {
			return nil, err
		}
		message.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		reversed = append(reversed, message)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	messages := make([]domain.ConversationMessage, len(reversed))
	for i := range reversed {
		messages[len(reversed)-1-i] = reversed[i]
	}
	return messages, nil
}

func (s *SQLiteStore) AppendChatMessage(ctx context.Context, message domain.ChatMessage, keepLimit int) error {
	if keepLimit <= 0 {
		keepLimit = 30
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO chat_message_cache (
			message_id, chat_id, thread_id, sender_user_id, sender_open_id, sender_union_id,
			content, chat_type, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(message_id) DO UPDATE SET
			chat_id = excluded.chat_id,
			thread_id = excluded.thread_id,
			sender_user_id = excluded.sender_user_id,
			sender_open_id = excluded.sender_open_id,
			sender_union_id = excluded.sender_union_id,
			content = excluded.content,
			chat_type = excluded.chat_type,
			created_at = excluded.created_at
	`,
		message.MessageID, message.ChatID, message.ThreadID, message.SenderUserID, message.SenderOpenID, message.SenderUnionID,
		message.Content, message.ChatType, message.CreatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		DELETE FROM chat_message_cache
		WHERE chat_id = ?
			AND message_id NOT IN (
				SELECT message_id
				FROM chat_message_cache
				WHERE chat_id = ?
				ORDER BY created_at DESC
				LIMIT ?
			)
	`, message.ChatID, message.ChatID, keepLimit)
	return err
}

func (s *SQLiteStore) ListRecentChatMessages(ctx context.Context, chatID string, limit int) ([]domain.ChatMessage, error) {
	if limit <= 0 {
		limit = 30
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT message_id, chat_id, thread_id, sender_user_id, sender_open_id, sender_union_id,
			content, chat_type, created_at
		FROM chat_message_cache
		WHERE chat_id = ?
		ORDER BY created_at DESC
		LIMIT ?
	`, chatID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	reversed := make([]domain.ChatMessage, 0, limit)
	for rows.Next() {
		var message domain.ChatMessage
		var createdAt string
		if err := rows.Scan(
			&message.MessageID, &message.ChatID, &message.ThreadID, &message.SenderUserID, &message.SenderOpenID, &message.SenderUnionID,
			&message.Content, &message.ChatType, &createdAt,
		); err != nil {
			return nil, err
		}
		message.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		reversed = append(reversed, message)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	messages := make([]domain.ChatMessage, len(reversed))
	for i := range reversed {
		messages[len(reversed)-1-i] = reversed[i]
	}
	return messages, nil
}

func (s *SQLiteStore) ConsumeChatMessages(ctx context.Context, chatID, throughMessageID string) error {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return nil
	}
	throughMessageID = strings.TrimSpace(throughMessageID)
	if throughMessageID == "" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM chat_message_cache WHERE chat_id = ?`, chatID)
		return err
	}

	var cutoff string
	err := s.db.QueryRowContext(ctx, `
		SELECT created_at
		FROM chat_message_cache
		WHERE chat_id = ? AND message_id = ?
	`, chatID, throughMessageID).Scan(&cutoff)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = s.db.ExecContext(ctx, `DELETE FROM chat_message_cache WHERE chat_id = ?`, chatID)
		return err
	}
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		DELETE FROM chat_message_cache
		WHERE chat_id = ? AND created_at <= ?
	`, chatID, cutoff)
	return err
}

func (s *SQLiteStore) CreateProactiveCandidate(ctx context.Context, candidate domain.ProactiveCandidate) (domain.ProactiveCandidate, error) {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO proactive_candidates (
			candidate_id, chat_id, thread_id, source_message_id, title, instruction,
			context_json, theme_key, status, expires_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		candidate.CandidateID, candidate.ChatID, candidate.ThreadID, candidate.SourceMessageID, candidate.Title, candidate.Instruction,
		candidate.ContextJSON, candidate.ThemeKey, candidate.Status,
		candidate.ExpiresAt.Format(time.RFC3339Nano), candidate.CreatedAt.Format(time.RFC3339Nano), candidate.UpdatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return domain.ProactiveCandidate{}, err
	}
	return candidate, nil
}

func (s *SQLiteStore) GetProactiveCandidate(ctx context.Context, candidateID string) (domain.ProactiveCandidate, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT candidate_id, chat_id, thread_id, source_message_id, title, instruction,
			context_json, theme_key, status, expires_at, created_at, updated_at
		FROM proactive_candidates
		WHERE candidate_id = ?
	`, candidateID)
	return scanProactiveCandidate(row)
}

func (s *SQLiteStore) UpdateProactiveCandidateStatus(ctx context.Context, candidateID string, status domain.ProactiveCandidateStatus) (domain.ProactiveCandidate, error) {
	now := time.Now()
	result, err := s.db.ExecContext(ctx, `
		UPDATE proactive_candidates
		SET status = ?, updated_at = ?
		WHERE candidate_id = ?
	`, status, now.Format(time.RFC3339Nano), candidateID)
	if err != nil {
		return domain.ProactiveCandidate{}, err
	}
	count, _ := result.RowsAffected()
	if count == 0 {
		return domain.ProactiveCandidate{}, errors.New("proactive candidate not found")
	}
	return s.GetProactiveCandidate(ctx, candidateID)
}

func (s *SQLiteStore) HasRecentProactiveCandidate(ctx context.Context, chatID, themeKey string, since time.Time) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM proactive_candidates
		WHERE chat_id = ?
			AND theme_key = ?
			AND created_at >= ?
			AND status IN (?, ?, ?)
	`, chatID, themeKey, since.Format(time.RFC3339Nano), domain.CandidatePending, domain.CandidateConfirmed, domain.CandidateIgnored).Scan(&count)
	return count > 0, err
}

func (s *SQLiteStore) LatestProactiveThemeKey(ctx context.Context, chatID string) (string, error) {
	var themeKey string
	err := s.db.QueryRowContext(ctx, `
		SELECT theme_key
		FROM proactive_candidates
		WHERE chat_id = ?
		ORDER BY created_at DESC
		LIMIT 1
	`, chatID).Scan(&themeKey)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return themeKey, err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanTask(row scanner) (domain.Task, error) {
	var (
		task              domain.Task
		requiresFlag      int
		createdAt         string
		updatedAt         string
		lastInteractionAt string
		idlePromptedAt    string
		stepsJSON         string
	)
	err := row.Scan(
		&task.TaskID, &task.Title, &task.UserInstruction, &task.Source, &task.Status, &task.CurrentStep, &task.ProgressText,
		&task.ChatID, &task.ThreadID, &task.MessageID, &task.InitiatorUserID, &task.InitiatorOpenID, &task.InitiatorUnionID,
		&task.DocURL, &task.SlidesURL, &task.DocID, &task.DocArtifactPath, &task.SlidesArtifactPath,
		&task.Summary, &requiresFlag, &task.ErrorMessage, &task.Version,
		&task.LastActor, &lastInteractionAt, &idlePromptedAt, &createdAt, &updatedAt, &stepsJSON,
	)
	if err != nil {
		return domain.Task{}, err
	}

	task.RequiresAction = requiresFlag == 1
	task.LastInteractionAt = parseTimePtr(lastInteractionAt)
	task.IdlePromptedAt = parseTimePtr(idlePromptedAt)
	task.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	task.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	_ = json.Unmarshal([]byte(stepsJSON), &task.Steps)
	return task, nil
}

func scanProactiveCandidate(row scanner) (domain.ProactiveCandidate, error) {
	var candidate domain.ProactiveCandidate
	var expiresAt string
	var createdAt string
	var updatedAt string
	err := row.Scan(
		&candidate.CandidateID, &candidate.ChatID, &candidate.ThreadID, &candidate.SourceMessageID,
		&candidate.Title, &candidate.Instruction, &candidate.ContextJSON, &candidate.ThemeKey, &candidate.Status,
		&expiresAt, &createdAt, &updatedAt,
	)
	if err != nil {
		return domain.ProactiveCandidate{}, err
	}
	candidate.ExpiresAt, _ = time.Parse(time.RFC3339Nano, expiresAt)
	candidate.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	candidate.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return candidate, nil
}

func (s *SQLiteStore) hasColumn(table, column string) (bool, error) {
	rows, err := s.db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid        int
			name       string
			columnType string
			notNull    int
			defaultVal any
			pk         int
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultVal, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func formatTimePtr(value *time.Time) string {
	if value == nil || value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339Nano)
}

func parseTimePtr(value string) *time.Time {
	if value == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil
	}
	return &parsed
}
