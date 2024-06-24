package sockets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/rs/zerolog/log"

	"aerolith.org/tetrolith/pkg/config"
	"aerolith.org/tetrolith/pkg/game"
)

const ConnPollPeriod = 60 * time.Second

// A BroadcastMessage gets sent to all connected users.
type BroadcastMessage struct {
	msg []byte
}

// A UserMessage is a message that should be sent to a user.
type UserMessage struct {
	username string
	msg      []byte
}

// A ConnMessage is a message that just gets sent to a single socket connection.
type ConnMessage struct {
	connID string
	msg    []byte
}

// Hub maintains the set of active clients and broadcasts messages to the
// clients.
type Hub struct {
	// Registered clients.
	clientsByUsername map[string]map[*Client]bool
	clientsByConnID   map[string]*Client
	// Inbound messages from the clients.
	// broadcast chan []byte

	// Register requests from the clients.
	register chan *Client

	// Unregister requests from clients.
	unregister chan *Client

	broadcast       chan BroadcastMessage
	broadcastUser   chan UserMessage
	sendConnMessage chan ConnMessage

	gameSessionManager *game.SessionManager
	gameEventsOut      chan []byte
}

func NewHub(cfg *config.Config) (*Hub, error) {
	gevents := make(chan []byte, 32)
	return &Hub{
		// broadcast:         make(chan []byte),
		broadcastUser:      make(chan UserMessage),
		sendConnMessage:    make(chan ConnMessage),
		broadcast:          make(chan BroadcastMessage),
		register:           make(chan *Client),
		unregister:         make(chan *Client),
		clientsByUsername:  make(map[string]map[*Client]bool),
		clientsByConnID:    make(map[string]*Client),
		gameSessionManager: game.NewSessionManager(cfg, gevents),
		gameEventsOut:      gevents,
	}, nil
}

func (h *Hub) addClient(client *Client) error {

	// Add client to appropriate maps
	byUser := h.clientsByUsername[client.username]
	if byUser == nil {
		h.clientsByUsername[client.username] = make(map[*Client]bool)
	}
	// Add the new user ID to the map.
	h.clientsByUsername[client.username][client] = true
	h.clientsByConnID[client.connID] = client

	return h.sendInitInfo(client)
}

func (h *Hub) removeClient(c *Client) error {
	// no need to protect with mutex, only called from
	// single-threaded Run
	log.Debug().Str("client", c.username).Str("connid", c.connID).Msg("removing client")
	close(c.send)
	delete(h.clientsByConnID, c.connID)

	if (len(h.clientsByUsername[c.username])) == 1 {
		delete(h.clientsByUsername, c.username)
		log.Debug().Msgf("deleted client from clientsbyusername. New length %v", len(
			h.clientsByUsername))

		return nil
	}
	// Otherwise, delete just the right socket (this one: c)
	log.Debug().Interface("username", c.username).Int("numconn", len(h.clientsByUsername[c.username])).
		Msg("non-one-num-conns")
	delete(h.clientsByUsername[c.username], c)

	return nil
}

func (h *Hub) sendToConnID(connID string, msg []byte) error {
	h.sendConnMessage <- ConnMessage{connID: connID, msg: msg}
	return nil
}

func (h *Hub) sendToUser(username string, msg []byte) error {
	h.broadcastUser <- UserMessage{username: username, msg: msg}
	return nil
}

func (h *Hub) Run() {
	ticker := time.NewTicker(ConnPollPeriod)
	defer func() {
		ticker.Stop()
	}()

	for {
		select {
		case client := <-h.register:
			err := h.addClient(client)
			if err != nil {
				log.Err(err).Msg("error-adding-client")
			}

		case client := <-h.unregister:
			err := h.removeClient(client)
			if err != nil {
				log.Err(err).Msg("error-removing-client")
			}
			log.Info().Str("username", client.username).Msg("unregistered-client")

		case message := <-h.broadcastUser:
			log.Debug().Str("user", string(message.username)).
				Msg("sending to all user sockets")
			// Send the message to every socket belonging to this user.
			for client := range h.clientsByUsername[message.username] {
				select {
				case client.send <- message.msg:
				default:
					log.Debug().Str("username", client.username).Msg("in broadcastUser, removeClient")
					h.removeClient(client)
				}
			}

		case message := <-h.broadcast:
			for _, client := range h.clientsByConnID {
				select {
				case client.send <- message.msg:
				default:
					h.removeClient(client)
				}
			}

		case message := <-h.sendConnMessage:
			c, ok := h.clientsByConnID[message.connID]
			if !ok {
				// This client does not exist in this node.
				log.Debug().Str("connID", message.connID).Msg("connID-not-found")
			} else {
				select {
				case c.send <- message.msg:
				default:
					log.Debug().Str("connID", message.connID).Msg("in sendToConnID, removeClient")
					h.removeClient(c)
				}
			}

		case <-ticker.C:
			log.Info().Int("num-conns", len(h.clientsByConnID)).
				Int("num-users", len(h.clientsByUsername)).Msg("conn-stats")

		case message := <-h.gameEventsOut:
			// Event from a game. Send to appropriate sockets.
			gsm := &game.GameStateManager{}
			err := json.Unmarshal(message, gsm)
			if err != nil {
				log.Err(err).Msg("unmarshalling-state")
			}
			for _, p := range gsm.Players {
				for client := range h.clientsByUsername[p] {
					select {
					case client.send <- message:
					default:
						log.Debug().Str("connID", client.connID).Msg("in gevtsout, remove")
						h.removeClient(client)
					}
				}
			}
		}
	}
}

