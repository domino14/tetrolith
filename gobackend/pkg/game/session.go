package game

import (
	"crypto/rand"
	"errors"

	"aerolith.org/tetrolith/pkg/config"
	"github.com/lithammer/shortuuid"
)

// a game session is a single instance of a game being played.
type GameSession struct {
	Players        []string // first one is the seeker
	ID             string   // game ID for URL
	ListName       string
	SearchCriteria string // JSON representation of list search criteria
	Lexicon        string
	GameManager    *GameStateManager
}

type SessionManager struct {
	Sessions          map[string]*GameSession // map of ID to session
	SessionsForPlayer map[string]*GameSession
	cfg               *config.Config
}

func NewSessionManager(cfg *config.Config) *SessionManager {
	return &SessionManager{
		Sessions:          make(map[string]*GameSession),
		SessionsForPlayer: make(map[string]*GameSession),
		cfg:               cfg,
	}
}

func (s *SessionManager) Seek(seeker, listname, lexicon, searchcriteria string) (*GameSession, error) {
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
		Lexicon:        lexicon,
		SearchCriteria: searchcriteria,
	}
	s.Sessions[gs.ID] = gs
	s.SessionsForPlayer[seeker] = gs
	return gs, nil
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
	if s, ok := s.SessionsForPlayer[joiner]; ok {
		errMsg := "player already in game session"
		if s.GameManager == nil {
			errMsg = "please cancel seek before accepting a game"
		}
		return nil, errors.New(errMsg)
	}
	gs := s.Sessions[id]
	if gs == nil {
		return nil, errors.New("session did not exist")
	}
	gs.Players = append(gs.Players, id)
	// Get the game started!
	var err error

	// fix me, nil channel should be socket or somethng

	gs.GameManager, err = NewGameStateManager(gs.SearchCriteria, s.cfg.WordDBServerAddress, id, nil, CryptoSeed())
	if err != nil {
		return nil, err
	}

	s.SessionsForPlayer[joiner] = gs
	return gs, nil
}
