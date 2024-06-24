package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/domino14/word_db_server/rpc/wordsearcher"
	"github.com/lithammer/shortuuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"google.golang.org/protobuf/encoding/protojson"

	"aerolith.org/tetrolith/pkg/config"
	"aerolith.org/tetrolith/pkg/game"
)

const BotTimeInterval = 2 * time.Second

type model struct {
	textInput textinput.Model
	mgr       *game.GameStateManager
	mgrstate  []byte
}

func (m model) Init() tea.Cmd {
	return textinput.Blink
}

type refreshMsg struct {
	newMgrState []byte
}

type botGuessMsg struct{}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {

	// Is it a key press?
	case tea.KeyMsg:

		// Cool, what was the actual key pressed?
		switch msg.Type {

		// These keys should exit the program.
		case tea.KeyCtrlC:
			return m, tea.Quit

		case tea.KeyEnter:
			m.mgr.Boards[0].Guess(strings.TrimSpace(m.textInput.Value()))
			m.textInput.Reset()
			return m, nil
		}

	case refreshMsg:
		m.mgrstate = msg.newMgrState
		return m, nil

	case botGuessMsg:
		gsm := &game.GameStateManager{}
		err := json.Unmarshal(m.mgrstate, gsm)
		if err != nil {
			panic(err)
		}

		if len(gsm.Boards) >= 2 && gsm.Boards[1] != nil {
			guess := gsm.Boards[1].RandomWord(true)
			m.mgr.Boards[1].Guess(guess)
		}
	}
	m.textInput, cmd = m.textInput.Update(msg)

	return m, cmd
}

func (m model) View() string {
	gsm := &game.GameStateManager{}
	err := json.Unmarshal(m.mgrstate, gsm)
	if err != nil {
		panic(err)
	}
	ptbl := gsm.Printable()
	return fmt.Sprintf("%s\n\n%s\n\n", ptbl, m.textInput.View())
}

func initialModel(mgr *game.GameStateManager) model {
	ti := textinput.New()
	ti.Placeholder = "Guess"
	ti.Focus()
	ti.CharLimit = 20
	ti.Width = 20

	return model{
		textInput: ti,
		mgrstate:  []byte("{}"),
		mgr:       mgr,
	}
}

func main() {
	cfg := &config.Config{}
	cfg.Load(os.Args[1:])
	if cfg.WordDBServerAddress == "" {
		panic("need word db server")
	}
	if cfg.Debug {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	} else {
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	}
	log.Debug().Msg("debug logging is on")

	searchparam1 := &wordsearcher.SearchRequest_SearchParam{
		Condition: wordsearcher.SearchRequest_LEXICON,
		Conditionparam: &wordsearcher.SearchRequest_SearchParam_Stringvalue{
			Stringvalue: &wordsearcher.SearchRequest_StringValue{Value: "NWL23"},
		},
	}
	searchparam2 := &wordsearcher.SearchRequest_SearchParam{
		Condition: wordsearcher.SearchRequest_LENGTH,
		Conditionparam: &wordsearcher.SearchRequest_SearchParam_Minmax{
			Minmax: &wordsearcher.SearchRequest_MinMax{Min: 7, Max: 8},
		},
	}

	sr := &wordsearcher.SearchRequest{
		Searchparams: []*wordsearcher.SearchRequest_SearchParam{
			searchparam1, searchparam2}}

	bts, err := protojson.Marshal(sr)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(bts))

	stateOut := make(chan []byte)
	mgr := game.NewGameStateManager(bts, []string{"us", "bot"}, cfg.WordDBServerAddress,
		shortuuid.New(),
		stateOut, game.CryptoSeed())
	p := tea.NewProgram(initialModel(mgr))

	mgr.StartGameCountdown()

	botTimer := time.NewTicker(BotTimeInterval)

	go func() {
		for {
			select {
			case state := <-stateOut:

				p.Send(refreshMsg{state})
			case <-botTimer.C:
				p.Send(botGuessMsg{})
			}
		}
	}()

	if _, err := p.Run(); err != nil {
		fmt.Printf("Alas, there's been an error: %v", err)
		os.Exit(1)
	}

}
