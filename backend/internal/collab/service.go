package collab

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"agentpilot/backend/internal/domain"
)

type TaskRepository interface {
	Get(ctx context.Context, taskID string) (domain.Task, error)
	Update(ctx context.Context, task domain.Task) (domain.Task, error)
}

type Document struct {
	DocKey               string    `json:"docKey"`
	TaskID               string    `json:"taskId"`
	Kind                 string    `json:"kind"`
	Title                string    `json:"title"`
	SourcePath           string    `json:"sourcePath,omitempty"`
	SnapshotSeq          int64     `json:"snapshotSeq"`
	SnapshotUpdateBase64 string    `json:"snapshotUpdateBase64,omitempty"`
	MarkdownCache        string    `json:"markdownCache"`
	Editable             bool      `json:"editable"`
	CreatedAt            time.Time `json:"createdAt"`
	UpdatedAt            time.Time `json:"updatedAt"`
}

type Update struct {
	DocKey       string    `json:"docKey"`
	Seq          int64     `json:"seq"`
	ClientID     string    `json:"clientId"`
	UpdateBase64 string    `json:"updateBase64"`
	CreatedAt    time.Time `json:"createdAt"`
}

type StateResponse struct {
	DocKey               string `json:"docKey"`
	SnapshotSeq          int64  `json:"snapshotSeq"`
	SnapshotUpdateBase64 string `json:"snapshotUpdateBase64,omitempty"`
}

type SnapshotRequest struct {
	BaseSeq              int64  `json:"baseSeq"`
	SnapshotUpdateBase64 string `json:"snapshotUpdateBase64"`
	MarkdownCache        string `json:"markdownCache"`
	ClientID             string `json:"clientId"`
}

type ExportRequest struct {
	Markdown             string `json:"markdown"`
	BaseSeq              int64  `json:"baseSeq"`
	SnapshotUpdateBase64 string `json:"snapshotUpdateBase64"`
	ClientID             string `json:"clientId"`
}

type Service struct {
	db          *sql.DB
	tasks       TaskRepository
	artifactDir string
	upgrader    websocket.Upgrader
	mu          sync.Mutex
	rooms       map[string]map[*websocket.Conn]struct{}
}

func (s *Service) Task(ctx context.Context, taskID string) (domain.Task, error) {
	return s.tasks.Get(ctx, taskID)
}

func NewService(db *sql.DB, taskStore TaskRepository) (*Service, error) {
	service := &Service{
		db:    db,
		tasks: taskStore,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(_ *http.Request) bool { return true },
		},
		rooms: make(map[string]map[*websocket.Conn]struct{}),
	}
	if err := service.migrate(); err != nil {
		return nil, err
	}
	return service, nil
}

func (s *Service) SetArtifactDir(path string) {
	s.artifactDir = strings.TrimSpace(path)
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/collab/docs/", s.handleCollabDoc)
}

