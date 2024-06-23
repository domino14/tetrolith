package game

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"aerolith.org/tetrolith/pkg/config"
	"github.com/lithammer/shortuuid"
)

// a game session is a single instance of a game being played.
type GameSession struct {
	Players        []string // first one is the seeker
	ID             string   // game ID for URL
	ListName       string
	SearchCriteria []byte            // JSON representation of list search criteria
	GameManager    *GameStateManager `json:"-"`
}

type SessionManager struct {
	sync.Mutex

	Sessions          map[string]*GameSession // map of ID to session
	SessionsForPlayer map[string]*GameSession
	cfg               *config.Config
	eventsOut         chan []byte
}

func NewSessionManager(cfg *config.Config, eventsOut chan []byte) *SessionManager {
	return &SessionManager{
		Sessions:          make(map[string]*GameSession),
		SessionsForPlayer: make(map[string]*GameSession),
		cfg:               cfg,
		eventsOut:         eventsOut,
	}
}

func (s *SessionManager) SendGuess(sender, gid, guess string) error {
	s.Lock()
	defer s.Unlock()

	gs := s.Sessions[gid]
	if gs == nil {
		return errors.New("no session with that game id")
	}

	return gs.GameManager.Guess(sender, guess)
}

func (s *SessionManager) Seek(seeker, listname string, searchcriteria []byte) (*GameSession, error) {
	s.Lock()
	defer s.Unlock()
	if s, ok := s.SessionsForPlayer[seeker]; ok {
		errMsg := "player already in game session"
		if s.GameManager == nil {
			errMsg = "player already has a seek open"
		}
		return nil, errors.New(errMsg)
	}

	gs := &GameSession{
		Players:        []string{seeker},
		ID:             shortuuid.New(),
		ListName:       listname,
		SearchCriteria: searchcriteria,
	}
	s.Sessions[gs.ID] = gs
	s.SessionsForPlayer[seeker] = gs
	return gs, nil
}

func (s *SessionManager) Unseek(seeker string) error {
	s.Lock()
	defer s.Unlock()

	if sess, ok := s.SessionsForPlayer[seeker]; !ok {
		return errors.New("not seeking a game")
	} else if sess.GameManager != nil {
		return errors.New("game already started")
	} else {
		delete(s.Sessions, sess.ID)
		delete(s.SessionsForPlayer, seeker)
	}
	return nil
}

func CryptoSeed() [32]byte {
	cryptoseed := make([]byte, 32)
	_, err := rand.Read(cryptoseed)
	if err != nil {
		panic(err)
	}

	var seed [32]byte
	copy(seed[:], cryptoseed[:32])
	return seed
}

func (s *SessionManager) Join(joiner, id string) (*GameSession, error) {
	s.Lock()
	defer s.Unlock()

	if sess, ok := s.SessionsForPlayer[joiner]; ok {
		errMsg := "player already in game session"
		if sess.GameManager == nil {
			errMsg = "please cancel seek before accepting a game"
		}
		return nil, errors.New(errMsg)
	}
	gs := s.Sessions[id]
	if gs == nil {
		fmt.Println("sessions are", s.Sessions, s.Sessions[id])
		return nil, errors.New("session did not exist")
	}
	gs.Players = append(gs.Players, joiner)
	// Get the game started!

	gs.GameManager = NewGameStateManager(gs.SearchCriteria, gs.Players,
		s.cfg.WordDBServerAddress, id, s.eventsOut, CryptoSeed())
	gs.GameManager.StartGameCountdown()
	// XXX: quit cleanly

	s.SessionsForPlayer[joiner] = gs
	return gs, nil
}

func (s *SessionManager) AllSessions() ([]byte, error) {
	s.Lock()
	defer s.Unlock()

	sessList := []*GameSession{}
	for _, sess := range s.Sessions {
		sessList = append(sessList, sess)
	}
	return json.Marshal(sessList)
}

// Leave destroys a game. For now any player can do it, but only in between rounds.
func (s *SessionManager) Leave(leaver, id string) error {

	s.Lock()
	defer s.Unlock()

	if sess, ok := s.SessionsForPlayer[leaver]; !ok {
		return errors.New("player not in session")
	} else {
		if sess.ID != id {
			return errors.New("unexpected - game session ID did not match!")
		}
		players := sess.GameManager.Players
		err := sess.GameManager.TryDestroy()
		if err != nil {
			return err
		}

		delete(s.Sessions, sess.ID)
		for _, p := range players {
			delete(s.SessionsForPlayer, p)
		}
	}

	return nil

}
