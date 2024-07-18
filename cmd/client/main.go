package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/color"
	"io"
	"log"
	"math"
	"os"

	"github.com/ebitenui/ebitenui"
	"github.com/ebitenui/ebitenui/image"
	"github.com/ebitenui/ebitenui/widget"
	"github.com/golang/freetype/truetype"
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"

	"github.com/domino14/tetrolith/pkg/game"
)

const screenWidth = 1024
const screenHeight = 800

const queuePulseDuration = 3.0 // seconds

type Game struct {
	ui         *ebitenui.UI
	guessInput *widget.TextInput

	state      *game.GameStateManager
	fontSource *text.GoTextFaceSource
	counter    int
}

func interpolateColor(t float64, start, end color.RGBA) color.RGBA {
	r := uint8(float64(start.R)*(1-t) + float64(end.R)*t)
	g := uint8(float64(start.G)*(1-t) + float64(end.G)*t)
	b := uint8(float64(start.B)*(1-t) + float64(end.B)*t)
	a := uint8(float64(start.A)*(1-t) + float64(end.A)*t)
	return color.RGBA{r, g, b, a}
}

func (g *Game) Update() error {
	// update the UI
	// Additional keys to manage focus
	if inpututil.IsKeyJustPressed(ebiten.KeyPageUp) {
		g.ui.ChangeFocus(widget.FOCUS_PREVIOUS)
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyPageDown) {
		g.ui.ChangeFocus(widget.FOCUS_NEXT)
	}

	if inpututil.IsKeyJustPressed(ebiten.KeyEnd) {
		if g.ui.GetFocusedWidget() == g.guessInput {
			fmt.Println("standardTextInput selected")
		}
	}
	g.ui.Update()
	g.counter++
	return nil
}

func (g *Game) Draw(screen *ebiten.Image) {
	screen.Fill(ColorConstants["Black"])

	t := float64(g.counter) / ebiten.DefaultTPS / queuePulseDuration
	t = t - math.Floor(t) // Ensure t is in the range [0, 1)

	if t > 0.5 {
		t = 1 - t
	}
	t *= 2
	startColor := color.RGBA{0xf4, 0xb0, 0xeb, 255}
	endColor := color.RGBA{255, 0, 0, 255}
	queueColor := interpolateColor(t, startColor, endColor)

	drawBoard(screen, g.state, g.fontSource, queueColor)
	g.ui.Draw(screen)
}

func (g *Game) Layout(outsideWidth, outsideHeight int) (screenWidth, screenHeight int) {
	s := ebiten.Monitor().DeviceScaleFactor()
	return int(float64(outsideWidth) * s), int(float64(outsideHeight) * s)
	// return 640, 480
}

func loadFont(size float64) (font.Face, error) {
	ttfFont, err := truetype.Parse(goregular.TTF)
	if err != nil {
		return nil, err
	}

	return truetype.NewFace(ttfFont, &truetype.Options{
		Size:    size,
		DPI:     72,
		Hinting: font.HintingFull,
	}), nil
}

func main() {
	ebiten.SetWindowSize(screenWidth, screenHeight)
	ebiten.SetWindowTitle("Hello, World!")

	g := &Game{state: &game.GameStateManager{}}

	// load images for button states: idle, hover, and pressed
	// buttonImage, _ := loadButtonImage()
	// load the font
	face, _ := loadFont(20)
	g.fontSource, _ = text.NewGoTextFaceSource(bytes.NewReader(goregular.TTF))

	// construct a new container that serves as the root of the UI hierarchy
	rootContainer := widget.NewContainer(
		widget.ContainerOpts.Layout(widget.NewAnchorLayout()),
	)

	g.guessInput = widget.NewTextInput(
		widget.TextInputOpts.WidgetOpts(
			widget.WidgetOpts.LayoutData(widget.AnchorLayoutData{
				HorizontalPosition: widget.AnchorLayoutPositionStart,
				VerticalPosition:   widget.AnchorLayoutPositionEnd,
				StretchHorizontal:  false,
				StretchVertical:    false,
				Padding:            widget.NewInsetsSimple(95),
			}),
			widget.WidgetOpts.MinSize(300, 35),
		),

		// Set the keyboard type when opened on mobile devices.
		// widget.TextInputOpts.MobileInputMode(jsUtil.TEXT),

		//Set the Idle and Disabled background image for the text input
		//If the NineSlice image has a minimum size, the widget will use that or
		// widget.WidgetOpts.MinSize; whichever is greater
		widget.TextInputOpts.Image(&widget.TextInputImage{
			Idle:     image.NewNineSliceColor(color.NRGBA{R: 100, G: 100, B: 100, A: 255}),
			Disabled: image.NewNineSliceColor(color.NRGBA{R: 100, G: 100, B: 100, A: 255}),
		}),

		//Set the font face and size for the widget
		widget.TextInputOpts.Face(face),

		//Set the colors for the text and caret
		widget.TextInputOpts.Color(&widget.TextInputColor{
			Idle:          color.NRGBA{254, 255, 255, 255},
			Disabled:      color.NRGBA{R: 200, G: 200, B: 200, A: 255},
			Caret:         color.NRGBA{254, 255, 255, 255},
			DisabledCaret: color.NRGBA{R: 200, G: 200, B: 200, A: 255},
		}),

		//Set how much padding there is between the edge of the input and the text
		widget.TextInputOpts.Padding(widget.NewInsetsSimple(5)),

		//Set the font and width of the caret
		widget.TextInputOpts.CaretOpts(
			widget.CaretOpts.Size(face, 2),
		),

		//This text is displayed if the input is empty
		widget.TextInputOpts.Placeholder("Guess"),
		widget.TextInputOpts.ClearOnSubmit(true),

		//This is called when the user hits the "Enter" key.
		//There are other options that can configure this behavior
		widget.TextInputOpts.SubmitHandler(func(args *widget.TextInputChangedEventArgs) {
			fmt.Println("Text Submitted: ", args.InputText)
		}),

		//This is called whenver there is a change to the text
		widget.TextInputOpts.ChangedHandler(func(args *widget.TextInputChangedEventArgs) {
			fmt.Println("Text Changed: ", args.InputText)
		}),
	)

	rootContainer.AddChild(g.guessInput)
	// w, h := 100, 50

	// construct the UI
	ui := ebitenui.UI{
		Container: rootContainer,
	}
	g.ui = &ui

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
