//go:build ignore

// coffee.go - an animated, colored terminal coffee mug with rising steam.
//
// Run:   go run coffee.go            (plays a 5 second animation)
// Flags: -duration 5s   how long to play
//
//	-fps 24        frames per second
//	-loop          keep playing until Ctrl-C
//
// Needs a terminal with ANSI color support (truecolor is used when the
// terminal advertises it, otherwise it falls back to 256 colors).
package main

import (
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

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

// ---------------------------------------------------------------- colors --

type rgb struct{ r, g, b float64 }

var (
	steamColor = rgb{225, 225, 225} // slightly darker than the mug's white
	mugWhite   = rgb{255, 255, 255}
	mugGray    = rgb{115, 115, 120}
	coffeeDark = rgb{92, 58, 36}
	coffeeLite = rgb{150, 100, 62}
)

func lerp(a, b rgb, t float64) rgb {
	t = clamp(t, 0, 1)
	return rgb{a.r + (b.r-a.r)*t, a.g + (b.g-a.g)*t, a.b + (b.b-a.b)*t}
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

func smoothstep(e0, e1, x float64) float64 {
	t := clamp((x-e0)/(e1-e0), 0, 1)
	return t * t * (3 - 2*t)
}

var truecolor = func() bool {
	c := strings.ToLower(os.Getenv("COLORTERM"))
	return strings.Contains(c, "truecolor") || strings.Contains(c, "24bit")
}()

func to256(r, g, b int) int {
	abs := func(x int) int {
		if x < 0 {
			return -x
		}
		return x
	}
	if abs(r-g) < 12 && abs(g-b) < 12 { // grays -> grayscale ramp
		switch {
		case r < 8:
			return 16
		case r > 248:
			return 231
		}
		return 232 + (r-8)/10
	}
	cube := func(v int) int { return int(float64(v)/255*5 + 0.5) }
	return 16 + 36*cube(r) + 6*cube(g) + cube(b)
}

func fg(c rgb) string {
	r := int(clamp(c.r, 0, 255))
	g := int(clamp(c.g, 0, 255))
	b := int(clamp(c.b, 0, 255))
	if truecolor {
		return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", r, g, b)
	}
	return fmt.Sprintf("\x1b[38;5;%dm", to256(r, g, b))
}

// ------------------------------------------------------------------- art --

const (
	steamRows = 14 // rows 0..13 of the art are steam, the rest is the mug
	blank     = '\u2800'
)

// Braille dot bit for (x, y) inside a 2x4 cell.
var dotBit = [2][4]uint8{{0x01, 0x02, 0x04, 0x40}, {0x08, 0x10, 0x20, 0x80}}

var (
	grid [][]rune
	rows int
	cols int

	steamW, steamH int
	steamSrc       [][]bool // the steam as a dot-level bitmap

	mugCells [][]string // pre-rendered mug cells (color + glyph)
)

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

func mugColor(r, c int) rgb {
	g := 0.10
	switch {
	case c <= 9:
		g = 0.55 // shaded left wall
	case c >= 26:
		g = 0.38 // right side and handle
	}
	g += 0.5 * float64(r-steamRows) / 9 // darker toward the bottom
	if r == steamRows {
		g = 0 // lit rim
	}
	return lerp(mugWhite, mugGray, clamp(g, 0, 0.85))
}

func glyph(ch rune) rune {
	if ch == blank {
		return ' '
	}
	return ch
}

func setup() {
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
			bits := uint8(grid[r][c] - blank)
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
			mugCells[r][c] = fg(mugColor(r, c)) + string(ch)
		}
	}
}

// ------------------------------------------------------------- particles --

type wisp struct{ phase, life, dx, amp, freq float64 }

var wisps []wisp

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

// ---------------------------------------------------------------- render --

func render(t float64) string {
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

	gain := smoothstep(0, 0.8, t) // steam builds up at the start
	step := int(t * 10)

	// 1) the original steam curl, swaying and flowing upward
	for y := 0; y < steamH; y++ {
		height := float64(steamH-1-y) / float64(steamH-1) // 0 at the mug, 1 at the top
		off := int(math.Round(4.0 * math.Pow(height, 1.2) * math.Sin(0.16*float64(y)-t*2.6)))
		row := float64(y) / 4
		wave := 0.94 + 0.06*math.Sin(0.55*row-t*6)
		top := 0.35 + 0.65*smoothstep(0, 6, row)
		b := wave * top * gain
		dissolve := 0.5 * math.Pow(height, 3) * (0.6 + 0.4*math.Sin(t*3))
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

	// 2) little wisps that drift up and fade away
	baseX := 36.0
	for _, w := range wisps {
		p := math.Mod(t/w.life+w.phase, 1)
		y := float64(steamH-4) - p*float64(steamH-2)
		x := baseX + w.dx*p + w.amp*math.Sin(t*w.freq+w.phase*6+p*5)*(0.3+p)
		b := math.Pow(math.Sin(math.Pi*p), 0.8) * 0.85 * gain
		plot(int(math.Round(x)), int(math.Round(y)), b)
		plot(int(math.Round(x))+1, int(math.Round(y)), b)
	}

	var sb strings.Builder
	sb.WriteString("\x1b[H")
	for r := 0; r < rows; r++ {
		sb.WriteString("  ")
		for c := 0; c < cols; c++ {
			switch {
			case r < steamRows:
				if bits[r][c] == 0 {
					sb.WriteByte(' ')
					continue
				}
				col := lerp(rgb{0, 0, 0}, steamColor, bright[r][c])
				sb.WriteString(fg(col))
				sb.WriteRune(blank + rune(bits[r][c]))
			case isLiquid(r, c):
				v := 0.5 + 0.5*math.Sin(float64(c)*0.7-t*4+float64(r))
				sb.WriteString(fg(lerp(coffeeDark, coffeeLite, v*0.6)))
				sb.WriteRune(grid[r][c])
			case mugCells[r][c] == "":
				sb.WriteByte(' ')
			default:
				sb.WriteString(mugCells[r][c])
			}
		}
		sb.WriteString("\x1b[0m\x1b[K\n")
	}
	return sb.String()
}

// ------------------------------------------------------------------ main --

func main() {
	duration := flag.Duration("duration", 5*time.Second, "how long to play")
	fps := flag.Int("fps", 24, "frames per second")
	loop := flag.Bool("loop", false, "loop until Ctrl-C")
	flag.Parse()

	setup()
	makeWisps(16)

	restore := func() { fmt.Print("\x1b[0m\x1b[?25h") }
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		restore()
		fmt.Println()
		os.Exit(0)
	}()
	defer restore()

	fmt.Print("\x1b[?25l\x1b[2J") // hide cursor, clear screen

	start := time.Now()
	tick := time.NewTicker(time.Second / time.Duration(*fps))
	defer tick.Stop()
	for {
		t := time.Since(start).Seconds()
		if !*loop && t >= duration.Seconds() {
			t = duration.Seconds()
		}
		fmt.Print(render(t))
		if !*loop && t >= duration.Seconds() {
			break
		}
		<-tick.C
	}
}