func (s *Service) EnsureMarkdownDocument(ctx context.Context, taskID string) (Document, error) {
	task, err := s.tasks.Get(ctx, taskID)
	if err != nil {
		return Document{}, err
	}
	docKey := docKeyForTask(taskID)
	if doc, err := s.getDocument(ctx, docKey); err == nil {
		return doc, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Document{}, err
	}

	markdown := ""
	sourcePath := s.markdownSourcePath(task)
	if sourcePath != "" {
		data, readErr := os.ReadFile(sourcePath)
		if readErr != nil {
			return Document{}, fmt.Errorf("read markdown artifact: %w", readErr)
		}
		markdown = string(data)
	}
	now := time.Now()
	doc := Document{
		DocKey:        docKey,
		TaskID:        task.TaskID,
		Kind:          "markdown",
		Title:         task.Title,
		SourcePath:    sourcePath,
		SnapshotSeq:   0,
		MarkdownCache: markdown,
		Editable:      sourcePath != "" || markdown != "",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := s.insertDocument(ctx, doc, nil); err != nil {
		return Document{}, err
	}
	return doc, nil
}

func (s *Service) State(ctx context.Context, docKey string) (StateResponse, error) {
	doc, err := s.getDocument(ctx, docKey)
	if err != nil {
		return StateResponse{}, err
	}
	return StateResponse{
		DocKey:               doc.DocKey,
		SnapshotSeq:          doc.SnapshotSeq,
		SnapshotUpdateBase64: doc.SnapshotUpdateBase64,
	}, nil
}

func (s *Service) UpdatesSince(ctx context.Context, docKey string, sinceSeq int64) ([]Update, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT doc_key, seq, client_id, update_blob, created_at
		FROM collab_updates
		WHERE doc_key = ? AND seq > ?
		ORDER BY seq ASC
	`, docKey, sinceSeq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var updates []Update
	for rows.Next() {
		update, err := scanUpdate(rows)
		if err != nil {
			return nil, err
		}
		updates = append(updates, update)
	}
	if updates == nil {
		updates = []Update{}
	}
	return updates, rows.Err()
}

func (s *Service) SaveSnapshot(ctx context.Context, docKey string, req SnapshotRequest) (Document, error) {
	snapshot, err := base64.StdEncoding.DecodeString(strings.TrimSpace(req.SnapshotUpdateBase64))
	if err != nil {
		return Document{}, fmt.Errorf("invalid snapshot update: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Document{}, err
	}
	defer tx.Rollback()

	doc, err := getDocumentTx(ctx, tx, docKey)
	if err != nil {
		return Document{}, err
	}
	if req.BaseSeq < doc.SnapshotSeq {
		return Document{}, fmt.Errorf("snapshot baseSeq %d is older than current snapshotSeq %d", req.BaseSeq, doc.SnapshotSeq)
	}

	now := time.Now()
	_, err = tx.ExecContext(ctx, `
		UPDATE collab_documents
		SET snapshot_seq = ?, snapshot_update = ?, current_markdown_cache = ?, updated_at = ?
		WHERE doc_key = ?
	`, req.BaseSeq, snapshot, req.MarkdownCache, now.Format(time.RFC3339Nano), docKey)
	if err != nil {
		return Document{}, err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM collab_updates WHERE doc_key = ? AND seq <= ?`, docKey, req.BaseSeq)
	if err != nil {
		return Document{}, err
	}
	if err := tx.Commit(); err != nil {
		return Document{}, err
	}
	return s.getDocument(ctx, docKey)
}

func (s *Service) ExportMarkdown(ctx context.Context, docKey string, req ExportRequest) (Document, error) {
	markdown := req.Markdown
	if strings.TrimSpace(req.SnapshotUpdateBase64) != "" {
		if _, err := s.SaveSnapshot(ctx, docKey, SnapshotRequest{
			BaseSeq:              req.BaseSeq,
			SnapshotUpdateBase64: req.SnapshotUpdateBase64,
			MarkdownCache:        markdown,
			ClientID:             req.ClientID,
		}); err != nil {
			return Document{}, err
		}
	}

	doc, err := s.getDocument(ctx, docKey)
	if err != nil {
		return Document{}, err
	}
	path := strings.TrimSpace(doc.SourcePath)
	if path == "" {
		path = filepath.Join("data", "pilot_artifacts", safeFileName(doc.DocKey)+".md")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return Document{}, err
	}
	if err := os.WriteFile(path, []byte(markdown), 0644); err != nil {
		return Document{}, err
	}

	now := time.Now()
	_, err = s.db.ExecContext(ctx, `
		UPDATE collab_documents
		SET source_path = ?, current_markdown_cache = ?, updated_at = ?
		WHERE doc_key = ?
	`, path, markdown, now.Format(time.RFC3339Nano), docKey)
	if err != nil {
		return Document{}, err
	}

	task, err := s.tasks.Get(ctx, doc.TaskID)
	if err == nil && task.DocArtifactPath != path {
		task.DocArtifactPath = path
		task.Version++
		task.UpdatedAt = now
		task.LastActor = "collab"
		_, _ = s.tasks.Update(ctx, task)
	}
	return s.getDocument(ctx, docKey)
}

func (s *Service) Handler() http.Handler {
	return http.HandlerFunc(s.handleCollabDoc)
}

