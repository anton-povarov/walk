package main

import (
	"image"
	"os"
	"testing"

	"github.com/nfnt/resize"
)

func TestGraphicHeartAlphaEdges(t *testing.T) {
	f, err := os.Open("img/1.png")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	source, _, err := image.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	b := source.Bounds()
	w, h := b.Dx()*5, b.Dy()*5
	filtered := resize.Resize(uint(w), uint(h), source, resize.Lanczos3)
	got := fitGraphic(source, w, h)
	overshoots := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, blue, a := filtered.At(x, y).RGBA()
			if a == 0 {
				continue
			}
			actual := got.At(x, y)
			ar, ag, ab, _ := actual.RGBA()
			for i, pair := range [][2]uint32{{r, ar}, {g, ag}, {blue, ab}} {
				if pair[0] > a {
					overshoots++
					// Overshoot at a transparent edge should saturate, not wrap
					// around into a dark or differently colored pixel.
					if pair[1]+257 < a {
						t.Errorf("edge (%d,%d) channel %d wrapped: %d, alpha %d", x, y, i, pair[1], a)
						return
					}
				}
			}
		}
	}
	if overshoots == 0 {
		t.Fatal("fixture no longer exercises Lanczos alpha overshoot")
	}
}
