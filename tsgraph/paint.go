package tsgraph

import (
	"image"
	"image/color"
	"image/draw"
	"math"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

// canvas wraps an RGBA image with the few drawing primitives the graph needs.
// Filled shapes go through x/image/vector, which antialiases them the way
// cairo does for rrdtool.
type canvas struct{ *image.RGBA }

func newCanvas(w, h int) canvas { return canvas{image.NewRGBA(image.Rect(0, 0, w, h))} }

func (c canvas) rect(x0, y0, x1, y1 int, col color.RGBA) {
	if x1 <= x0 || y1 <= y0 {
		return
	}
	draw.Draw(c.RGBA, image.Rect(x0, y0, x1, y1).Intersect(c.Bounds()),
		&image.Uniform{col}, image.Point{}, draw.Src)
}

// rectBlend paints a rectangle at the given coverage, so a gridline drawn over
// the data tints it rather than cutting a hole through it.
func (c canvas) rectBlend(x0, y0, x1, y1 int, col color.RGBA, a float64) {
	if a >= 1 {
		c.rect(x0, y0, x1, y1, col)
		return
	}
	r := image.Rect(x0, y0, x1, y1).Intersect(c.Bounds())
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			c.SetRGBA(x, y, blend(col, c.RGBAAt(x, y), a))
		}
	}
}

// fill rasterises a closed polygon.
func (c canvas) fill(pts [][2]float32, col color.RGBA) {
	c.fillAll([][][2]float32{pts}, col)
}

// fillAll rasterises several polygons in one pass, over their bounding box
// rather than the whole image. Both matter: a rasterizer per segment allocates
// hundreds of buffers, and rasterizing at full image size scans every pixel of
// the canvas for a shape that may cover a tenth of it.
func (c canvas) fillAll(polys [][][2]float32, col color.RGBA) {
	minX, minY := float32(math.Inf(1)), float32(math.Inf(1))
	maxX, maxY := float32(math.Inf(-1)), float32(math.Inf(-1))
	n := 0
	for _, pts := range polys {
		if len(pts) < 3 {
			continue
		}
		n++
		for _, p := range pts {
			minX, maxX = min32(minX, p[0]), max32(maxX, p[0])
			minY, maxY = min32(minY, p[1]), max32(maxY, p[1])
		}
	}
	if n == 0 {
		return
	}

	box := image.Rect(int(minX)-1, int(minY)-1, int(maxX)+2, int(maxY)+2).Intersect(c.Bounds())
	if box.Empty() {
		return
	}
	ox, oy := float32(box.Min.X), float32(box.Min.Y)

	r := vector.NewRasterizer(box.Dx(), box.Dy())
	for _, pts := range polys {
		if len(pts) < 3 {
			continue
		}
		r.MoveTo(pts[0][0]-ox, pts[0][1]-oy)
		for _, p := range pts[1:] {
			r.LineTo(p[0]-ox, p[1]-oy)
		}
		r.ClosePath()
	}
	r.Draw(c.RGBA, box, &image.Uniform{col}, image.Point{})
}

func min32(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

func max32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

// stroke draws a polyline of the given width, one quad per segment plus a
// square joint, so corners do not gap at steep changes.
func (c canvas) stroke(pts [][2]float32, w float32, col color.RGBA) {
	if w <= 0 {
		w = 1
	}
	for i := 1; i < len(pts); i++ {
		x0, y0, x1, y1 := pts[i-1][0], pts[i-1][1], pts[i][0], pts[i][1]
		dx, dy := x1-x0, y1-y0
		l := float32(math.Hypot(float64(dx), float64(dy)))
		if l == 0 {
			continue
		}
		nx, ny := -dy/l*w/2, dx/l*w/2
		c.fill([][2]float32{{x0 + nx, y0 + ny}, {x1 + nx, y1 + ny}, {x1 - nx, y1 - ny}, {x0 - nx, y0 - ny}}, col)
		if i+1 < len(pts) {
			c.fill([][2]float32{{x1 - w/2, y1 - w/2}, {x1 + w/2, y1 - w/2},
				{x1 + w/2, y1 + w/2}, {x1 - w/2, y1 + w/2}}, col)
		}
	}
}

// dashedV and dashedH draw dashed lines, blended so they tint the data rather
// than cutting through it.
// w is the line thickness. It has to scale with the image or a line keeps its
// single pixel while everything around it grows, which reads as the whole graph
// washing out.
func (c canvas) dashedV(x, y0, y1 int, on, off, w int, col color.RGBA, a float64) {
	for y := y0; y < y1; y += on + off {
		c.rectBlend(x, y, x+w, min(y+on, y1), col, a)
	}
}

func (c canvas) dashedH(y, x0, x1 int, on, off, w int, col color.RGBA, a float64) {
	for x := x0; x < x1; x += on + off {
		c.rectBlend(x, y, min(x+on, x1), y+w, col, a)
	}
}

func (c canvas) text(f font.Face, s string, x, y int, col color.RGBA) {
	(&font.Drawer{Dst: c.RGBA, Src: &image.Uniform{col}, Face: f, Dot: fixed.P(x, y)}).DrawString(s)
}

// textRotated draws s turned a quarter turn anticlockwise, for the y-axis
// title. It renders flat then transposes, compositing each glyph pixel over
// the destination -- copying the raw RGBA would carry the antialiasing alpha
// through and leave a smear wherever the background is not white.
func (c canvas) textRotated(f font.Face, s string, cx, cy int, col color.RGBA) {
	w := textWidth(f, s)
	h := f.Metrics().Height.Ceil() + 4
	tmp := canvas{image.NewRGBA(image.Rect(0, 0, w, h))}
	tmp.text(f, s, 0, h-4, color.RGBA{0xFF, 0xFF, 0xFF, 0xFF})

	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			_, _, _, a := tmp.At(x, y).RGBA()
			if a == 0 {
				continue
			}
			dx, dy := cx+y-h/2, cy+w/2-x
			if !(image.Point{dx, dy}).In(c.Bounds()) {
				continue
			}
			c.Set(dx, dy, blend(col, c.RGBAAt(dx, dy), float64(a)/0xFFFF))
		}
	}
}

// blend mixes src over dst at the given coverage.
func blend(src, dst color.RGBA, a float64) color.RGBA {
	mix := func(s, d uint8) uint8 { return uint8(float64(s)*a + float64(d)*(1-a) + 0.5) }
	return color.RGBA{mix(src.R, dst.R), mix(src.G, dst.G), mix(src.B, dst.B), 0xFF}
}

func textWidth(f font.Face, s string) int { return font.MeasureString(f, s).Round() }
