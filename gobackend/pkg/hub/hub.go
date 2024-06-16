package sockets

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/dgrijalva/jwt-go"
	"github.com/rs/zerolog/log"

	"aerolith.org/tetrolith/pkg/config"
)

const ConnPollPeriod = 60 * time.Second

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

	broadcastUser   chan UserMessage
	sendConnMessage chan ConnMessage
}

func NewHub(cfg *config.Config) (*Hub, error) {

	return &Hub{
		// broadcast:         make(chan []byte),
		broadcastUser:     make(chan UserMessage),
		sendConnMessage:   make(chan ConnMessage),
		register:          make(chan *Client),
		unregister:        make(chan *Client),
		clientsByUsername: make(map[string]map[*Client]bool),
		clientsByConnID:   make(map[string]*Client),
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

	return nil
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
	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		c.username, ok = claims["usn"].(string)
		if !ok {
			return errors.New("malformed token - unn")
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
