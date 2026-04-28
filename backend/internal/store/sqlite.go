package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
	AppendMessage(ctx context.Context, message domain.ConversationMessage) error
	AppendToolInvocation(ctx context.Context, invocation domain.ToolInvocation) error
	ListMessages(ctx context.Context, sessionID string, limit int) ([]domain.ConversationMessage, error)
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
			status TEXT NOT NULL,
			current_step TEXT NOT NULL,
			progress_text TEXT NOT NULL,
			doc_url TEXT NOT NULL,
			slides_url TEXT NOT NULL,
			summary TEXT NOT NULL,
			requires_action INTEGER NOT NULL,
			error_message TEXT NOT NULL,
			version INTEGER NOT NULL,
			last_actor TEXT NOT NULL,
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
			chat_id, thread_id, message_id,
			doc_url, slides_url, summary, requires_action, error_message, version,
			last_actor, created_at, updated_at, steps_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		task.TaskID, task.Title, task.UserInstruction, task.Source, task.Status, task.CurrentStep, task.ProgressText,
		task.ChatID, task.ThreadID, task.MessageID,
		task.DocURL, task.SlidesURL, task.Summary, boolToInt(task.RequiresAction), task.ErrorMessage, task.Version,
		task.LastActor, task.CreatedAt.Format(time.RFC3339Nano), task.UpdatedAt.Format(time.RFC3339Nano), string(stepsJSON),
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
			chat_id = ?, thread_id = ?, message_id = ?,
			doc_url = ?, slides_url = ?, summary = ?, requires_action = ?, error_message = ?,
			version = ?, last_actor = ?, created_at = ?, updated_at = ?, steps_json = ?
		WHERE task_id = ?
	`,
		task.Title, task.UserInstruction, task.Source, task.Status, task.CurrentStep, task.ProgressText,
		task.ChatID, task.ThreadID, task.MessageID,
		task.DocURL, task.SlidesURL, task.Summary, boolToInt(task.RequiresAction), task.ErrorMessage,
		task.Version, task.LastActor, task.CreatedAt.Format(time.RFC3339Nano), task.UpdatedAt.Format(time.RFC3339Nano),
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
			chat_id, thread_id, message_id,
			doc_url, slides_url, summary, requires_action, error_message, version,
			last_actor, created_at, updated_at, steps_json
		FROM tasks
		WHERE task_id = ?
	`, taskID)
	return scanTask(row)
}

func (s *SQLiteStore) List(ctx context.Context) ([]domain.Task, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT task_id, title, user_instruction, source, status, current_step, progress_text,
			chat_id, thread_id, message_id,
			doc_url, slides_url, summary, requires_action, error_message, version,
			last_actor, created_at, updated_at, steps_json
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

type scanner interface {
	Scan(dest ...any) error
}

func scanTask(row scanner) (domain.Task, error) {
	var (
		task         domain.Task
		requiresFlag int
		createdAt    string
		updatedAt    string
		stepsJSON    string
	)
	err := row.Scan(
		&task.TaskID, &task.Title, &task.UserInstruction, &task.Source, &task.Status, &task.CurrentStep, &task.ProgressText,
		&task.ChatID, &task.ThreadID, &task.MessageID,
		&task.DocURL, &task.SlidesURL, &task.Summary, &requiresFlag, &task.ErrorMessage, &task.Version,
		&task.LastActor, &createdAt, &updatedAt, &stepsJSON,
	)
	if err != nil {
		return domain.Task{}, err
	}

	task.RequiresAction = requiresFlag == 1
	task.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	task.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	_ = json.Unmarshal([]byte(stepsJSON), &task.Steps)
	return task, nil
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
