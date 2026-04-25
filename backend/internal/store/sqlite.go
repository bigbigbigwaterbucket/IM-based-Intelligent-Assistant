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
	return err
}

func (s *SQLiteStore) Create(ctx context.Context, task domain.Task) (domain.Task, error) {
	stepsJSON, err := json.Marshal(task.Steps)
	if err != nil {
		return domain.Task{}, err
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO tasks (
			task_id, title, user_instruction, source, status, current_step, progress_text,
			doc_url, slides_url, summary, requires_action, error_message, version,
			last_actor, created_at, updated_at, steps_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		task.TaskID, task.Title, task.UserInstruction, task.Source, task.Status, task.CurrentStep, task.ProgressText,
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
			doc_url = ?, slides_url = ?, summary = ?, requires_action = ?, error_message = ?,
			version = ?, last_actor = ?, created_at = ?, updated_at = ?, steps_json = ?
		WHERE task_id = ?
	`,
		task.Title, task.UserInstruction, task.Source, task.Status, task.CurrentStep, task.ProgressText,
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

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