func (s *Service) handleCollabDoc(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/collab/docs/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[0] == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	docKey := parts[0]
	action := parts[1]

	switch {
	case action == "state" && r.Method == http.MethodGet:
		state, err := s.State(r.Context(), docKey)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, state)
	case action == "updates" && r.Method == http.MethodGet:
		sinceSeq := int64(0)
		if raw := strings.TrimSpace(r.URL.Query().Get("sinceSeq")); raw != "" {
			_, _ = fmt.Sscan(raw, &sinceSeq)
		}
		updates, err := s.UpdatesSince(r.Context(), docKey, sinceSeq)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, updates)
	case action == "snapshot" && r.Method == http.MethodPost:
		var req SnapshotRequest
		if err := readJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
			return
		}
		doc, err := s.SaveSnapshot(r.Context(), docKey, req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, doc)
	case action == "export" && r.Method == http.MethodPost:
		var req ExportRequest
		if err := readJSON(r, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
			return
		}
		doc, err := s.ExportMarkdown(r.Context(), docKey, req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, doc)
	case action == "ws" && r.Method == http.MethodGet:
		s.handleWebSocket(w, r, docKey)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (s *Service) handleWebSocket(w http.ResponseWriter, r *http.Request, docKey string) {
	if _, err := s.getDocument(r.Context(), docKey); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	s.join(docKey, conn)
	defer func() {
		s.leave(docKey, conn)
		_ = conn.Close()
	}()

	clientID := strings.TrimSpace(r.URL.Query().Get("clientId"))
	for {
		var msg wsMessage
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}
		if msg.Type != "update" || strings.TrimSpace(msg.UpdateBase64) == "" {
			continue
		}
		update, err := s.appendUpdate(r.Context(), docKey, fallback(msg.ClientID, clientID), msg.UpdateBase64)
		if err != nil {
			_ = conn.WriteJSON(wsMessage{Type: "error", Error: err.Error()})
			continue
		}
		out := wsMessage{
			Type:         "update",
			DocKey:       docKey,
			Seq:          update.Seq,
			ClientID:     update.ClientID,
			UpdateBase64: update.UpdateBase64,
		}
		s.broadcast(docKey, out)
	}
}

func (s *Service) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS collab_documents (
			doc_key TEXT PRIMARY KEY,
			task_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			title TEXT NOT NULL,
			source_path TEXT NOT NULL DEFAULT '',
			snapshot_seq INTEGER NOT NULL DEFAULT 0,
			snapshot_update BLOB,
			current_markdown_cache TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS collab_updates (
			doc_key TEXT NOT NULL,
			seq INTEGER NOT NULL,
			client_id TEXT NOT NULL DEFAULT '',
			update_blob BLOB NOT NULL,
			created_at TEXT NOT NULL,
			PRIMARY KEY (doc_key, seq)
		);
		CREATE INDEX IF NOT EXISTS idx_collab_updates_doc_seq
			ON collab_updates(doc_key, seq);
	`)
	return err
}

func (s *Service) insertDocument(ctx context.Context, doc Document, snapshot []byte) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO collab_documents (
			doc_key, task_id, kind, title, source_path, snapshot_seq, snapshot_update,
			current_markdown_cache, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, doc.DocKey, doc.TaskID, doc.Kind, doc.Title, doc.SourcePath, doc.SnapshotSeq, snapshot,
		doc.MarkdownCache, doc.CreatedAt.Format(time.RFC3339Nano), doc.UpdatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *Service) getDocument(ctx context.Context, docKey string) (Document, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT doc_key, task_id, kind, title, source_path, snapshot_seq, snapshot_update,
			current_markdown_cache, created_at, updated_at
		FROM collab_documents
		WHERE doc_key = ?
	`, docKey)
	return scanDocument(row)
}