func (h *Hub) socketLogin(c *Client) error {

	token, err := jwt.Parse(c.connToken, func(token *jwt.Token) (interface{}, error) {
		// Don't forget to validate the alg is what you expect:
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("Unexpected signing method: %v", token.Header["alg"])
		}

		// hmacSampleSecret is a []byte containing your secret, e.g. []byte("my_secret_key")
		return []byte(os.Getenv("SECRET_KEY")), nil
	})
	if err != nil {
		return err
	}
	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		c.username, ok = claims["usn"].(string)
		if !ok {
			return errors.New("malformed token - usn")
		}
		log.Debug().Str("username", c.username).Msg("socket connection")
	}
	if err != nil {
		log.Err(err).Str("token", c.connToken).Msg("socket-login-failure")
	}
	if !token.Valid {
		return errors.New("invalid token")
	}
	return err
}

type SeekMsg struct {
	SearchCriteria json.RawMessage
	ListName       string
}

type GuessMsg struct {
	Gid   string
	Guess string
}

func (h *Hub) parseAndExecuteMessage(ctx context.Context, message []byte, c *Client) error {
	tp, pl, _ := bytes.Cut(message, []byte(" "))
	cmd := string(bytes.TrimSpace(tp))
	payload := string(bytes.TrimSpace(pl))
	switch cmd {
	case "SEEK": // SEEK json
		seekMsg := &SeekMsg{}
		err := json.Unmarshal(pl, seekMsg)
		if err != nil {
			return err
		}
		sess, err := h.gameSessionManager.Seek(c.username, seekMsg.ListName, seekMsg.SearchCriteria)
		if err != nil {
			return err
		}
		// broadcast seek
		var sk bytes.Buffer
		sk.WriteString("SEEK ")
		sjson, err := json.Marshal(sess)
		if err != nil {
			return err
		}
		sk.WriteString(string(sjson))
		h.broadcast <- BroadcastMessage{msg: sk.Bytes()}
	case "JOIN":
		_, err := h.gameSessionManager.Join(c.username, payload)
		if err != nil {
			return err
		}
		// broadcast join
		var sk bytes.Buffer
		sk.WriteString("JOIN ")
		sk.WriteString(c.username)
		sk.WriteString(" ")
		sk.WriteString(payload)
		h.broadcast <- BroadcastMessage{msg: sk.Bytes()}
	case "UNSEEK":
		err := h.gameSessionManager.Unseek(c.username)
		if err != nil {
			return err
		}
		// broadcast unseek
		var sk bytes.Buffer
		sk.WriteString("UNSEEK ")
		sk.WriteString(c.username)
		h.broadcast <- BroadcastMessage{msg: sk.Bytes()}
	case "SOLVE":
		guessMsg := &GuessMsg{}
		err := json.Unmarshal(pl, guessMsg)
		if err != nil {
			return err
		}
		err = h.gameSessionManager.SendGuess(c.username, guessMsg.Gid, guessMsg.Guess)
		if err != nil {
			return err
		}

	case "CHAT":

	case "LEAVE":
		err := h.gameSessionManager.Leave(c.username, payload)
		if err != nil {
			return err
		}
		// broadcast leave
		var sk bytes.Buffer
		sk.WriteString("LEAVE ")
		sk.WriteString(c.username)
		sk.WriteString(" ")
		sk.WriteString(payload)
		h.broadcast <- BroadcastMessage{msg: sk.Bytes()}
	default:
		return errors.New("badly formatted message")
	}
	return nil
}

func (h *Hub) sendInitInfo(client *Client) error {
	sessions, err := h.gameSessionManager.AllSessions()
	if err != nil {
		return err
	}
	sessionsMsg := []byte("SESSIONS ")
	sessionsMsg = append(sessionsMsg, sessions...)

	client.send <- sessionsMsg
	return nil
}
