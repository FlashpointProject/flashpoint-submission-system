package linkpreview

import (
	"bytes"
	_ "embed"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strings"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// Embed the existing FPFSS favicon, so deployment needs no browser, font server,
// native graphics libraries or extra filesystem assets.
//
//go:embed logo.png
var logoPNG []byte

// Liberation Sans is the OFL-licensed, Arial-metric-compatible font used for
// the site's sans-serif appearance. Keep the font license with the assets.
//
//go:embed fonts/LiberationSans-Regular.ttf
var regularTTF []byte

//go:embed fonts/LiberationSans-Bold.ttf
var boldTTF []byte
var regularFont = mustFont(regularTTF)
var boldFont = mustFont(boldTTF)
var logo = func() image.Image {
	im, err := png.Decode(bytes.NewReader(logoPNG))
	if err != nil {
		panic(err)
	}
	return im
}()

func mustFont(b []byte) *opentype.Font {
	f, err := opentype.Parse(b)
	if err != nil {
		panic(err)
	}
	return f
}

const scale = 2
const cardWidth = 560
const padding = 18
const tableWidth = cardWidth - 2*padding

var black = color.RGBA{0, 0, 0, 255}
var white = color.RGBA{255, 255, 255, 255}
var border = color.RGBA{203, 203, 203, 255}

type fonts struct{ regular, bold, small, title font.Face }

func newFonts() fonts {
	face := func(f *opentype.Font, size float64) font.Face {
		v, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size * scale, DPI: 72, Hinting: font.HintingFull})
		if err != nil {
			panic(err)
		}
		return v
	}
	return fonts{face(regularFont, 13), face(boldFont, 13), face(regularFont, 12), face(boldFont, 24)}
}
func (f fonts) close() { f.regular.Close(); f.bold.Close(); f.small.Close(); f.title.Close() }

type cell struct {
	x, width int
	label    bool
	lines    []string
}
type row struct {
	height int
	cells  []cell
}
type layout struct {
	titles               []string
	rows                 []row
	headerBottom, height int
}

func measure(f font.Face, s string) int { return font.MeasureString(f, s).Ceil() / scale }

// Wrap measured glyphs, including words without spaces, and visibly truncate at
// a bounded number of lines. User metadata can never expand the image unboundedly.
func wrap(f font.Face, s string, width, maxLines int) []string {
	s = Text(s, 1500)
	if s == "" {
		return []string{"—"}
	}
	remaining := []rune(s)
	lines := []string{}
	for len(remaining) > 0 && len(lines) < maxLines {
		n := 0
		for n < len(remaining) && measure(f, string(remaining[:n+1])) <= width {
			n++
		}
		if n == 0 {
			n = 1
		}
		if n == len(remaining) {
			lines = append(lines, string(remaining))
			break
		}
		if len(lines) == maxLines-1 {
			for n > 0 && measure(f, string(remaining[:n])+"…") > width {
				n--
			}
			lines = append(lines, strings.TrimSpace(string(remaining[:n]))+"…")
			break
		}
		if pos := strings.LastIndex(string(remaining[:n]), " "); pos > 0 {
			n = len([]rune(string(remaining[:n])[:pos]))
		}
		lines = append(lines, strings.TrimSpace(string(remaining[:n])))
		remaining = []rune(strings.TrimSpace(string(remaining[n:])))
	}
	return lines
}

func makeLayout(c *Card, f fonts) layout {
	l := layout{titles: wrap(f.title, c.Title, tableWidth-79, 3)}
	headerHeight := 18 + len(l.titles)*29 + 22
	if headerHeight < 64 {
		headerHeight = 64
	}
	l.headerBottom = padding + headerHeight + 18
	y := l.headerBottom
	if len(c.Statuses) > 0 {
		y += 62 + 12
	}
	for _, fields := range c.Rows {
		r := row{height: 28}
		groupWidth := tableWidth / len(fields)
		for i, field := range fields {
			x := padding + i*groupWidth
			labelWidth := 112
			if c.Kind == "Tag" {
				labelWidth = 130
			}
			lines := 3
			if field.Label == "Description" || field.Label == "Aliases" {
				lines = 5
			}
			cells := []cell{{x, labelWidth, true, wrap(f.bold, field.Label, labelWidth-14, 2)}, {x + labelWidth, groupWidth - labelWidth, false, wrap(f.regular, field.Value, groupWidth-labelWidth-14, lines)}}
			for _, cell := range cells {
				h := len(cell.lines)*18 + 10
				if h > r.height {
					r.height = h
				}
			}
			r.cells = append(r.cells, cells...)
		}
		l.rows = append(l.rows, r)
		y += r.height
	}
	l.height = y + padding
	return l
}

