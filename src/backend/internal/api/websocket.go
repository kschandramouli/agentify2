package api

import (
	"net/http"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// TODO: restrict origins in production
		return true
	},
}

// ChatMessage represents a message in the chat interface.
type ChatMessage struct {
	Type    string            `json:"type"` // "user_message" | "agent_response" | "error"
	Text    string            `json:"text"`
	Context map[string]string `json:"context,omitempty"`
}

// HandleChatWebSocket upgrades the connection to WebSocket and handles chat.
func (h *Handler) HandleChatWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Error("websocket upgrade failed", "error", err)
		return
	}
	defer conn.Close()

	// TODO: implement chat message handling loop
	// for {
	//   var msg ChatMessage
	//   if err := conn.ReadJSON(&msg); err != nil {
	//     h.logger.Error("websocket read error", "error", err)
	//     return
	//   }
	//   // Process message, call agent, send response
	// }
}
