package game

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/domino14/word_db_server/rpc/wordsearcher"
	"github.com/rs/zerolog/log"
	"google.golang.org/protobuf/encoding/protojson"
)

type Status int

const (
	Countdown Status = iota
	Playing
	PermanentlyOver
)

const TotalNumQuestions = 50
const NumSlots = 16
const TickDuration = 1 * time.Second
const OppTickDuration = 3 * time.Second
const InitGameCountdownTime = 2 * time.Second
const NextGameCountdownTime = 10 * time.Second

type GameStateManager struct {
	ID             string
	Status         Status
	timer          *time.Timer
	Boards         []*GameBoard
	Players        []string
	QuestionOffset int
	stop           chan struct{}
	stateChange    chan struct{}
	addToOppQueue  chan *Question
	randSeed       [32]byte
	stateOut       chan []byte
	wdbServer      string
	SearchCriteria []byte
	boardexited    chan int
	exitedboards   []bool
}

type BoardStatus int

const (
	PieceDropping BoardStatus = iota
	PieceAboutToDrop
	PlayerQueueEmpty
)

// State changes are important to keep track of for animation purposes.
type StateChangeType string

const (
	// PieceFall is when a single piece falls one slot
	PieceFall StateChangeType = "piecefall"
	// PieceLand is when a piece lands at the lowest possible point
	PieceLand StateChangeType = "pieceland"
	// StackRise is when our stack goes up, usually because of opponent pieces
	StackRise StateChangeType = "stackrise"
	// StackQueue is when our stack is queued to go up, usually because of opponent pieces
	StackQueue StateChangeType = "stackqueue"
	// FullySolveQuestion is when we solve a question
	FullySolveQuestion StateChangeType = "fullysolvequestion"

	Lost StateChangeType = "lost"
)

// A StateChange should be sent to the display front-end along with the full state.
// This will allow the front-end to animate changes.
type StateChange struct {
	ChangeType    StateChangeType
	PayloadNum    int
	PayloadNum2   int
	PayloadString string
}

type GameBoard struct {
	sync.Mutex

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
	Idx           int
	oppqueueReady bool
	Solved        int
	quitting      bool

	oppQueueChan    chan *Question
	manager         *GameStateManager
	stop            chan struct{}
	status          BoardStatus
	LastStateChange StateChange
}

type Question struct {
	OrigQuestion *wordsearcher.Alphagram
	Whose        int // index in players
	AnswerMap    map[string]bool
}

func (a *Question) populateMap() {
	a.AnswerMap = map[string]bool{}
	for _, answer := range a.OrigQuestion.Words {
		a.AnswerMap[strings.ToLower(answer.Word)] = true
	}
}

func (a *Question) answersLeft() int {
	return len(a.AnswerMap)
}

func NewGameStateManager(searchCriteria []byte, players []string, wdbServer, ID string, stateout chan []byte,
	randseed [32]byte) *GameStateManager {

	gs := &GameStateManager{
		Status:         Countdown,
		stateChange:    make(chan struct{}, 1),
		Players:        players,
		ID:             ID,
		stateOut:       stateout,
		addToOppQueue:  make(chan *Question, 8),
		wdbServer:      wdbServer,
		SearchCriteria: searchCriteria,
		randSeed:       randseed,
		boardexited:    make(chan int),
	}

	return gs
}

