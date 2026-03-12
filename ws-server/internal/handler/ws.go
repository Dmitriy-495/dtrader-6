package handler

import (
	"log"
	"net/http"

	"github.com/gorilla/websocket"
	"github.com/Dmitriy-495/dtrader-6/ws-server/internal/hub"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type WSHandler struct {
	hub    *hub.Hub
	apiKey string
}

func New(h *hub.Hub, apiKey string) *WSHandler {
	return &WSHandler{hub: h, apiKey: apiKey}
}

func (h *WSHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	apiKey := r.Header.Get("X-API-Key")
	if apiKey != h.apiKey {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		log.Printf("⛔ WS: отклонено подключение с неверным ключом от %s", r.RemoteAddr)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("❌ WS upgrade error: %v", err)
		return
	}

	client := hub.NewClient()
	h.hub.Register(client)
	log.Printf("✅ WS: клиент подключился %s", r.RemoteAddr)

	go func() {
		defer func() {
			h.hub.Unregister(client)
			conn.Close()
		}()
		for data := range client.Send() {
			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				log.Printf("⚠️ WS write error %s: %v", r.RemoteAddr, err)
				return
			}
		}
	}()

	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				log.Printf("👋 WS: клиент отключился %s", r.RemoteAddr)
			} else {
				log.Printf("⚠️ WS read error %s: %v", r.RemoteAddr, err)
			}
			h.hub.Unregister(client)
			return
		}
	}
}
