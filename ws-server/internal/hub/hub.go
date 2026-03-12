package hub

import (
	"encoding/json"
	"log"
	"sync"
)

type Message struct {
	Channel string      `json:"channel"`
	Symbol  string      `json:"symbol"`
	Data    interface{} `json:"data"`
}

type Client struct {
	send chan []byte
}

func NewClient() *Client {
	return &Client{
		send: make(chan []byte, 256),
	}
}

func (c *Client) Send() <-chan []byte {
	return c.send
}

type Hub struct {
	clients    map[*Client]struct{}
	mu         sync.RWMutex
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client
}

func New() *Hub {
	return &Hub{
		clients:    make(map[*Client]struct{}),
		broadcast:  make(chan []byte, 512),
		register:   make(chan *Client),
		unregister: make(chan *Client),
	}
}

func (h *Hub) Register(c *Client) {
	h.register <- c
}

func (h *Hub) Unregister(c *Client) {
	h.unregister <- c
}

func (h *Hub) Broadcast(msg Message) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("⚠️ hub: не удалось сериализовать сообщение: %v", err)
		return
	}
	select {
	case h.broadcast <- data:
	default:
		log.Printf("⚠️ hub: broadcast канал переполнен, сообщение пропущено")
	}
}

func (h *Hub) Run() {
	log.Println("🔀 Hub запущен")
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = struct{}{}
			h.mu.Unlock()
			log.Printf("✅ Hub: клиент подключился (всего: %d)", len(h.clients))

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
			h.mu.Unlock()
			log.Printf("👋 Hub: клиент отключился (всего: %d)", len(h.clients))

		case data := <-h.broadcast:
			h.mu.RLock()
			for client := range h.clients {
				select {
				case client.send <- data:
				default:
					h.mu.RUnlock()
					h.mu.Lock()
					delete(h.clients, client)
					close(client.send)
					h.mu.Unlock()
					h.mu.RLock()
					log.Printf("⚠️ Hub: клиент отключён (переполнен буфер)")
				}
			}
			h.mu.RUnlock()
		}
	}
}

func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}
