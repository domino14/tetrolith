// Package sockets encapsulates all of the Websocket communication.
// It's part of the framework/drivers layer.
package sockets

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lithammer/shortuuid"
	"github.com/rs/zerolog/log"
)

var AllowedOrigins = []string{}

func init() {
	allowedOrigins := os.Getenv("ALLOWED_ORIGINS")
	if allowedOrigins != "" {
		for _, origin := range strings.Split(allowedOrigins, ",") {
			origin = strings.TrimSpace(origin)
			AllowedOrigins = append(AllowedOrigins, origin)
		}
	}
	log.Info().Interface("AllowedOrigins", AllowedOrigins).Msg("set allowed origins")
}

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 15 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = 5 * time.Second

	// Maximum message size allowed from peer.
	maxMessageSize = 512
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		if len(AllowedOrigins) == 0 {
			return true
		}
		originHeader := r.Header.Get("Origin")
		// https://woogles.io or https://www.woogles.io on production, for example.
		for _, origin := range AllowedOrigins {
			if originHeader == origin {
				return true
			}
		}
		return false
	},
}

// Client is a middleman between the websocket connection and the hub.
type Client struct {
	sync.RWMutex
	hub *Hub

	// The websocket connection.
	conn *websocket.Conn

	// Buffered channel of outbound messages.
	send chan []byte

	username string

	connID    string
	connToken string

	forwardedFor string
	pongCount    int
	lastPingSent time.Time
	// The round-trip lag; it is a sort of average.
	avglag time.Duration
}

func (c *Client) sendError(err error) {
	c.send <- []byte("ERROR: " + err.Error())
}

func (c *Client) sendLatency() {
	c.send <- []byte(fmt.Sprintf("LAGMS: %d", c.avglag/time.Millisecond))
}

// readPump pumps messages from the websocket connection to the hub.
//
// The application runs readPump in a per-connection goroutine. The application
// ensures that there is at most one reader on a connection by executing all
// reads from this goroutine.
func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()
	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		received := time.Now()
		c.RLock()
		curlag := received.Sub(c.lastPingSent)
		c.RUnlock()
		c.pongCount++
		var mix float64
		// Decaying average after the first four pongs. Thx lichess.
		if c.pongCount > 4 {
			mix = 0.1
		} else {
			mix = 1 / float64(c.pongCount)
		}
		c.avglag += time.Duration(mix * (float64(curlag) - float64(c.avglag)))

		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		if c.pongCount%10 == 2 {
			log.Info().Float64("curlag-ms", float64(curlag)/float64(time.Millisecond)).
				Float64("avglag-ms", float64(c.avglag)/float64(time.Millisecond)).
				Str("username", c.username).
				Int("pong-count", c.pongCount).
				Str("ips", c.forwardedFor).
				Str("connID", c.connID).
				Msg("got-pong")
		} //else {
		// This might be too noisy even for debug but let's enable this
		// for a bit.
		//log.Debug().Str("username", c.username).Msg("single-pong")
		//}
		c.sendLatency()
		return nil
	})
	for {
		// _, message, err
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Err(err).Msg("unexpected-close")
			}
			// Probably a regular disconnect:
			log.Debug().Str("username", c.username).Err(err).Msg("other-error-breaking-out")
			break
		}

		// Here is where we parse the message and send something off to the hub
		// potentially.

		err = parseAndExecuteMessage(context.Background(), message, c)
		if err != nil {
			log.Err(err).Str("username", c.username).Msg("parse-and-execute-message")
			c.sendError(err)
			continue
		}

		// message = bytes.TrimSpace(bytes.Replace(message, newline, space, -1))
		// c.hub.broadcast <- message
	}
}

// writePump pumps messages from the hub to the websocket connection.
//
// A goroutine running writePump is started for each connection. The
// application ensures that there is at most one writer to a connection by
// executing all writes from this goroutine.
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
				// The hub closed the channel.
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				log.Info().Msg("hub closed channel")
				// XXX: should we remove the connection here??
				// maybe not since the connection close happens in the defer.
				return
			}

			w, err := c.conn.NextWriter(websocket.BinaryMessage)
			if err != nil {
				return
			}
			w.Write(message)

			// Add queued messages to the current websocket message.
			n := len(c.send)
			for i := 0; i < n; i++ {
				w.Write(<-c.send)
			}

			if err := w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
			c.Lock()
			c.lastPingSent = time.Now()
			c.Unlock()
		}
	}
}

// close connection with an error string.
func closeMessage(ws *websocket.Conn, errStr string) {
	// close code 1008 is used for a generic "policy violation" message.
	msg := websocket.FormatCloseMessage(websocket.ClosePolicyViolation, errStr)
	log.Debug().Str("closemsg", string(msg)).Msg("writing close message")
	err := ws.WriteMessage(websocket.CloseMessage, msg)
	if err != nil {
		log.Err(err).Msg("writing close message back to user")
	}
	ws.Close()
}

// ServeWS handles websocket requests from the peer. This runs in its own
// goroutine.
func ServeWS(hub *Hub, w http.ResponseWriter, r *http.Request) {
	fwd := r.Header.Values("X-Forwarded-For")
	tokens, ok := r.URL.Query()["token"]
	log.Debug().Interface("ips", fwd).Msg("servews-new-conn")
	if !ok || len(tokens[0]) < 1 {
		log.Error().Msg("token is missing")
		return
	}

	token := tokens[0]

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Err(err).Msg("upgrading socket")
		return
	}

	client := &Client{
		hub:          hub,
		conn:         conn,
		send:         make(chan []byte, 256),
		connID:       shortuuid.New(),
		connToken:    token,
		forwardedFor: strings.Join(fwd, ","),
	}

	// First, verify connection token
	err = hub.socketLogin(client)
	if err != nil {
		log.Err(err).Msg("socket-login-error")
		client.conn.Close()
		return
	}

	client.hub.register <- client

	// Allow collection of memory referenced by the caller by doing all work in
	// new goroutines.
	go client.writePump()
	go client.readPump()
	log.Debug().Str("connID", client.connID).Msg("leaving-servews")
}

func parseAndExecuteMessage(ctx context.Context, message []byte, c *Client) error {
	tp, payload, found := bytes.Cut(message, []byte(": "))
	if found == false {
		return errors.New("badly formatted message")
	}
	switch string(tp) {
	case "SEEK":

	case "JOIN":

	case "UNSEEK":

	case "SOLVE":

	case "CHAT":

	}
	return nil
}
