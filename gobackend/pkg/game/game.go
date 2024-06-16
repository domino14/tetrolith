package game

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/domino14/word_db_server/rpc/wordsearcher"
	"github.com/rs/zerolog/log"
	"google.golang.org/protobuf/encoding/protojson"
)

type Status int

const (
	Countdown Status = iota
	Playing
)

const TotalNumQuestions = 50
const NumSlots = 15
const TickDuration = 1 * time.Second
const OppTickDuration = 3 * time.Second
const GameCountdownTime = 2 * time.Second

type GameStateManager struct {
	ID             string
	Status         Status
	timer          *time.Timer
	Boards         [2]*GameBoard
	questionOffset int
	questions      []*wordsearcher.Alphagram
	stop           chan struct{}
	stateChange    chan struct{}
	addToOppQueue  chan *Question
	randSeed       int
	randomizer     *rand.Rand
	stateOut       chan string
}

type BoardStatus int

const (
	PieceDropping BoardStatus = iota
	PieceAboutToDrop
)

type GameBoard struct {
	// Slots go from top to bottom.
	Slots [NumSlots]*Question // alphagrams
	// Each board should have its own independent timer
	Timer         *time.Timer `json:"-"`
	Queue         []*Question // One queue of alphagrams per player from the top
	OppQueue      []*Question // Queue of alphagrams that were sent over by the opp
	fallerPos     int
	OppQueueTimer *time.Timer `json:"-"` // Separate timer for the queued up opponent's racks
	guessEvents   chan string
	Dead          bool
	Won           bool
	idx           int
	oppqueueReady bool
	Solved        int

	oppQueueChan chan *Question
	manager      *GameStateManager
	stop         chan struct{}
	status       BoardStatus
}

type Question struct {
	origquestion *wordsearcher.Alphagram
	whose        int // index in players
	answermap    map[string]bool
}

func (a *Question) populateMap() {
	a.answermap = map[string]bool{}
	for _, answer := range a.origquestion.Words {
		a.answermap[strings.ToLower(answer.Word)] = true
	}
}

func (a *Question) answersLeft() int {
	return len(a.answermap)
}

func NewGameStateManager(searchCriteria, wdbServer, ID string, stateout chan string, randseed [32]byte) (*GameStateManager, error) {
	s := wordsearcher.NewQuestionSearcherProtobufClient(wdbServer, &http.Client{})
	sr := &wordsearcher.SearchRequest{}
	err := protojson.Unmarshal([]byte(searchCriteria), sr)
	if err != nil {
		return nil, err
	}

	resp, err := s.Search(context.Background(), sr)
	if err != nil {
		return nil, err
	}

	if len(resp.Alphagrams) < TotalNumQuestions {
		return nil, errors.New("too few questions")
	}
	// split them equally
	if len(resp.Alphagrams)%2 == 1 {
		resp.Alphagrams = resp.Alphagrams[:len(resp.Alphagrams)-1]
	}

	// start a game
	gs := &GameStateManager{
		Status:        Countdown,
		questions:     resp.Alphagrams,
		stateChange:   make(chan struct{}, 1),
		ID:            ID,
		stateOut:      stateout,
		addToOppQueue: make(chan *Question, 8),
	}
	gs.randomizer = rand.New(rand.NewChaCha8(randseed))
	gs.Boards[0] = newGameBoard(0, gs)
	gs.Boards[1] = newGameBoard(1, gs)

	gs.randomizer.Shuffle(len(resp.Alphagrams),
		func(i, j int) {
			resp.Alphagrams[i], resp.Alphagrams[j] = resp.Alphagrams[j], resp.Alphagrams[i]
		})

	for idx, alph := range resp.Alphagrams[:TotalNumQuestions] {
		whose := idx % 2
		q := &Question{
			origquestion: alph,
			whose:        whose,
		}
		// It's already an alphagram, but we want to make sure we sort by rune consistently
		// for both guesses and alphagrams.
		q.origquestion.Alphagram = alphagrammize(q.origquestion.Alphagram)
		q.populateMap()
		gs.Boards[whose].Queue = append(gs.Boards[whose].Queue, q)
	}

	gs.startGameCountdown()

	return gs, nil
}

func (gs *GameStateManager) startGameCountdown() {
	// start timer
	gs.timer = time.NewTimer(GameCountdownTime)
	go gs.Loop()
}

