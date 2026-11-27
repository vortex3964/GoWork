// Package coffee renders the /coffee animation as a TUI-safe overlay. It
// paints only the mug and its steam inside the existing frame: no screen
// clearing, no cursor movement/hiding, and no alternate-screen switches, so
// it composes with the host application instead of fighting it.
package coffee

import (
	"fmt"
	"image/color"
	"math"
	"math/rand"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"GoWork/Tui/Style"
)

// The mug art comes from assets/coffee.go. Rows above steamRows are the
// original braille steam; the rows below are the mug itself.
const art = `⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⡼⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⣼⠇⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠰⡏⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢰⡇⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢸⣷⡀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢿⣷⡀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢀⠀⠈⢻⣿⣄⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠈⢆⠀⠀⠙⣿⣆⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢧⠀⠀⠘⢿⣇⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⣸⡆⠀⠀⠘⣿⡀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⣰⣿⠃⠀⠀⠀⣿⠇⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⣼⣿⠃⠀⠀⠀⠀⡿⠁⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⣿⡏⣀⣀⣀⠀⡜⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⣀⡤⠤⠒⠒⠋⠉⠉⠻⣧⠀⠀⠀⠈⠉⠁⠀⠀⠀⠢⢄⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⣾⣿⠀⠀⠀⠀⣀⣀⣀⣀⣤⣽⣦⣄⣀⣀⣀⣀⠀⠀⠀⠀⢹⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⣿⣿⣿⠷⠾⠿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⡿⠶⠚⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⢿⣿⡏⠀⠀⠀⠀⠀⠀⠈⠉⠉⠉⠉⠉⠉⠀⠀⠀⠀⠀⠀⠀⣸⠛⠻⣷⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠸⣿⣧⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢀⠃⠀⢠⣿⠇⠀⠀
⠀⠀⠀⠀⠀⠀⠀⣹⣿⡆⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢠⣎⣠⣴⠿⠃⠀⠀⠀
⠀⢀⣠⠔⠒⠈⠉⠀⠹⣿⣄⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⣠⠾⠛⠛⠉⠒⠢⣄⠀⠀
⠀⣿⡁⠀⠀⠀⠀⠀⠀⠈⢻⣦⡀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⣀⣾⡃⠀⠀⠀⠀⠀⠀⠀⡟⠀
⠀⠙⠻⣶⣀⠀⠀⠀⠀⠀⠀⠈⠙⠲⠦⣤⣄⣀⣀⣀⣤⣤⣾⣯⡵⠞⠋⠀⠀⠀⣀⠟⠀⠀⠀⠀
⠀⠀⠀⠀⠉⠛⠻⠿⠿⠶⠶⠤⠤⠤⣄⣀⣀⣀⣀⣀⣀⣀⣀⡠⠤⠤⠤⠴⠖⠉⠀⠀⠀⠀⠀⠀`

const (
	steamRows   = 14 // rows 0..13 of the art are steam, the rest is the mug
	steamShades = 6
	blank       = '\u2800'
)

// Braille dot bit for (x, y) inside a 2x4 cell.
var dotBit = [2][4]uint8{{0x01, 0x02, 0x04, 0x40}, {0x08, 0x10, 0x20, 0x80}}

var (
	grid [][]rune
	rows int
	cols int

	steamW, steamH int
	steamSrc       [][]bool // the steam as a dot-level bitmap

	mugCells [][]string // pre-rendered static mug cells (color + glyph)

	steamStyles  []lipgloss.Style
	mugStyles    []lipgloss.Style
	liquidStyles []lipgloss.Style
	wisps        []wisp
)

func init() {
	setupStyles()
	setupArt()
	makeWisps(16)
}

// makeShadeStyles builds a dim-to-bright ramp from two palette colors. The
// styles are built once; frames only pick one and render a glyph.
func makeShadeStyles(lo, hi color.Color, n int) []lipgloss.Style {
	out := make([]lipgloss.Style, n)
	for i := 0; i < n; i++ {
		out[i] = lipgloss.NewStyle().Foreground(mixColor(lo, hi, float64(i)/float64(n-1)))
	}
	return out
}

