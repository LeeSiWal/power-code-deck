package ws

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 1048576 // 1MB
)

type Client struct {
	hub           *Hub
	conn          *websocket.Conn
	send          chan []byte
	watchingAgent string
	viewerID      string // stable id for this connection (SessionEngine viewer)
	deviceID      string // persistent per-browser id (from the ?device= query param)

	// attachCount counts, per agent, how many independent UI surfaces on THIS
	// connection asked to watch it.
	//
	// The browser has one WebSocket (a singleton) but several things that attach
	// through it: the terminal view, and the dashboard's thumbnails. They all share
	// this connection's single viewerID, so a thumbnail unmounting and sending
	// terminal:detach used to drop the viewer the terminal view still needed —
	// after which every keystroke was silently discarded by the write gate.
	//
	// So the engine viewer lives until the LAST surface detaches. Only readPump's
	// goroutine touches this, same as watchingAgent.
	attachCount map[string]int
	// Companion shells are independent of watchingAgent: a native chat and its
	// scratch shell must remain attached at the same time.
	shellAttached map[string]bool
	shellMu       sync.RWMutex

	// foreground is the browser's own visibilitychange, reported explicitly. A device
	// that is looking at the app already gets the in-app toast, so pushing as well is
	// the double-buzz. Read from whatever goroutine raised the notification (not
	// readPump), hence its own mutex.
	//
	// Defaults to FALSE, and that default matters: an unknown state must mean "push
	// it". Suppressing on a guess loses the alert entirely, and a missed 승인 필요
	// leaves the agent blocked with nobody watching.
	foreground bool
	fgMu       sync.RWMutex
}

func (c *Client) addShell(sessionID string) {
	c.shellMu.Lock()
	defer c.shellMu.Unlock()
	if c.shellAttached == nil {
		c.shellAttached = make(map[string]bool)
	}
	c.shellAttached[sessionID] = true
}

func (c *Client) removeShell(sessionID string) {
	c.shellMu.Lock()
	defer c.shellMu.Unlock()
	delete(c.shellAttached, sessionID)
}

func (c *Client) hasShell(sessionID string) bool {
	c.shellMu.RLock()
	defer c.shellMu.RUnlock()
	return c.shellAttached[sessionID]
}

func (c *Client) shells() []string {
	c.shellMu.RLock()
	defer c.shellMu.RUnlock()
	ids := make([]string, 0, len(c.shellAttached))
	for id := range c.shellAttached {
		ids = append(ids, id)
	}
	return ids
}

func (c *Client) readPump() {
	defer func() {
		c.hub.unregister(c)
		c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("WebSocket read error: %v", err)
			}
			break
		}

		var msg WSMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			log.Printf("Invalid WS message: %v", err)
			continue
		}

		c.hub.handleMessage(c, msg)
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (c *Client) sendEvent(event string, payload interface{}) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	msg := WSMessage{
		Event:   event,
		Payload: json.RawMessage(data),
	}
	msgBytes, err := json.Marshal(msg)
	if err != nil {
		return
	}

	select {
	case c.send <- msgBytes:
	default:
		// Buffer full, skip
	}
}

func (c *Client) setForeground(v bool) {
	c.fgMu.Lock()
	c.foreground = v
	c.fgMu.Unlock()
}

func (c *Client) isForeground() bool {
	c.fgMu.RLock()
	defer c.fgMu.RUnlock()
	return c.foreground
}
