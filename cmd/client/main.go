package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/domino14/tetrolith/pkg/game"
)

type Game struct {
	state *game.GameStateManager
}

func (g *Game) Update() error {
	return nil
}

func (g *Game) Draw(screen *ebiten.Image) {
	screen.Fill(ColorConstants["Black"])
	drawBoard(screen, g.state)
}

func (g *Game) Layout(outsideWidth, outsideHeight int) (screenWidth, screenHeight int) {
	s := ebiten.Monitor().DeviceScaleFactor()
	return int(float64(outsideWidth) * s), int(float64(outsideHeight) * s)
	// return 640, 480
}

func main() {
	ebiten.SetWindowSize(1024, 800)
	ebiten.SetWindowTitle("Hello, World!")

	g := &Game{state: &game.GameStateManager{}}
	f, err := os.Open("./testdata/state.json")
	if err != nil {
		log.Fatal(err)
	}
	bts, err := io.ReadAll(f)
	if err != nil {
		log.Fatal(err)
	}
	err = json.Unmarshal(bts, g.state)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(g.state.Printable())

	if err := ebiten.RunGame(g); err != nil {
		log.Fatal(err)
	}
}
