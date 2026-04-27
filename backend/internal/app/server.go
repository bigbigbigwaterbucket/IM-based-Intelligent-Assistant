package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	_ "modernc.org/sqlite"

	"agentpilot/backend/internal/config"
	"agentpilot/backend/internal/larkbot"
	"agentpilot/backend/internal/orchestrator"
	"agentpilot/backend/internal/planner"
	"agentpilot/backend/internal/statehub"
	"agentpilot/backend/internal/store"
	"agentpilot/backend/internal/tools"
)

type Server struct {
	addr     string
	handler  http.Handler
	shutdown func()
}

func NewServer() (*Server, error) {
	if err := config.LoadDotEnv(os.Getenv("ENV_FILE"), "backend/.env", ".env"); err != nil {
		return nil, fmt.Errorf("load env file: %w", err)
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "file:agentpilot.db?_pragma=busy_timeout(5000)"
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	taskStore, err := store.NewSQLiteStore(db)
	if err != nil {
		return nil, err
	}

	hub := statehub.NewHub()
	planSvc := planner.NewService()
	toolRunner := tools.NewRunner(tools.Config{
		FeishuAppID:       os.Getenv("FEISHU_APP_ID"),
		FeishuAppSecret:   os.Getenv("FEISHU_APP_SECRET"),
		EnableFeishuTools: envFlag("ENABLE_FEISHU_TOOLS") || envFlag("ENABLE_LARK_TOOLS"),
		FeishuDocBaseURL:  os.Getenv("FEISHU_DOC_BASE_URL"),
		ArtifactDir:       envOrDefault("ARTIFACT_DIR", tools.ArtifactDir()),
	})
	orch := orchestrator.New(taskStore, hub, planSvc, toolRunner)

	shutdown := func() {}
	botConfig := larkbot.ConfigFromEnv()
	if botConfig.Enabled {
		botCtx, botCancel := context.WithCancel(context.Background())
		bot, err := larkbot.New(botConfig, orch)
		if err != nil {
			botCancel()
			return nil, fmt.Errorf("create feishu bot: %w", err)
		}
		bot.Start(botCtx)
		shutdown = botCancel
	}

	mux := http.NewServeMux()
	registerRoutes(mux, orch, hub, taskStore)

	return &Server{
		addr:     ":" + port,
		handler:  withCORS(mux),
		shutdown: shutdown,
	}, nil
}

func (s *Server) Addr() string {
	return s.addr
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

func (s *Server) Close() {
	if s.shutdown != nil {
		s.shutdown()
	}
}

func registerRoutes(mux *http.ServeMux, orch *orchestrator.Service, hub *statehub.Hub, taskStore store.TaskRepository) {
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/tasks", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			tasks, err := taskStore.List(r.Context())
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, tasks)
		case http.MethodPost:
			var req orchestrator.CreateTaskInput
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
				return
			}
			task, err := orch.CreateTask(r.Context(), req)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusCreated, task)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/tasks/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/tasks/")
		parts := strings.Split(path, "/")
		if len(parts) == 0 || parts[0] == "" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		taskID := parts[0]

		if len(parts) == 1 && r.Method == http.MethodGet {
			task, err := taskStore.Get(r.Context(), taskID)
			if err != nil {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, task)
			return
		}

		if len(parts) == 2 && parts[1] == "retry" && r.Method == http.MethodPost {
			task, err := orch.SubmitAction(r.Context(), taskID, orchestrator.ActionInput{
				ActionType: "retry_task",
				ActorType:  "desktop",
				ClientID:   "legacy-retry",
			})
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, task)
			return
		}

		if len(parts) == 2 && parts[1] == "actions" && r.Method == http.MethodPost {
			var req orchestrator.ActionInput
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
				return
			}
			task, err := orch.SubmitAction(r.Context(), taskID, req)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, task)
			return
		}

		w.WriteHeader(http.StatusNotFound)
	})

	mux.Handle("/ws", hub.Handler())
	mux.Handle("/artifacts/", http.StripPrefix("/artifacts/", http.FileServer(http.Dir(envOrDefault("ARTIFACT_DIR", tools.ArtifactDir())))))
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envFlag(key string) bool {
	return strings.EqualFold(os.Getenv(key), "true") || os.Getenv(key) == "1"
}