func (gs *GameStateManager) Loop() {
	log.Debug().Str("id", gs.ID).Msg("start game state manager loop")
gloop:
	for {
		select {
		case <-gs.timer.C:
			log.Debug().Str("id", gs.ID).Msg("timer tick")
			if gs.Status == Countdown {
				// Actually start game
				gs.Boards[0].Tick()
				gs.Boards[1].Tick()

				go gs.Boards[0].loop()
				go gs.Boards[1].loop()

				gs.Status = Playing
				gs.stateChange <- struct{}{}
			}

		case alph := <-gs.addToOppQueue:
			opp := 1 - alph.whose // if 0, then 1, else if 1 then 0
			gs.Boards[opp].oppQueueChan <- alph

		case <-gs.stop:
			break gloop

		case <-gs.stateChange:
			// Send out game state to sockets! Print out, etc. stop the game if needed.
			gs.stateOut <- gs.Printable()
			if gs.Boards[0].Won || gs.Boards[0].Dead || gs.Boards[1].Won || gs.Boards[1].Dead {
				gs.Boards[0].Quit()
				gs.Boards[1].Quit()
				break gloop
			}
		}
	}
	log.Info().Str("id", gs.ID).Msg("leaving manager loop")

}

func newGameBoard(idx int, gs *GameStateManager) *GameBoard {
	gb := &GameBoard{
		idx:          idx,
		fallerPos:    -1,
		guessEvents:  make(chan string, 5),
		oppQueueChan: make(chan *Question, 5),
		manager:      gs,
	}
	gb.OppQueueTimer = time.NewTimer(0)
	// We can't construct a timer in Go without starting it, so start and stop the opp queue timer.
	if !gb.OppQueueTimer.Stop() {
		<-gb.OppQueueTimer.C
	}

	return gb
}

func (gb *GameBoard) loop() {
	log.Debug().Int("idx", gb.idx).Msg("start game board loop")
	gb.status = PieceDropping
gbloop:
	for {
		select {
		case <-gb.Timer.C:
			gb.Tick()
			gb.manager.stateChange <- struct{}{}

		case <-gb.OppQueueTimer.C:
			// Opp queue is now ready to be added to game board. It will
			// be added as soon as the next piece drops.
			gb.SetOppQueueReady()

		case evt := <-gb.guessEvents:
			log.Debug().Int("idx", gb.idx).Str("event", evt).Msg("event")
			if gb.handleGuessEvent(evt) {
				gb.manager.stateChange <- struct{}{}
			}

		case alph := <-gb.oppQueueChan:
			startTimer := false
			if len(gb.OppQueue) == 0 {
				startTimer = true
			}
			gb.OppQueue = append(gb.OppQueue, alph)
			gb.manager.stateChange <- struct{}{}
			if startTimer {
				gb.OppQueueTimer = time.NewTimer(OppTickDuration)
			}

		case <-gb.stop:
			break gbloop
		}
	}
	log.Debug().Int("idx", gb.idx).Msg("leave game board loop")

}

// topOfStack is the topmost slot idx that is occupied (or, if the board is empty, NumSlots)
// Do NOT count the current faller.
func (gb *GameBoard) topOfStack() int {
	for i := 0; i < NumSlots; i++ {
		if gb.Slots[i] != nil && i != gb.fallerPos {
			return i
		}
	}
	return NumSlots
}

func (gb *GameBoard) Quit() {
	gb.OppQueueTimer.Stop()
	gb.Timer.Stop()
	gb.stop <- struct{}{}
}

