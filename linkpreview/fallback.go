package linkpreview

import (
	"image"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

// fallbackFace draws each rune with the first face that has a glyph for it, so
// Latin text keeps Liberation Sans' metrics while CJK and other scripts missing
// from it still render. Metrics come from the first face to keep baselines and
// line heights identical to the Latin-only layout.
type fallbackFace []font.Face

func (f fallbackFace) pick(r rune) font.Face {
	for _, face := range f {
		if _, ok := face.GlyphAdvance(r); ok {
			return face
		}
	}
	return f[0]
}

func (f fallbackFace) Glyph(dot fixed.Point26_6, r rune) (image.Rectangle, image.Image, image.Point, fixed.Int26_6, bool) {
	return f.pick(r).Glyph(dot, r)
}

func (f fallbackFace) GlyphBounds(r rune) (fixed.Rectangle26_6, fixed.Int26_6, bool) {
	return f.pick(r).GlyphBounds(r)
}

func (f fallbackFace) GlyphAdvance(r rune) (fixed.Int26_6, bool) {
	return f.pick(r).GlyphAdvance(r)
}

// Kerning only applies between glyphs of the same font.
func (f fallbackFace) Kern(r0, r1 rune) fixed.Int26_6 {
	if face := f.pick(r0); face == f.pick(r1) {
		return face.Kern(r0, r1)
	}
	return 0
}

func (f fallbackFace) Metrics() font.Metrics { return f[0].Metrics() }

func (f fallbackFace) Close() error {
	for _, face := range f {
		face.Close()
	}
	return nil
}