func (gs *GameStateManager) start() error {
	// reseed randomizer with the same seed so shuffle is deterministic.
	randomizer := rand.New(rand.NewChaCha8(gs.randSeed))
	gs.exitedboards = make([]bool, len(gs.Players))
	s := wordsearcher.NewQuestionSearcherProtobufClient(gs.wdbServer, &http.Client{})
	sr := &wordsearcher.SearchRequest{}
	err := protojson.Unmarshal(gs.SearchCriteria, sr)
	if err != nil {
		return err
	}

	resp, err := s.Search(context.Background(), sr)
	if err != nil {
		return err
	}

	// start a game

	randomizer.Shuffle(len(resp.Alphagrams),
		func(i, j int) {
			resp.Alphagrams[i], resp.Alphagrams[j] = resp.Alphagrams[j], resp.Alphagrams[i]
		})

	if len(resp.Alphagrams)-gs.QuestionOffset < TotalNumQuestions {
		return errors.New("too few questions left")
	}

	resp.Alphagrams = resp.Alphagrams[gs.QuestionOffset : gs.QuestionOffset+TotalNumQuestions]
	// Re-initialize boards.
	gs.Boards = make([]*GameBoard, len(gs.Players))
	for i := range gs.Players {
		gs.Boards[i] = newGameBoard(i, gs)
	}

	for idx, alph := range resp.Alphagrams {
		whose := idx % 2
		q := &Question{
			OrigQuestion: alph,
			Whose:        whose,
		}
		// It's already an alphagram, but we want to make sure we sort by rune consistently
		// for both guesses and alphagrams.
		q.OrigQuestion.Alphagram = alphagrammize(q.OrigQuestion.Alphagram)
		q.populateMap()
		gs.Boards[whose].Queue = append(gs.Boards[whose].Queue, q)
	}
	gs.QuestionOffset += TotalNumQuestions

	// Actually start game
	for i := range gs.Boards {
		gs.Boards[i].Tick()
	}
	for i := range gs.Boards {
		go gs.Boards[i].loop()
	}

	gs.Status = Playing
	gs.stateChange <- struct{}{}

	return nil
}

func (gs *GameStateManager) TryDestroy() error {
	if gs.Status != Countdown {
		return errors.New("cannot destroy an ongoing game")
	}
	gs.Stop()
	for _, b := range gs.Boards {
		b.Quit()
	}
	return nil
}

func (gs *GameStateManager) StartGameCountdown() {
	// start timer
	gs.timer = time.NewTimer(InitGameCountdownTime)
	go gs.Loop()
}

func (gs *GameStateManager) Guess(username, guess string) error {
	found := false
	for i := range gs.Players {
		if gs.Players[i] == username {
			found = true
			gs.Boards[i].Guess(guess)
			break
		}
	}
	if !found {
		return errors.New("player is not in this game")
	}
	return nil
}

func (gs *GameStateManager) Loop() {
	log.Info().Str("gid", gs.ID).Msg("start game state manager loop")
gloop:
	for {
		select {
		case <-gs.timer.C:
			if gs.Status == Countdown {
				err := gs.start()
				if err != nil {
					log.Err(err).Msg("start-error")
					break gloop
				}
			}

		case alph := <-gs.addToOppQueue:
			opp := 1 - alph.Whose // if 0, then 1, else if 1 then 0
			gs.Boards[opp].oppQueueChan <- alph

		case <-gs.stop:
			break gloop

		case <-gs.stateChange:
			// Send out game state to sockets! Print out, etc. stop the game if needed.
			for i := range gs.Boards {
				gs.Boards[i].Lock()
			}
			gs.stateOut <- gs.Marshal()
			for i := range gs.Boards {
				gs.Boards[len(gs.Boards)-1-i].Unlock()
			}

		case idx := <-gs.boardexited:
			gs.exitedboards[idx] = true
			allquit := true
			for i := range gs.exitedboards {
				if !gs.exitedboards[i] {
					allquit = false
					break
				}
			}
			if allquit {
				gs.timer = time.NewTimer(NextGameCountdownTime)
				gs.Status = Countdown
			} else {
				for i := range gs.Boards {
					if i != idx {
						gs.Boards[i].shouldQuitSoon()
					}
				}
			}
		}
	}
	gs.Status = PermanentlyOver
	gs.stateOut <- gs.Marshal()
	log.Info().Str("gid", gs.ID).Msg("leaving manager loop")

}

func (gs *GameStateManager) Stop() {
	gs.stop <- struct{}{}
}

func newGameBoard(idx int, gs *GameStateManager) *GameBoard {
	gb := &GameBoard{
		Idx:          idx,
		fallerPos:    -1,
		guessEvents:  make(chan string, 5),
		oppQueueChan: make(chan *Question, 5),
		manager:      gs,
		stop:         make(chan struct{}),
	}
	gb.OppQueueTimer = time.NewTimer(0)
	// We can't construct a timer in Go without starting it, so start and stop the opp queue timer.
	if !gb.OppQueueTimer.Stop() {
		<-gb.OppQueueTimer.C
	}

	return gb
}

