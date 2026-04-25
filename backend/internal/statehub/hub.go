package statehub

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Envelope struct {
	EventType string      `json:"eventType"`
	TaskID    string      `json:"taskId"`
	Version   int         `json:"version"`
	Payload   interface{} `json:"payload"`
	EmittedAt time.Time   `json:"emittedAt"`
}

type Hub struct {
	upgrader websocket.Upgrader
	mu       sync.Mutex
	clients  map[*websocket.Conn]struct{}
}

func NewHub() *Hub {
	return &Hub{
		upgrader: websocket.Upgrader{
			CheckOrigin: func(_ *http.Request) bool { return true },
		},
		clients: make(map[*websocket.Conn]struct{}),
	}
}

func (h *Hub) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := h.upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}

		h.mu.Lock()
		h.clients[conn] = struct{}{}
		h.mu.Unlock()

		go func() {
			defer func() {
				h.mu.Lock()
				delete(h.clients, conn)
				h.mu.Unlock()
				_ = conn.Close()
			}()

			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		}()
	})
}

func (h *Hub) Broadcast(eventType, taskID string, version int, payload interface{}) {
	message, err := json.Marshal(Envelope{
		EventType: eventType,
		TaskID:    taskID,
		Version:   version,
		Payload:   payload,
		EmittedAt: time.Now(),
	})
	if err != nil {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	for client := range h.clients {
		if err := client.WriteMessage(websocket.TextMessage, message); err != nil {
			_ = client.Close()
			delete(h.clients, client)
		}
	}
}