// Tick advances the board.
func (gb *GameBoard) Tick() {
	var topOfStack int
	if gb.status == PieceDropping {

		topOfStack = gb.topOfStack()
		if topOfStack == 0 {
			// This player lost - the whole stack is full?
			log.Debug().Msg("stack-full-losing")
			gb.Dead = true
			return
		}

		// Drop faller down.
		if gb.fallerPos == -1 {
			gb.LetGoNextPiece()
		}
		gb.fallerPos++

	} else if gb.status == PieceAboutToDrop {

		if gb.oppqueueReady {
			if len(gb.OppQueue) == 0 {
				log.Error().Msg("oppqueue-zero-length-but-ready?")
			} else {
				gb.addOppQueue()
				gb.oppqueueReady = false
				if gb.Dead {
					return
				}
				// If we are adding the opp queue contents, we give the player a little breather
				// before we drop the next piece.
				// Note that the status remains "PieceAboutToDrop"
				gb.Timer = time.NewTimer(TickDuration)
				return
			}

		}
		topOfStack = gb.topOfStack()
		if topOfStack == 0 {
			log.Debug().Msg("abttodrop-stack-full-losing")
			gb.Dead = true
			return
		}
		gb.LetGoNextPiece()
		gb.fallerPos = 0
	}

	if gb.fallerPos == topOfStack-1 {
		// landed naturally.
		if gb.fallerPos > 0 {
			gb.Slots[gb.fallerPos-1], gb.Slots[gb.fallerPos] = gb.Slots[gb.fallerPos], gb.Slots[gb.fallerPos-1]
		}
		// Piece landed.
		// If we are at the very top, give a bit of a more lenient pause to the player.
		tickDuration := TickDuration / 4
		if gb.fallerPos == 0 {
			tickDuration = TickDuration
		}

		gb.fallerPos = -1
		// if piece lands naturally, wait a beat to bring down the next piece.
		gb.status = PieceAboutToDrop
		gb.Timer = time.NewTimer(tickDuration)
		return
	} else if gb.fallerPos == 0 && topOfStack == 0 {
		// Player lost
		log.Debug().Msg("no-space-for-faller-losing")
		gb.Dead = true
		return
	} else {
		// drop piece down a slot, it's still in the air
		if gb.fallerPos > 0 {
			gb.Slots[gb.fallerPos-1], gb.Slots[gb.fallerPos] = gb.Slots[gb.fallerPos], gb.Slots[gb.fallerPos-1]
		}
	}

	// start next timer
	gb.status = PieceDropping
	gb.Timer = time.NewTimer(TickDuration)
}

// LetGoNextPiece lets go the next alphagram, i.e., starts it falling.
func (gb *GameBoard) LetGoNextPiece() bool {
	if len(gb.Queue) > 0 {
		nextq := gb.Queue[len(gb.Queue)-1]
		gb.Queue = gb.Queue[:len(gb.Queue)-1]
		gb.Slots[0] = nextq
		return true
	}
	return false
}

func (gb *GameBoard) SetOppQueueReady() {
	gb.oppqueueReady = true
}

func (gb *GameBoard) addOppQueue() {
	for len(gb.OppQueue) > 0 {

		nextq := gb.OppQueue[0]
		gb.OppQueue = gb.OppQueue[1:]
		// Shift everything up and insert the queued item at the bottom
		for i := 1; i < len(gb.Slots); i++ {
			gb.Slots[i], gb.Slots[i-1] = gb.Slots[i-1], gb.Slots[i]
		}
		gb.Slots[len(gb.Slots)-1] = nextq
		// The top slot is filled up, and the opp queue still has words in it. GG.
		if gb.Slots[0] != nil && len(gb.OppQueue) > 0 {
			log.Debug().Msg("oppqueue-too-full-losing")
			gb.Dead = true
		}
	}
}

// GuessRandomWord only used for debugging/etc
func (gb *GameBoard) GuessRandomWord() {
	left := []string{}

	for slot, question := range gb.Slots {
		if gb.Slots[slot] == nil {
			continue
		}
		for k := range question.answermap {
			left = append(left, k)
		}
	}
	if len(left) == 0 {
		return
	}
	g := rand.IntN(len(left))

	ourguess := left[g]
	if rand.Float32() < 0.15 {
		ourguess = alphagrammize(ourguess) // get it wrong
	} else if rand.Float32() < 0.45 {
		// Don't guess at all
		return
	}
	gb.guessEvents <- ourguess
}

