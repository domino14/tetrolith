package main

import (
	"bytes"
	"image/color"
	"log"
	"strconv"

	"github.com/domino14/tetrolith/pkg/game"
	"github.com/hajimehoshi/ebiten/examples/resources/fonts"
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"
)

var (
	mplusFaceSource *text.GoTextFaceSource
)

const (
	tileSize      = 32
	tileArcRadius = 3
)

var (
	p1TileColor = color.RGBA{0x44, 0x17, 0xb7, 255}
	p2TileColor = color.RGBA{0xfd, 0xb7, 0x2b, 255}

	p1TileStroke = color.RGBA{0x49, 0x28, 0x89, 255}
	p2TileStroke = color.RGBA{0xa5, 0x77, 0x19, 255}

	p1TextColor = color.White
	p2TextColor = color.Black
)

func init() {
	s, err := text.NewGoTextFaceSource(bytes.NewReader(fonts.ArcadeN_ttf))
	if err != nil {
		log.Fatal(err)
	}
	mplusFaceSource = s
}

func drawPlayerBoard(screen *ebiten.Image, g *game.GameStateManager, bidx int, x, y float64) {
	// vector.DrawFilledRect(screen, float32(x-5), float32(y-5), 300, 550, color.Black, false)
	boardWidth := 300
	boardHeight := 550
	strokeWidth := 2
	vector.StrokeRect(screen, float32(x-5), float32(y-5), float32(boardWidth), float32(boardHeight),
		float32(strokeWidth), ColorConstants["White"], false)
	board := g.Boards[bidx]

	optxt := &text.DrawOptions{}
	optxt.GeoM.Translate(x, y-30)
	optxt.ColorScale.ScaleWithColor(ColorConstants["White"])
	text.Draw(screen, g.Players[bidx], &text.GoTextFace{
		Source: mplusFaceSource,
		Size:   24,
	}, optxt)

	optxt2 := &text.DrawOptions{}
	optxt2.GeoM.Translate(x+190, y-30)
	optxt2.ColorScale.ScaleWithColor(ColorConstants["Blue"])

	text.Draw(screen, "Pts:"+strconv.Itoa(board.Solved), &text.GoTextFace{
		Source: mplusFaceSource,
		Size:   24,
	}, optxt2)

	for idx, slot := range board.Slots {
		if slot == nil {
			continue
		}
		drawAlpha(screen, slot.OrigQuestion.Alphagram, slot.Whose, x, y+float64(idx*(tileSize+2)),
			len(slot.OrigQuestion.Words))
	}

	// Draw the opp queue.
	if len(board.OppQueue) == 0 {
		return
	}
	height := len(board.OppQueue) * (tileSize + 2)
	vector.DrawFilledRect(screen, float32(x-25), float32(y)+float32(boardHeight-height-4),
		15, float32(height-4), ColorConstants["Magenta"], false)
}

func drawBoard(screen *ebiten.Image, g *game.GameStateManager) {
	drawPlayerBoard(screen, g, 0, 100, 80)
	drawPlayerBoard(screen, g, 1, 600, 80)

}

func drawAlpha(screen *ebiten.Image, alpha string, pidx int, x, y float64, nsol int) {
	var bgcolor, textcolor, strokecolor color.Color
	if pidx == 0 {
		bgcolor, textcolor, strokecolor = p1TileColor, p1TextColor, p1TileStroke
	} else {
		bgcolor, textcolor, strokecolor = p2TileColor, p2TextColor, p2TileStroke
	}

	for idx, t := range []rune(alpha) {
		drawNSolChip(screen, x+(tileSize/2), y+(tileSize/2), tileSize/2, nsol)
		tx := x + 5 + float64(tileSize)*float64(idx+1)
		drawTile(screen, string(t), bgcolor, textcolor, strokecolor, tx, y, tileSize, tileArcRadius)
	}
}

var ColorConstants = map[string]color.RGBA{
	"White":   {0xfe, 0xff, 0xff, 0xff},
	"Black":   {0x3e, 0x3f, 0x3a, 0xff},
	"Green":   {0x5e, 0xf3, 0x86, 0xff},
	"Yellow":  {0xd3, 0xe9, 0x48, 0xff},
	"Blue":    {0x60, 0xc0, 0xdc, 0xff},
	"Purple":  {0x72, 0x5e, 0xf3, 0xff},
	"Magenta": {0xe9, 0x5a, 0xd6, 0xff},
}

type ChipAttributes struct {
	color     color.RGBA
	opacity   float64
	textColor color.RGBA
	outline   color.RGBA
}