func mixColor(a, b color.Color, t float64) color.Color {
	t = clamp(t, 0, 1)
	ar, ag, ab, _ := a.RGBA()
	br, bg, bb, _ := b.RGBA()
	r := float64(ar>>8) + (float64(br>>8)-float64(ar>>8))*t
	g := float64(ag>>8) + (float64(bg>>8)-float64(ag>>8))*t
	bl := float64(ab>>8) + (float64(bb>>8)-float64(ab>>8))*t
	return lipgloss.Color(fmt.Sprintf("#%02X%02X%02X", int(r), int(g), int(bl)))
}

func setupStyles() {
	steamStyles = makeShadeStyles(style.Muted, style.Text, steamShades)
	mugStyles = makeShadeStyles(style.Muted, style.Text, steamShades)
	liquidStyles = []lipgloss.Style{
		lipgloss.NewStyle().Foreground(style.Warning),
		lipgloss.NewStyle().Foreground(style.Primary),
	}
}

func setupArt() {
	for _, line := range strings.Split(strings.Trim(art, "\n"), "\n") {
		grid = append(grid, []rune(line))
	}
	rows, cols = len(grid), len(grid[0])
	steamW, steamH = cols*2, steamRows*4

	steamSrc = make([][]bool, steamH)
	for y := range steamSrc {
		steamSrc[y] = make([]bool, steamW)
	}
	for r := 0; r < steamRows; r++ {
		for c := 0; c < cols; c++ {
			ch := grid[r][c]
			if ch < blank || ch > blank+0xFF {
				continue
			}
			bits := uint8(ch - blank)
			for dx := 0; dx < 2; dx++ {
				for dy := 0; dy < 4; dy++ {
					if bits&dotBit[dx][dy] != 0 {
						steamSrc[r*4+dy][c*2+dx] = true
					}
				}
			}
		}
	}

	mugCells = make([][]string, rows)
	for r := steamRows; r < rows; r++ {
		mugCells[r] = make([]string, cols)
		for c := 0; c < cols; c++ {
			ch := grid[r][c]
			if ch == blank || isLiquid(r, c) {
				continue // blank, or liquid (colored every frame)
			}
			mugCells[r][c] = mugStyles[mugShadeIndex(r, c)].Render(string(ch))
		}
	}
}

func isLiquid(r, c int) bool {
	switch r {
	case 15:
		return c >= 12 && c <= 23
	case 16:
		return c >= 9 && c <= 27
	case 17:
		return c >= 15 && c <= 21
	}
	return false
}

// mugShadeIndex picks a pre-rendered brightness for a static mug cell,
// shading the left wall darker, the rim bright, and the body darker toward
// the bottom.
func mugShadeIndex(r, c int) int {
	g := 0.35
	switch {
	case c <= 9:
		g = 0.65 // shaded left wall
	case c >= 26:
		g = 0.55 // right side and handle
	}
	g += 0.45 * float64(r-steamRows) / 9 // darker toward the bottom
	if r == steamRows {
		g = 0 // lit rim
	}
	idx := int(clamp(1-g, 0, 1) * float64(steamShades-1))
	return idx
}

type wisp struct{ phase, life, dx, amp, freq float64 }

func makeWisps(n int) {
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < n; i++ {
		wisps = append(wisps, wisp{
			phase: rng.Float64(),
			life:  2.2 + rng.Float64()*1.4,
			dx:    (rng.Float64() - 0.5) * 16,
			amp:   1.5 + rng.Float64()*2.5,
			freq:  1.5 + rng.Float64()*1.5,
		})
	}
}