func (gb *GameBoard) handleGuessEvent(g string) bool {
	// for loop is fast and fine right?
	g = strings.ToLower(strings.TrimSpace(g))

	partiallySolved := false
	fullySolvedQuestion := false
	gotWrong := false
	fullySolvedSlot := -1
	madePunishableMistake := false
	stateChanged := false

	for slot, question := range gb.Slots {
		if gb.Slots[slot] == nil {
			continue
		}
		partiallySolved, fullySolvedQuestion, gotWrong = solveQuestion(question, g)
		if fullySolvedQuestion {
			fullySolvedSlot = slot
		}
		if partiallySolved {
			stateChanged = true
			break
		}
		if gotWrong && slot == gb.fallerPos {
			stateChanged = true
			madePunishableMistake = true
		}
	}
	if !partiallySolved && madePunishableMistake {
		// if our guess didn't even partially solve anything, then the user
		// made a mistake. Drop the current piece and bring up the next one
		gb.Timer.Stop()
		topOfStack := gb.topOfStack()
		if topOfStack == 0 {
			// This shouldn't happen, because the piece would not have dropped?
			log.Error().Msg("badcondition-top-of-stack-0")
			gb.Dead = true
			return stateChanged
		}
		// Drop item immediately and set short timer for next piece.
		gb.Slots[gb.fallerPos], gb.Slots[topOfStack-1] = gb.Slots[topOfStack-1], gb.Slots[gb.fallerPos]
		gb.fallerPos = -1
		gb.status = PieceAboutToDrop
		gb.Timer = time.NewTimer(TickDuration / 4)
		return stateChanged
	}
	if fullySolvedQuestion {
		// The slot X is fully solved. if we solved a question that was meant for us, send it to the opp
		if gb.Slots[fullySolvedSlot].whose == gb.idx {
			q := gb.Slots[fullySolvedSlot]
			// Repopulate the answer map for the opponent:
			q.populateMap()
			gb.manager.addToOppQueue <- q
		}
		gb.Slots[fullySolvedSlot] = nil
		gb.Solved++

		if gb.fallerPos == fullySolvedSlot {
			// If we solved the faller just return now. Set short timer for next piece.
			gb.fallerPos = -1
			gb.status = PieceAboutToDrop
			gb.Timer = time.NewTimer(TickDuration / 4)
			return stateChanged
		}
		// Otherwise, shift some items downwards

		// Start at any items directly on top of item we just solved.
		lastSlot := fullySolvedSlot - 1
		for lastSlot > 0 && gb.Slots[lastSlot] != nil && lastSlot != gb.fallerPos {
			gb.Slots[lastSlot], gb.Slots[lastSlot+1] = gb.Slots[lastSlot+1], gb.Slots[lastSlot]
			lastSlot--
		}

		// Check if everything is fully solved.
		if len(gb.Queue) == 0 {
			// Purposefully not checking if the opp queue is empty.
			weWon := true
			for i := range gb.Slots {
				if gb.Slots[i] != nil {
					weWon = false
				}
			}
			if weWon {
				gb.Won = true
			}
		}
	}
	return stateChanged
}

func (gb *GameBoard) Guess(guess string) {
	gb.guessEvents <- guess
}

func (gb *GameBoard) Printable() []string {
	strarr := []string{}

	strarr = append(strarr, "_____________________")
	strarr = append(strarr, fmt.Sprintf("Board %v Dead %v Won %v", gb.idx, gb.Dead, gb.Won))
	// print board.
	strarr = append(strarr, "------------------")
	// reset := "\x1b[0m"
	for _, s := range gb.Slots {
		// color := "\x1b[0;31m" // red
		if s != nil {
			if s.whose == 1 {
				// color = "\x1b[0;36m" // cyan
			}
			// strarr = append(strarr, fmt.Sprintf("%0s %d %-20s %0s", color, s.answersLeft(), s.origquestion.Alphagram, reset))
			strarr = append(strarr, fmt.Sprintf("| %d %s [p%d]", s.answersLeft(), s.origquestion.Alphagram, s.whose))
		} else {
			strarr = append(strarr, "|                 |")
		}
	}
	strarr = append(strarr, "------------------")
	strarr = append(strarr, "")
	strarr = append(strarr, fmt.Sprintf("Opp queue: %d", len(gb.OppQueue)))
	strarr = append(strarr, fmt.Sprintf("Our queue: %d", len(gb.Queue)))
	strarr = append(strarr, fmt.Sprintf("Solved total: %d", gb.Solved))
	strarr = append(strarr, "_____________________")
	return strarr
}

func solveQuestion(q *Question, guess string) (bool, bool, bool) {
	fullySolved := false
	partiallySolved := false
	wrong := false
	if _, ok := q.answermap[guess]; ok {
		delete(q.answermap, guess)
		partiallySolved = true
	} else {
		if alphagrammize(guess) == strings.ToLower(q.origquestion.Alphagram) {
			// Wrong guess
			wrong = true
		}
	}

	if partiallySolved && len(q.answermap) == 0 {
		fullySolved = true
	}
	return partiallySolved, fullySolved, wrong
}

func (gs *GameStateManager) Printable() string {
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("ID: %s", gs.ID))
	b0 := gs.Boards[0].Printable()
	b1 := gs.Boards[1].Printable()
	for i := 0; i < len(b0); i++ {
		builder.WriteString(fmt.Sprintf("%-40s          %-45s\n", b0[i], b1[i]))
	}
	return builder.String()
}

func alphagrammize(w string) string {
	rns := []rune(w)
	sort.Slice(rns, func(i, j int) bool { return rns[i] < rns[j] })
	return string(rns)
}