func getDocumentTx(ctx context.Context, tx *sql.Tx, docKey string) (Document, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT doc_key, task_id, kind, title, source_path, snapshot_seq, snapshot_update,
			current_markdown_cache, created_at, updated_at
		FROM collab_documents
		WHERE doc_key = ?
	`, docKey)
	return scanDocument(row)
}

func (s *Service) appendUpdate(ctx context.Context, docKey, clientID, rawUpdate string) (Update, error) {
	update, err := base64.StdEncoding.DecodeString(strings.TrimSpace(rawUpdate))
	if err != nil {
		return Update{}, fmt.Errorf("invalid update: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Update{}, err
	}
	defer tx.Rollback()

	var updateMax int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM collab_updates WHERE doc_key = ?`, docKey).Scan(&updateMax); err != nil {
		return Update{}, err
	}
	var snapshotSeq int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(snapshot_seq, 0) FROM collab_documents WHERE doc_key = ?`, docKey).Scan(&snapshotSeq); err != nil {
		return Update{}, err
	}
	seq := updateMax
	if snapshotSeq > seq {
		seq = snapshotSeq
	}
	seq++
	now := time.Now()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO collab_updates (doc_key, seq, client_id, update_blob, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, docKey, seq, clientID, update, now.Format(time.RFC3339Nano))
	if err != nil {
		return Update{}, err
	}
	if err := tx.Commit(); err != nil {
		return Update{}, err
	}
	return Update{
		DocKey:       docKey,
		Seq:          seq,
		ClientID:     clientID,
		UpdateBase64: rawUpdate,
		CreatedAt:    now,
	}, nil
}

func (s *Service) join(docKey string, conn *websocket.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rooms[docKey] == nil {
		s.rooms[docKey] = make(map[*websocket.Conn]struct{})
	}
	s.rooms[docKey][conn] = struct{}{}
}

func (s *Service) leave(docKey string, conn *websocket.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rooms[docKey], conn)
	if len(s.rooms[docKey]) == 0 {
		delete(s.rooms, docKey)
	}
}

func (s *Service) broadcast(docKey string, msg wsMessage) {
	s.mu.Lock()
	clients := make([]*websocket.Conn, 0, len(s.rooms[docKey]))
	for conn := range s.rooms[docKey] {
		clients = append(clients, conn)
	}
	s.mu.Unlock()
	for _, conn := range clients {
		_ = conn.WriteJSON(msg)
	}
}

type wsMessage struct {
	Type         string `json:"type"`
	DocKey       string `json:"docKey,omitempty"`
	Seq          int64  `json:"seq,omitempty"`
	ClientID     string `json:"clientId,omitempty"`
	UpdateBase64 string `json:"updateBase64,omitempty"`
	Error        string `json:"error,omitempty"`
}

type scanner interface {
	Scan(dest ...any) error
}

func scanDocument(row scanner) (Document, error) {
	var doc Document
	var snapshot []byte
	var createdAt string
	var updatedAt string
	if err := row.Scan(
		&doc.DocKey, &doc.TaskID, &doc.Kind, &doc.Title, &doc.SourcePath, &doc.SnapshotSeq, &snapshot,
		&doc.MarkdownCache, &createdAt, &updatedAt,
	); err != nil {
		return Document{}, err
	}
	if len(snapshot) > 0 {
		doc.SnapshotUpdateBase64 = base64.StdEncoding.EncodeToString(snapshot)
	}
	doc.Editable = true
	doc.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	doc.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return doc, nil
}

func scanUpdate(row scanner) (Update, error) {
	var update Update
	var blob []byte
	var createdAt string
	if err := row.Scan(&update.DocKey, &update.Seq, &update.ClientID, &blob, &createdAt); err != nil {
		return Update{}, err
	}
	update.UpdateBase64 = base64.StdEncoding.EncodeToString(blob)
	update.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	return update, nil
}

func docKeyForTask(taskID string) string {
	return taskID + ":doc"
}

func (s *Service) markdownSourcePath(task domain.Task) string {
	if path := strings.TrimSpace(task.DocArtifactPath); path != "" {
		return path
	}
	docURL := strings.TrimSpace(task.DocURL)
	if !strings.HasPrefix(docURL, "/artifacts/") || !strings.HasSuffix(strings.ToLower(docURL), ".md") {
		return ""
	}
	name := strings.TrimPrefix(docURL, "/artifacts/")
	name = strings.TrimLeft(name, `/\`)
	if name == "" || strings.Contains(name, "..") {
		return ""
	}
	artifactDir := strings.TrimSpace(s.artifactDir)
	if artifactDir == "" {
		artifactDir = filepath.Join("data", "pilot_artifacts")
	}
	return filepath.Join(artifactDir, filepath.FromSlash(name))
}

func safeFileName(value string) string {
	value = strings.TrimSpace(value)
	replacer := strings.NewReplacer("\\", "_", "/", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	value = replacer.Replace(value)
	if value == "" {
		return "collab_doc"
	}
	return value
}

func fallback(value, defaultValue string) string {
	if strings.TrimSpace(value) == "" {
		return defaultValue
	}
	return value
}

func readJSON(r *http.Request, target any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(target)
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
