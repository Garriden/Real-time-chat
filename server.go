package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type MessageData struct {
	Type     string `json:"type"`
	Username string `json:"username"`
	Content  string `json:"content"`
}

func BuildJson(messageType, username, content string) ([]byte, error) {
	message := MessageData{
		Type:     messageType,
		Username: username,
		Content:  content,
	}

	jsonData, err := json.Marshal(message)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal JSON: %w", err)
	}

	return jsonData, nil
}

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

// UserListMessage struct to be sent as JSON
type UserListMessage struct {
	Type  string   `json:"type"`
	Users []string `json:"users"`
}

// Client represents a single connected user.
type Client struct {
	conn *websocket.Conn
	mu   sync.Mutex
	name string
	id   string
}

// NewClient creates and initializes a new Client.
func NewClient(conn *websocket.Conn, name string) *Client {
	return &Client{
		conn: conn,
		name: name,
		id:   uuid.New().String(),
	}
}

type broadcastMsg struct {
	message []byte
	sender  *Client
}

// Manager is a server that manages the WebSocket connections.
type Manager struct {
	clients    map[*Client]bool
	mu         sync.Mutex
	broadcast  chan broadcastMsg
	register   chan *Client
	unregister chan *Client
}

// NewManager creates and initializes a new Manager.
func NewManager() *Manager {
	return &Manager{
		clients:    make(map[*Client]bool),
		broadcast:  make(chan broadcastMsg),
		register:   make(chan *Client),
		unregister: make(chan *Client),
	}
}

// Run starts the manager's event loop to handle client connections and broadcasts.
func (m *Manager) Run() {
	for {
		select {
		case newClient := <-m.register:
			// New Client has connected.
			m.mu.Lock()
			m.clients[newClient] = true

			//userNames := []string{} // Translate clients into an string array
			// TODO: Broadcast ?
			for client := range m.clients {
				//userNames = append(userNames, client.name) // TODO: need to convert it every time ?
				if client == newClient {
					//continue // Skip sender
				} else {
					go func(c *Client) {
						c.mu.Lock()

						//newClientHasArrivedMessage := []byte(newClient.name + " has connected.")
						jsonMessage, _ := BuildJson("connection", newClient.name, " has connected.")
						err := c.conn.WriteMessage(websocket.TextMessage, jsonMessage)

						c.mu.Unlock()

						if err != nil {
							log.Printf("Error sending message to client: %v", err)
							c.conn.Close()
							m.unregister <- c
						}

					}(client)
				}

				UpdateUserList(m)

			}

			m.mu.Unlock()
			log.Println("New client connected. Total clients:", len(m.clients))

		case client := <-m.unregister:
			m.mu.Lock()

			if _, ok := m.clients[client]; ok {
				delete(m.clients, client)
				client.conn.Close()
				log.Println("Client disconnected. Total clients:", len(m.clients))
			}

			UpdateUserList(m)

			m.mu.Unlock()

		case bmsg := <-m.broadcast:
			m.mu.Lock()
			for client := range m.clients {
				if client == bmsg.sender {
					continue // Skip sender
				}
				go func(c *Client) {
					c.mu.Lock()
					err := c.conn.WriteMessage(websocket.TextMessage, bmsg.message)
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
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("Upgrade failed:", err)
		return
	}

	// Read the first message as the username
	_, nameMsg, err := conn.ReadMessage()
	if err != nil {
		log.Println("Failed to read username:", err)
		conn.Close()
		return
	}
	raw := string(nameMsg)
	prefix := "UserEntering: "
	var name string
	if len(raw) >= len(prefix) && raw[:len(prefix)] == prefix {
		name = raw[len(prefix):]
	} else {
		name = raw // fallback if prefix not present
	}

	client := NewClient(conn, name)
	m.register <- client

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

				jsonMessage, _ := BuildJson("connection", client.name, " has left.")

				m.broadcast <- broadcastMsg{message: jsonMessage, sender: client}
			} else {
				log.Println("Read error:", err)
			}
			break
		}
		// Only handle text messages.
		if messageType == websocket.TextMessage {
			// Send the received message to the broadcast channel.
			m.broadcast <- broadcastMsg{message: p, sender: client}
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

func UpdateUserList(m *Manager) {
	userNames := []string{} // Translate clients into an string array
	// need to convert it every time ?
	for client := range m.clients {
		userNames = append(userNames, client.name) // TODO: Super ineficient.
	}
	// updateUserList.
	userListMsg := UserListMessage{
		Type:  "userList",
		Users: userNames,
	}
	// Marshal the struct into a JSON byte slice
	jsonMsg, err := json.Marshal(userListMsg)
	if err != nil {
		log.Printf("Error marshalling user list: %v", err)
		return
	}
	// Loop through all clients and send the message
	// TODO: Bcast ?
	for client := range m.clients {
		client.mu.Lock()
		// client.conn.WriteMessage is a blocking call. For production,
		// you should send this over a channel to a client goroutine.
		if err := client.conn.WriteMessage(websocket.TextMessage, jsonMsg); err != nil {
			log.Printf("Error sending user list to client: %v", err)
		}

		client.mu.Unlock()
	}
}
