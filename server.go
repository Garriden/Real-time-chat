package main

import (
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// We'll need to define an Upgrader.
// It will be used to upgrade a standard HTTP connection to a WebSocket connection.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// CheckOrigin allows connections from all origins, which is useful for
	// development with a separate frontend. In a production environment,
	// you should restrict this to your specific domain.
	CheckOrigin: func(r *http.Request) bool { return true }, // TODO: Restrict in production
}

// Client represents a single connected user.
type Client struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

// NewClient creates and initializes a new Client.
func NewClient(conn *websocket.Conn) *Client {
	return &Client{conn: conn}
}

// Manager is a server that manages the WebSocket connections.
type Manager struct {
	clients    map[*Client]bool
	mu         sync.Mutex
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client
}

// NewManager creates and initializes a new Manager.
func NewManager() *Manager {
	return &Manager{
		clients:    make(map[*Client]bool),
		broadcast:  make(chan []byte),
		register:   make(chan *Client),
		unregister: make(chan *Client),
	}
}

// Run starts the manager's event loop to handle client connections and broadcasts.
func (m *Manager) Run() {
	for {
		select {
		case client := <-m.register:
			m.mu.Lock()
			m.clients[client] = true
			m.mu.Unlock()
			log.Println("New client connected. Total clients:", len(m.clients))

		case client := <-m.unregister:
			m.mu.Lock()
			if _, ok := m.clients[client]; ok {
				delete(m.clients, client)
				client.conn.Close()
				log.Println("Client disconnected. Total clients:", len(m.clients))
			}
			m.mu.Unlock()

		case message := <-m.broadcast:
			m.mu.Lock()
			for client := range m.clients {
				// Use a goroutine to send the message to avoid blocking the main broadcast loop.
				go func(c *Client) {
					c.mu.Lock()
					err := c.conn.WriteMessage(websocket.TextMessage, message)
					c.mu.Unlock()
					if err != nil {
						log.Printf("Error sending message to client: %v", err)
						c.conn.Close()
						m.unregister <- c
					}
				}(client)
			}
			m.mu.Unlock()
		}
	}
}

// wsHandler handles WebSocket requests.
func (m *Manager) wsHandler(w http.ResponseWriter, r *http.Request) {
	// Upgrade the HTTP connection to a WebSocket connection.
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Upgrade failed:", err)
		return
	}

	// Create a new client and register it with the manager.
	client := NewClient(conn)
	m.register <- client

	// Start a goroutine to read messages from the client.
	go m.readMessages(client)
}

// readMessages reads messages from a client and sends them to the broadcast channel.
func (m *Manager) readMessages(client *Client) {
	defer func() {
		m.unregister <- client
	}()

	for {
		// Read a message from the client.
		messageType, p, err := client.conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				log.Println("Client disconnected gracefully.")
			} else {
				log.Println("Read error:", err)
			}
			break
		}
		// Only handle text messages.
		if messageType == websocket.TextMessage {
			// Send the received message to the broadcast channel.
			m.broadcast <- p
			log.Printf("Received message: %s", string(p))
		}
	}
}

func main() {
	// Create and start the manager.
	manager := NewManager()
	go manager.Run()

	// Define the HTTP handler for WebSocket connections.
	http.HandleFunc("/ws", manager.wsHandler)

	// Define a handler for the root path to serve a simple welcome message.
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Go WebSocket Server running. Connect to /ws for chat."))
	})

	port := ":8080"
	server := &http.Server{
		Addr:              port,
		ReadHeaderTimeout: 3 * time.Second,
	}

	fmt.Printf("Go WebSocket server started on port %s\n", port)
	log.Fatal(server.ListenAndServe())
}