// Dimensions returns the exact PNG dimensions for the Open Graph image tags.
func Dimensions(c *Card) (int, int) {
	f := newFonts()
	defer f.close()
	l := makeLayout(c, f)
	return cardWidth * scale, l.height * scale
}

func PNG(c *Card) ([]byte, error) {
	f := newFonts()
	defer f.close()
	l := makeLayout(c, f)
	im := image.NewRGBA(image.Rect(0, 0, cardWidth*scale, l.height*scale))
	fill := func(x, y, w, h int, col color.Color) {
		draw.Draw(im, image.Rect(x*scale, y*scale, (x+w)*scale, (y+h)*scale), image.NewUniform(col), image.Point{}, draw.Src)
	}
	text := func(x, y int, s string, face font.Face, col color.Color) {
		d := font.Drawer{Dst: im, Src: image.NewUniform(col), Face: face, Dot: fixed.P(x*scale, y*scale+face.Metrics().Ascent.Ceil())}
		d.DrawString(s)
	}
	line := func(x, y, w, h int, col color.Color) { fill(x, y, w, h, col) }
	box := func(x, y, w, h int) {
		line(x, y, w, 1, border)
		line(x, y+h, w, 1, border)
		line(x, y, 1, h, border)
		line(x+w, y, 1, h+1, border)
	}
	fill(0, 0, cardWidth, l.height, white)
	fill(0, 0, cardWidth, 1, color.Gray{68})
	fill(0, l.height-1, cardWidth, 1, color.Gray{68})
	fill(0, 0, 1, l.height, color.Gray{68})
	fill(cardWidth-1, 0, 1, l.height, color.Gray{68})
	xdraw.CatmullRom.Scale(im, image.Rect(padding*scale, padding*scale, (padding+64)*scale, (padding+64)*scale), logo, logo.Bounds(), draw.Over, nil)
	x := padding + 79
	y := padding
	text(x, y, SiteName, f.small, color.Gray{85})
	y += 18
	for _, s := range l.titles {
		text(x, y, s, f.title, black)
		y += 29
	}
	text(x, y+2, c.Kind, f.regular, black)
	y = l.headerBottom
	if len(c.Statuses) > 0 {
		for i, status := range c.Statuses {
			left := padding + i*tableWidth/len(c.Statuses)
			right := padding + (i+1)*tableWidth/len(c.Statuses)
			w := right - left
			head := hex(status.Color)
			fg := color.Color(white)
			if status.Label == "Bot" {
				head = color.RGBA{224, 224, 224, 255}
				fg = black
			}
			if status.Label == "FP" {
				head = hex("#8011a7")
			}
			if status.Label == "F" {
				head = hex("#00ffe1")
				fg = black
			}
			fill(left, y, w, 26, head)
			text(left+(w-measure(f.bold, status.Label))/2, y+5, status.Label, f.bold, fg)
			bg := color.Color(color.RGBA{242, 242, 242, 255})
			fg = black
			if !status.Dot && status.Value != "0" {
				bg = hex(status.Color)
				fg = white
			}
			fill(left, y+26, w, 36, bg)
			if status.Dot {
				cx, cy := (left+w/2)*scale, (y+44)*scale
				radius := 10 * scale
				for py := -radius; py <= radius; py++ {
					for px := -radius; px <= radius; px++ {
						if px*px+py*py <= radius*radius {
							im.Set(cx+px, cy+py, hex(status.Color))
						}
					}
				}
			} else {
				text(left+(w-measure(f.bold, status.Value))/2, y+35, status.Value, f.bold, fg)
			}
			box(left, y, w, 26)
			box(left, y+26, w, 36)
		}
		y += 74
	}
	for _, r := range l.rows {
		for _, c := range r.cells {
			face := f.regular
			if c.label {
				face = f.bold
			}
			top := y + (r.height-len(c.lines)*18)/2
			for _, s := range c.lines {
				tx := c.x + 7
				if c.label {
					tx = c.x + c.width - 7 - measure(face, s)
				}
				text(tx, top, s, face, black)
				top += 18
			}
			box(c.x, y, c.width, r.height)
		}
		y += r.height
	}
	var out bytes.Buffer
	err := png.Encode(&out, im)
	return out.Bytes(), err
}

func hex(s string) color.RGBA {
	var rgb [3]uint8
	for i := 0; i < 3; i++ {
		for _, r := range s[1+i*2 : 3+i*2] {
			rgb[i] *= 16
			if r >= '0' && r <= '9' {
				rgb[i] += uint8(r - '0')
			} else {
				rgb[i] += uint8(r - 'a' + 10)
			}
		}
	}
	return color.RGBA{rgb[0], rgb[1], rgb[2], 255}

}