func (gb *GameBoard) loop() {
	log.Debug().Int("idx", gb.Idx).Msg("start game board loop")
	gb.status = PieceDropping
gbloop:
	for {
		select {
		case <-gb.Timer.C:
			gb.Tick()
			gb.manager.stateChange <- struct{}{}

			gb.Lock()
			if gb.Won || gb.Dead || gb.quitting {
				gb.Unlock()
				break gbloop
			}
			gb.Unlock()

		case <-gb.OppQueueTimer.C:
			// Opp queue is now ready to be added to game board. It will
			// be added as soon as the next piece drops.
			gb.SetOppQueueReady()

		case evt := <-gb.guessEvents:
			log.Debug().Int("idx", gb.Idx).Str("event", evt).Msg("event")
			if gb.handleGuessEvent(evt) {
				gb.manager.stateChange <- struct{}{}
			}
			gb.Lock()
			if gb.Won || gb.Dead {
				gb.Unlock()
				break gbloop
			}
			gb.Unlock()

		case alph := <-gb.oppQueueChan:

			gb.Lock()
			startTimer := false
			if len(gb.OppQueue) == 0 {
				startTimer = true
			}
			gb.OppQueue = append(gb.OppQueue, alph)
			gb.Unlock()

			gb.manager.stateChange <- struct{}{}
			if startTimer {
				gb.OppQueueTimer = time.NewTimer(OppTickDuration)
			}

		case <-gb.stop:
			break gbloop
		}
	}
	gb.OppQueueTimer.Stop()
	gb.Timer.Stop()
	gb.manager.boardexited <- gb.Idx
	log.Debug().Int("idx", gb.Idx).Msg("leave game board loop")

}

func (gb *GameBoard) shouldQuitSoon() {
	gb.Lock()
	gb.quitting = true
	gb.Unlock()
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
	gb.stop <- struct{}{}
	log.Debug().Str("gid", gb.manager.ID).Int("board-idx", gb.Idx).Msg("gb-quitting")
}

