package hub

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Broker struct {
	mu    sync.RWMutex
	rooms map[int64]map[*client]struct{}
}

type client struct {
	conn  *websocket.Conn
	send  chan []byte
	scope Scope
}

func NewBroker() *Broker { return &Broker{rooms: map[int64]map[*client]struct{}{}} }

func (b *Broker) Publish(companyID, userID int64, event string, payload any) {
	msg, err := json.Marshal(map[string]any{"event": event, "data": payload})
	if err != nil {
		return
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for c := range b.rooms[companyID] {
		if !c.scope.Receives(userID) {
			continue
		}
		select {
		case c.send <- msg:
		default:
		}
	}
}

func (b *Broker) join(companyID int64, c *client) {
	b.mu.Lock()
	if b.rooms[companyID] == nil {
		b.rooms[companyID] = map[*client]struct{}{}
	}
	b.rooms[companyID][c] = struct{}{}
	b.mu.Unlock()
}

func (b *Broker) leave(companyID int64, c *client) {
	b.mu.Lock()
	delete(b.rooms[companyID], c)
	if len(b.rooms[companyID]) == 0 {
		delete(b.rooms, companyID)
	}
	b.mu.Unlock()
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	companyID, _ := strconv.ParseInt(r.URL.Query().Get("company_id"), 10, 64)
	userID, err := s.verifyWSTicket(r.URL.Query().Get("ticket"), companyID, time.Now())
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	scope, ok := s.tickets.take(r.URL.Query().Get("ticket"), time.Now())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return s.originAllowed(r.Header.Get("Origin")) }}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := &client{conn: conn, send: make(chan []byte, 64), scope: scope}
	done := make(chan struct{})
	s.broker.join(companyID, c)
	log.Printf("hub: ws joined company=%d user=%d", companyID, userID)

	go func() {
		defer func() {
			s.broker.leave(companyID, c)
			close(done)
			conn.Close()
		}()
		conn.SetReadLimit(4096)
		conn.SetReadDeadline(time.Now().Add(70 * time.Second))
		conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(70 * time.Second)) })
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	go func() {
		ping := time.NewTicker(30 * time.Second)
		defer ping.Stop()
		hello, _ := json.Marshal(map[string]any{"event": "connected", "data": map[string]any{"company_id": companyID}})
		c.send <- hello
		for {
			select {
			case <-done:
				return
			case msg := <-c.send:
				conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
					conn.Close()
					return
				}
			case <-ping.C:
				conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					conn.Close()
					return
				}
			}
		}
	}()
}