func hash(x, y, s int) float64 {
	h := uint32(x)*374761393 + uint32(y)*668265263 + uint32(s)*2147483647
	h = (h ^ (h >> 13)) * 1274126177
	h ^= h >> 16
	return float64(h&0xffff) / 65535
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

func smoothstep(e0, e1, x float64) float64 {
	t := clamp((x-e0)/(e1-e0), 0, 1)
	return t * t * (3 - 2*t)
}

func shadeStyle(styles []lipgloss.Style, v float64) lipgloss.Style {
	idx := int(clamp(v, 0, 1) * float64(len(styles)-1))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(styles) {
		idx = len(styles) - 1
	}
	return styles[idx]
}

// Frame renders the animation at t. It returns only the mug block, with SGR
// color codes and plain newlines between lines: no cursor addressing, no
// screen clearing and no trailing newline.
func Frame(t time.Duration) string {
	secs := t.Seconds()
	bits := make([][]uint8, steamRows)
	bright := make([][]float64, steamRows)
	for i := range bits {
		bits[i] = make([]uint8, cols)
		bright[i] = make([]float64, cols)
	}
	plot := func(x, y int, b float64) {
		if x < 0 || x >= steamW || y < 0 || y >= steamH {
			return
		}
		r, c := y/4, x/2
		bits[r][c] |= dotBit[x%2][y%4]
		if b > bright[r][c] {
			bright[r][c] = b
		}
	}

	gain := smoothstep(0, 0.8, secs) // steam builds up at the start
	step := int(secs * 10)

	// The original steam curl, swaying and flowing upward.
	for y := 0; y < steamH; y++ {
		height := float64(steamH-1-y) / float64(steamH-1)
		off := int(math.Round(4.0 * math.Pow(height, 1.2) * math.Sin(0.16*float64(y)-secs*2.6)))
		row := float64(y) / 4
		wave := 0.94 + 0.06*math.Sin(0.55*row-secs*6)
		top := 0.35 + 0.65*smoothstep(0, 6, row)
		b := wave * top * gain
		dissolve := 0.5 * math.Pow(height, 3) * (0.6 + 0.4*math.Sin(secs*3))
		for x := 0; x < steamW; x++ {
			sx := x - off
			if sx < 0 || sx >= steamW || !steamSrc[y][sx] {
				continue
			}
			if hash(x, y, step) < dissolve {
				continue
			}
			plot(x, y, b)
		}
	}

	// Little wisps that drift up and fade away.
	const baseX = 36.0
	for _, w := range wisps {
		p := math.Mod(secs/w.life+w.phase, 1)
		y := float64(steamH-4) - p*float64(steamH-2)
		x := baseX + w.dx*p + w.amp*math.Sin(secs*w.freq+w.phase*6+p*5)*(0.3+p)
		b := math.Pow(math.Sin(math.Pi*p), 0.8) * 0.85 * gain
		plot(int(math.Round(x)), int(math.Round(y)), b)
		plot(int(math.Round(x))+1, int(math.Round(y)), b)
	}

	lines := make([]string, 0, rows)
	for r := 0; r < rows; r++ {
		var sb strings.Builder
		sb.WriteString("  ")
		for c := 0; c < cols; c++ {
			switch {
			case r < steamRows:
				if bits[r][c] == 0 {
					sb.WriteByte(' ')
					continue
				}
				sb.WriteString(shadeStyle(steamStyles, bright[r][c]).Render(string(blank + rune(bits[r][c]))))
			case isLiquid(r, c):
				v := 0.5 + 0.5*math.Sin(float64(c)*0.7-secs*4+float64(r))
				sb.WriteString(shadeStyle(liquidStyles, v*0.6).Render(string(grid[r][c])))
			case mugCells[r][c] == "":
				sb.WriteByte(' ')
			default:
				sb.WriteString(mugCells[r][c])
			}
		}
		sb.WriteString("\x1b[0m")
		lines = append(lines, sb.String())
	}
	return strings.Join(lines, "\n")
}