// Tick advances the board.
func (gb *GameBoard) Tick() {
	gb.Lock()
	defer gb.Unlock()
	var topOfStack int
	if gb.status == PieceDropping {

		topOfStack = gb.topOfStack()
		if topOfStack == 0 {
			// This player lost - the whole stack is full?
			log.Debug().Msg("stack-full-losing")
			gb.Dead = true
			gb.LastStateChange = StateChange{ChangeType: Lost}
			return
		}

		// Drop faller down.
		if gb.fallerPos == -1 {
			gb.LetGoNextPiece()
		}
		gb.fallerPos++

	} else if gb.status == PieceAboutToDrop || gb.status == PlayerQueueEmpty {

		if gb.oppqueueReady {
			if len(gb.OppQueue) == 0 {
				log.Error().Msg("oppqueue-zero-length-but-ready?")
			} else {
				added := gb.addOppQueue()
				gb.oppqueueReady = false
				if gb.Dead {
					gb.LastStateChange = StateChange{ChangeType: Lost}
					return
				}
				// If we are adding the opp queue contents, we give the player a little breather
				// before we drop the next piece.
				// Note that the status remains "PieceAboutToDrop"
				gb.Timer = time.NewTimer(TickDuration)
				gb.LastStateChange = StateChange{ChangeType: StackRise, PayloadNum: added}

				return
			}

		}
		if len(gb.Queue) == 0 {
			gb.status = PlayerQueueEmpty
			gb.Timer = time.NewTimer(TickDuration)
			return
		} else {
			topOfStack = gb.topOfStack()
			if topOfStack == 0 {
				log.Debug().Msg("abttodrop-stack-full-losing")
				gb.Dead = true
				gb.LastStateChange = StateChange{ChangeType: Lost}
				return
			}
			gb.LetGoNextPiece()
			gb.fallerPos = 0
		}
	}

	if gb.fallerPos == topOfStack-1 {
		// landed naturally.
		gb.LastStateChange = StateChange{ChangeType: PieceLand, PayloadNum: gb.fallerPos, PayloadNum2: gb.fallerPos - 1}

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
		gb.LastStateChange = StateChange{ChangeType: Lost}

		return
	} else {
		// drop piece down a slot, it's still in the air
		if gb.fallerPos > 0 {
			gb.Slots[gb.fallerPos-1], gb.Slots[gb.fallerPos] = gb.Slots[gb.fallerPos], gb.Slots[gb.fallerPos-1]
		}
		gb.LastStateChange = StateChange{ChangeType: PieceFall, PayloadNum: gb.fallerPos, PayloadNum2: gb.fallerPos - 1}

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

func (gb *GameBoard) addOppQueue() int {
	added := 0
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
		added += 1
	}
	return added
}

// RandomWord only used for debugging/etc
func (gb *GameBoard) RandomWord(wrongSometimes bool) string {
	left := []string{}

	for slot, question := range gb.Slots {
		if gb.Slots[slot] == nil {
			continue
		}
		for k := range question.AnswerMap {
			left = append(left, k)
		}
	}
	if len(left) == 0 {
		return ""
	}
	g := rand.IntN(len(left))

	ourguess := left[g]
	if wrongSometimes {
		if rand.Float32() < 0.15 {
			ourguess = alphagrammize(ourguess) // get it wrong
		} else if rand.Float32() < (float32(0.35) - float32(len(left))/100.0) {
			// Don't guess at all
			return ""
		}
	}
	return ourguess
}

func (gb *GameBoard) handleGuessEvent(g string) bool {
	gb.Lock()
	defer gb.Unlock()
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
			gb.LastStateChange = StateChange{ChangeType: Lost}
			return stateChanged
		}
		// Drop item immediately and set short timer for next piece.
		gb.Slots[gb.fallerPos], gb.Slots[topOfStack-1] = gb.Slots[topOfStack-1], gb.Slots[gb.fallerPos]
		gb.LastStateChange = StateChange{ChangeType: PieceLand, PayloadNum: topOfStack - 1, PayloadNum2: gb.fallerPos}
		gb.fallerPos = -1
		gb.status = PieceAboutToDrop
		gb.Timer = time.NewTimer(TickDuration / 4)
		return stateChanged
	}
	if fullySolvedQuestion {
		// The slot X is fully solved. if we solved a question that was meant for us, send it to the opp
		if gb.Slots[fullySolvedSlot].Whose == gb.Idx {
			q := gb.Slots[fullySolvedSlot]
			// Repopulate the answer map for the opponent:
			q.populateMap()
			gb.manager.addToOppQueue <- q
		}
		gb.Slots[fullySolvedSlot] = nil
		gb.Solved++
		gb.LastStateChange = StateChange{ChangeType: FullySolveQuestion, PayloadNum: fullySolvedSlot}

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
	strarr = append(strarr, fmt.Sprintf("Board %d Dead %v Won %v", gb.Idx, gb.Dead, gb.Won))
	// print board.
	strarr = append(strarr, "------------------")
	// reset := "\x1b[0m"
	for _, s := range gb.Slots {
		// color := "\x1b[0;31m" // red
		if s != nil {
			if s.Whose == 1 {
				// color = "\x1b[0;36m" // cyan
			}
			// strarr = append(strarr, fmt.Sprintf("%0s %d %-20s %0s", color, s.answersLeft(), s.OrigQuestion.Alphagram, reset))
			strarr = append(strarr, fmt.Sprintf("| %d %s [p%d]", s.answersLeft(), s.OrigQuestion.Alphagram, s.Whose))
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
	if _, ok := q.AnswerMap[guess]; ok {
		delete(q.AnswerMap, guess)
		partiallySolved = true
	} else {
		if alphagrammize(guess) == strings.ToLower(q.OrigQuestion.Alphagram) {
			// Wrong guess
			wrong = true
		}
	}

	if partiallySolved && len(q.AnswerMap) == 0 {
		fullySolved = true
	}
	return partiallySolved, fullySolved, wrong
}

func (gs *GameStateManager) Printable() string {
	var builder strings.Builder
	if len(gs.Boards) == 0 {
		return "(Uninitialized)"
	}
	builder.WriteString(fmt.Sprintf("GameID: %s\n", gs.ID))
	builder.WriteString(fmt.Sprintf("Question Offset %d\n", gs.QuestionOffset))

	b0 := gs.Boards[0].Printable()
	b1 := gs.Boards[1].Printable()
	for i := 0; i < len(b0); i++ {
		builder.WriteString(fmt.Sprintf("              %-40s          %-45s\n", b0[i], b1[i]))
	}
	return builder.String()
}

func (gs *GameStateManager) Marshal() []byte {
	bts, err := json.Marshal(gs)
	if err != nil {
		panic(err)
	}
	return bts
}

func alphagrammize(w string) string {
	rns := []rune(w)
	sort.Slice(rns, func(i, j int) bool { return rns[i] < rns[j] })
	return string(rns)
}
