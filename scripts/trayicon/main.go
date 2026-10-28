// Command trayicon draws DSDock's menu bar icon: a whale in black on
// transparent, which macOS recolours for light and dark menu bars (a
// "template" image). Run: go run ./scripts/trayicon internal/desktop/tray.png
package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

const size = 44 // 22 pt at 2x

// inside reports whether a point (in a 0..1 square) is part of the whale.
func inside(x, y float64) bool {
	// body: a wide ellipse, flatter at the bottom
	bx, by := (x-0.46)/0.36, (y-0.60)/0.20
	if bx*bx+by*by <= 1 && y <= 0.76 {
		return true
	}
	// tail: two lobes rising to the right
	if x > 0.74 && x < 0.95 {
		t := (x - 0.74) / 0.21
		top := 0.55 - 0.22*t
		if y > top && y < 0.62-0.04*t {
			return true
		}
	}
	// spout: three short strokes above the head
	for _, s := range [][4]float64{{0.30, 0.38, 0.30, 0.14}, {0.29, 0.33, 0.17, 0.19}, {0.31, 0.33, 0.43, 0.19}} {
		if seg(x, y, s[0], s[1], s[2], s[3]) < 0.024 {
			return true
		}
	}
	return false
}

func seg(px, py, ax, ay, bx, by float64) float64 {
	dx, dy := bx-ax, by-ay
	t := math.Max(0, math.Min(1, ((px-ax)*dx+(py-ay)*dy)/(dx*dx+dy*dy)))
	return math.Hypot(px-(ax+t*dx), py-(ay+t*dy))
}

func main() {
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	const ss = 4 // supersampling for smooth edges
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			n := 0
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					x := (float64(px) + (float64(sx)+0.5)/ss) / size
					y := (float64(py) + (float64(sy)+0.5)/ss) / size
					// the eye is a hole in the body
					if inside(x, y) && math.Hypot(x-0.24, y-0.56) > 0.035 {
						n++
					}
				}
			}
			img.Set(px, py, color.NRGBA{0, 0, 0, uint8(255 * n / (ss * ss))})
		}
	}
	f, err := os.Create(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}