func getChipAttributes(effectiveNumAnagrams int) ChipAttributes {
	outlineColor := color.RGBA{0x7e, 0x7f, 0x7a, 0xff}
	if effectiveNumAnagrams > 9 {
		effectiveNumAnagrams = 9
	}
	switch effectiveNumAnagrams {
	case 9:
		return ChipAttributes{
			color:     ColorConstants["Black"],
			opacity:   1.0,
			textColor: ColorConstants["White"],
			outline:   outlineColor,
		}
	case 8:
		return ChipAttributes{
			color:     ColorConstants["Black"],
			opacity:   0.65,
			textColor: ColorConstants["White"],
			outline:   outlineColor,
		}
	case 7:
		return ChipAttributes{
			color:     color.RGBA{0x32, 0x5d, 0x88, 0xff},
			opacity:   1.0,
			textColor: ColorConstants["White"],
			outline:   outlineColor,
		}
	case 6:
		return ChipAttributes{
			color:     ColorConstants["Magenta"],
			opacity:   1.0,
			textColor: ColorConstants["White"],
			outline:   outlineColor,
		}
	case 5:
		return ChipAttributes{
			color:     ColorConstants["Green"],
			opacity:   1.0,
			textColor: ColorConstants["Black"],
			outline:   outlineColor,
		}
	case 4:
		return ChipAttributes{
			color:     ColorConstants["Yellow"],
			opacity:   1.0,
			textColor: ColorConstants["Black"],
			outline:   outlineColor,
		}
	case 3:
		return ChipAttributes{
			color:     ColorConstants["Blue"],
			opacity:   1.0,
			textColor: ColorConstants["White"],
			outline:   outlineColor,
		}
	case 2:
		return ChipAttributes{
			color:     ColorConstants["Purple"],
			opacity:   1.0,
			textColor: ColorConstants["White"],
			outline:   outlineColor,
		}
	case 1:
		return ChipAttributes{
			color:     ColorConstants["White"],
			opacity:   1.0,
			textColor: ColorConstants["Black"],
			outline:   outlineColor,
		}
	default:
		return ChipAttributes{
			color:     ColorConstants["White"],
			opacity:   1.0,
			textColor: ColorConstants["Black"],
			outline:   outlineColor,
		}
	}
}

func drawNSolChip(screen *ebiten.Image, cx, cy, radius float64, nsol int) {

	if nsol > 9 {
		nsol = 9
	}
	ca := getChipAttributes(nsol)

	vector.DrawFilledCircle(screen, float32(cx), float32(cy), float32(radius), ca.color, false)
	vector.StrokeCircle(screen, float32(cx), float32(cy), float32(radius), 2,
		ca.outline, false)

	optxt := &text.DrawOptions{}
	optxt.GeoM.Translate(cx-(radius/2), cy-(radius/2))
	optxt.ColorScale.ScaleWithColor(ca.textColor)
	text.Draw(screen, strconv.Itoa(nsol), &text.GoTextFace{
		Source: mplusFaceSource,
		Size:   float64(radius * 1.3),
	}, optxt)

}

func drawTile(screen *ebiten.Image, tch string, tileColor, textColor, tileStrokeColor color.Color,
	x, y, size, arcradius float64) {
	var path vector.Path

	// leave room for outline
	topLeftX, topLeftY := float32(x), float32(y)
	topRightX, topRightY := float32(x+size-1), float32(y)
	bottomRightX, bottomRightY := float32(x+size-1), float32(y+size-1)
	bottomLeftX, bottomLeftY := float32(x), float32(y+size-1)
	radius := float32(arcradius)

	// Create the path for rounded rectangle
	path.MoveTo(topLeftX+radius, topLeftY)
	path.LineTo(topRightX-radius, topRightY)
	path.ArcTo(topRightX, topRightY, topRightX, topRightY+radius, radius)
	path.LineTo(bottomRightX, bottomRightY-radius)
	path.ArcTo(bottomRightX, bottomRightY, bottomRightX-radius, bottomRightY, radius)
	path.LineTo(bottomLeftX+radius, bottomLeftY)
	path.ArcTo(bottomLeftX, bottomLeftY, bottomLeftX, bottomLeftY-radius, radius)
	path.LineTo(topLeftX, topLeftY+radius)
	path.ArcTo(topLeftX, topLeftY, topLeftX+radius, topLeftY, radius)
	path.Close()

	// Fill the path with background color
	vs, is := path.AppendVerticesAndIndicesForFilling(nil, nil)
	img := ebiten.NewImage(1, 1)
	img.Fill(tileColor)
	op := &ebiten.DrawTrianglesOptions{}
	screen.DrawTriangles(vs, is, img, op)

	// Stroke the outline of the tile
	vst, ist := path.AppendVerticesAndIndicesForStroke(nil, nil, &vector.StrokeOptions{Width: 1})
	img = ebiten.NewImage(1, 1)
	img.Fill(tileStrokeColor)
	op = &ebiten.DrawTrianglesOptions{}
	screen.DrawTriangles(vst, ist, img, op)

	optxt := &text.DrawOptions{}
	optxt.GeoM.Translate(float64(x+6), float64(y+5))
	optxt.ColorScale.ScaleWithColor(textColor)
	text.Draw(screen, tch, &text.GoTextFace{
		Source: mplusFaceSource,
		Size:   float64(size - 8),
	}, optxt)

}
