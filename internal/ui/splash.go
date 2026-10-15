package ui

import (
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/JGoterris/orbit/internal/ui/theme"
)

// The splash is orbit's logo (assets/logo.svg) drawn with half blocks, two square pixels per cell, and animated while
// the app starts: the core grows, the ring draws itself, the satellites start orbiting and the wordmark comes in. Any
// key closes it like an iris onto the core, and the app shows through; a second key skips the rest.

const (
	splashFPS    = 30
	splashHold   = 2700 * time.Millisecond // until it closes on its own
	splashClose  = 450 * time.Millisecond
	splashTag    = "local services, in orbit"
	splashBy     = "crafted by "
	splashAuthor = "JGoterris"
)

type splashTickMsg struct{}

func splashTick() tea.Cmd {
	return tea.Tick(time.Second/splashFPS, func(time.Time) tea.Msg { return splashTickMsg{} })
}

type splashStar struct {
	x, y          float64 // fraction of the screen
	glyph         string
	phase, speed  float64
	delay, bright float64
	tint          int // 0-2 text, 3 cyan, 4 violet
}

type splashState struct {
	start   time.Time
	closing time.Time // zero while it plays
	stars   []splashStar
}

func newSplash() *splashState {
	r := rand.New(rand.NewPCG(7, 42))
	s := &splashState{start: time.Now()}
	for range 400 {
		g := "·"
		switch v := r.Float64(); {
		case v > 0.95:
			g = "✦"
		case v > 0.87:
			g = "⋆"
		case v > 0.80:
			g = "+"
		}
		s.stars = append(s.stars, splashStar{
			x: r.Float64(), y: r.Float64(), glyph: g,
			phase: r.Float64() * 2 * math.Pi, speed: 1.5 + r.Float64()*3,
			delay: r.Float64() * 0.5, bright: 0.25 + r.Float64()*0.75, tint: r.IntN(5),
		})
	}
	return s
}

func splashFits(w, h int) bool { return w >= 44 && h >= 10 }

// splashUpdate handles a message while the splash is up; handled=false lets the app see it as well.
func (m *Model) splashUpdate(msg tea.Msg) (cmd tea.Cmd, handled bool) {
	s := m.splash
	if !splashFits(m.width, m.height) {
		m.splash = nil
		_, tick := msg.(splashTickMsg)
		return nil, tick
	}
	switch msg := msg.(type) {
	case splashTickMsg:
		now := time.Now()
		if s.closing.IsZero() && now.Sub(s.start) >= splashHold {
			s.closing = now
		}
		if !s.closing.IsZero() && now.Sub(s.closing) >= splashClose {
			m.splash = nil
			return nil, true
		}
		return splashTick(), true
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			m.splash = nil
			return nil, false
		}
		s.skip(m)
		return nil, true
	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress {
			s.skip(m)
		}
		return nil, true
	}
	return nil, false
}

// skip starts closing; when it already is, it ends right away.
func (s *splashState) skip(m *Model) {
	if s.closing.IsZero() {
		s.closing = time.Now()
		return
	}
	m.splash = nil
}

// ---------------------------------------------------------------- colour

type rgb struct{ r, g, b float64 }

func hexRGB(s string) rgb {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return rgb{}
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return rgb{}
	}
	return rgb{float64(v >> 16 & 0xff), float64(v >> 8 & 0xff), float64(v & 0xff)}
}

func (a rgb) mix(b rgb, t float64) rgb {
	t = clamp01(t)
	return rgb{a.r + (b.r-a.r)*t, a.g + (b.g-a.g)*t, a.b + (b.b-a.b)*t}
}

func (a rgb) near(b rgb) bool {
	return math.Abs(a.r-b.r)+math.Abs(a.g-b.g)+math.Abs(a.b-b.b) < 6
}

func (a rgb) key() [3]uint8 {
	return [3]uint8{uint8(math.Round(a.r)), uint8(math.Round(a.g)), uint8(math.Round(a.b))}
}

type splashPalette struct {
	bg, text, muted, dim, cyan, violet, green rgb
}

func splashColors() splashPalette {
	p := theme.Active()
	return splashPalette{
		bg: hexRGB(p.Bg), text: hexRGB(p.Text), muted: hexRGB(p.Muted), dim: hexRGB(p.Dim),
		cyan: hexRGB(p.Cyan), violet: hexRGB(p.Accent2), green: hexRGB(p.Green),
	}
}

// ---------------------------------------------------------------- easing

func clamp01(t float64) float64 { return math.Max(0, math.Min(1, t)) }

// phase is how far t is through [from, from+length], 0..1.
func phase(t, from, length float64) float64 { return clamp01((t - from) / length) }

func easeOutCubic(t float64) float64 { return 1 - math.Pow(1-t, 3) }

func easeInCubic(t float64) float64 { return t * t * t }

func easeInOutCubic(t float64) float64 {
	if t < 0.5 {
		return 4 * t * t * t
	}
	return 1 - math.Pow(-2*t+2, 3)/2
}

func easeOutBack(t float64) float64 {
	const c1 = 1.70158
	const c3 = c1 + 1
	return 1 + c3*math.Pow(t-1, 3) + c1*math.Pow(t-1, 2)
}

// coverage is how much of a pixel aa units wide lies inside a shape at signed distance d.
func coverage(d, aa float64) float64 { return clamp01(0.5 - d/aa) }

// ---------------------------------------------------------------- cells

type splashCell struct {
	ch     string
	fg, bg rgb
}

type splashGrid struct {
	w, h  int
	cells []splashCell
}

func newSplashGrid(w, h int, bg rgb) *splashGrid {
	g := &splashGrid{w: w, h: h, cells: make([]splashCell, w*h)}
	for i := range g.cells {
		g.cells[i] = splashCell{ch: " ", bg: bg}
	}
	return g
}

func (g *splashGrid) at(x, y int) *splashCell {
	if x < 0 || y < 0 || x >= g.w || y >= g.h {
		return nil
	}
	return &g.cells[y*g.w+x]
}

// text writes a string of single-width runes starting at x, on the cells' background.
func (g *splashGrid) text(x, y int, s string, fg rgb) int {
	for _, r := range s {
		if c := g.at(x, y); c != nil {
			c.ch, c.fg = string(r), fg
		}
		x++
	}
	return x
}

// blit draws a pixel canvas at cell (x, y): two pixels per cell, the top one as ▀'s foreground. Cells where both are
// still the background keep what was there (the stars).
func (g *splashGrid) blit(c *pixels, x, y int, bg rgb) {
	for row := 0; row*2 < c.h; row++ {
		for col := range c.w {
			top := c.px[row*2*c.w+col]
			bot := bg
			if row*2+1 < c.h {
				bot = c.px[(row*2+1)*c.w+col]
			}
			if top.near(bg) && bot.near(bg) {
				continue
			}
			cell := g.at(x+col, y+row)
			if cell == nil {
				continue
			}
			if top.near(bot) {
				*cell = splashCell{ch: " ", bg: top}
			} else {
				*cell = splashCell{ch: "▀", fg: top, bg: bot}
			}
		}
	}
}

// run renders the cells [x0, x1) of a row with truecolor escapes, emitting them only when the colours change.
func (g *splashGrid) run(y, x0, x1 int) string {
	var b strings.Builder
	b.WriteString("\x1b[0m")
	var fg, bg [3]uint8
	hasFg, hasBg := false, false
	for x := x0; x < x1; x++ {
		c := g.cells[y*g.w+x]
		if k := c.bg.key(); !hasBg || k != bg {
			bg, hasBg = k, true
			writeSGR(&b, 48, k)
		}
		if c.ch != " " {
			if k := c.fg.key(); !hasFg || k != fg {
				fg, hasFg = k, true
				writeSGR(&b, 38, k)
			}
		}
		b.WriteString(c.ch)
	}
	b.WriteString("\x1b[0m")
	return b.String()
}

func writeSGR(b *strings.Builder, kind int, c [3]uint8) {
	b.WriteString("\x1b[")
	b.WriteString(strconv.Itoa(kind))
	b.WriteString(";2;")
	b.WriteString(strconv.Itoa(int(c[0])))
	b.WriteByte(';')
	b.WriteString(strconv.Itoa(int(c[1])))
	b.WriteByte(';')
	b.WriteString(strconv.Itoa(int(c[2])))
	b.WriteByte('m')
}

type pixels struct {
	w, h int
	px   []rgb
}

// ---------------------------------------------------------------- layout

// The logo's scene, in the SVG's units around its centre: the tilted ring and the satellites with their halos.
const sceneW, sceneH = 228.0, 160.0

type splashLayout struct {
	logoRows, logoW, logoX, logoY int
	scale                         float64 // scene units per pixel
	k                             int     // size of a wordmark pixel
	wordX, wordY                  int
	textY                         int
	cx, cy                        float64 // where the core is, in cells
}

func layoutSplash(w, h int) splashLayout {
	l := splashLayout{k: 1}
	if w >= 100 && h >= 46 {
		l.k = 2
	}
	wordRows := wordmarkHeight * l.k / 2
	text := wordRows + 3 // wordmark, blank, tagline, meta
	gap := l.k
	rows := min(h-2-text-gap, 14*l.k+2)
	if byWidth := int(float64(w-4) * sceneH / sceneW / 2); rows > byWidth {
		rows = byWidth
	}
	if rows < 7 {
		rows, gap = 0, 0
	}
	l.logoRows = rows
	if rows > 0 {
		l.scale = sceneH / float64(rows*2)
		l.logoW = int(math.Ceil(sceneW / l.scale))
	}
	top := (h - (rows + gap + text)) / 2
	l.logoX, l.logoY = (w-l.logoW)/2, top
	l.wordY = top + rows + gap
	l.wordX = (w - wordmarkWidth*l.k) / 2
	l.textY = l.wordY + wordRows + 1
	l.cx, l.cy = float64(l.logoX)+float64(l.logoW)/2, float64(l.logoY)+float64(rows)/2
	if rows == 0 {
		l.cx, l.cy = float64(w)/2, float64(l.wordY)+float64(wordRows)/2
	}
	return l
}

// ---------------------------------------------------------------- logo

const (
	ringRX, ringRY = 100.0, 38.0
	ringTilt       = -28 * math.Pi / 180
	coreR, coreDot = 44.0, 27.0
	greenStart     = -0.35 // where the green satellite sits in the logo, as an angle on the ring
	violetStart    = 2.65
)

type orbitDot struct {
	x, y, r, halo float64
	col           rgb
	alpha         float64
	front         bool
}

type logoFrame struct {
	aa, hw, gap  float64
	core, pulse  float64
	glow         float64
	sweep        float64
	dots         []orbitDot
	pal          splashPalette
	tiltC, tiltS float64
	coreGradR    float64
}

// satellite adds a satellite at angle phi on the ring, with a fading trail behind it.
func (f *logoFrame) satellite(phi, speed, r, halo float64, col rgb, pop float64) {
	if pop <= 0 {
		return
	}
	for k := 6; k >= 0; k-- {
		a := phi + float64(k)*0.07*math.Copysign(1, speed)
		u, v := ringRX*math.Cos(a), ringRY*math.Sin(a)
		depth := math.Sin(a)
		scale := (1 + 0.18*depth) * pop
		alpha := 1.0
		if depth < 0 {
			alpha = 0.55 + 0.45*(1+depth)
		}
		d := orbitDot{
			x: u*f.tiltC - v*f.tiltS, y: u*f.tiltS + v*f.tiltC,
			r: r * scale, col: col, alpha: alpha, front: depth > 0,
		}
		if k == 0 {
			d.halo = halo * scale
		} else {
			fade := 1 - float64(k)/7
			d.r *= 0.35 + 0.45*fade
			d.alpha *= 0.45 * fade
		}
		f.dots = append(f.dots, d)
	}
}

func (f *logoFrame) dot(c rgb, d orbitDot, x, y float64) rgb {
	dist := math.Hypot(x-d.x, y-d.y)
	if d.halo > 0 {
		c = c.mix(f.pal.bg, coverage(dist-d.halo, f.aa))
	}
	return c.mix(d.col, coverage(dist-d.r, f.aa)*d.alpha)
}

// ring is the colour of the ring at u (along its long axis) and how opaque it is there.
func (f *logoFrame) ring(u float64) (rgb, float64) {
	t := clamp01((u + ringRX) / (2 * ringRX))
	if t < 0.5 {
		return f.pal.violet.mix(f.pal.muted, t*2), 0.35 + (0.9-0.35)*t*2
	}
	return f.pal.muted.mix(f.pal.cyan, (t-0.5)*2), 0.9 + 0.1*(t-0.5)*2
}

// sweepAt is how much of the ring is drawn at angle theta, and how bright the drawing head is there.
func (f *logoFrame) sweepAt(theta float64) (on, head float64) {
	if f.sweep >= 1 {
		return 1, 0
	}
	if f.sweep <= 0 {
		return 0, 0
	}
	delta := math.Mod(theta-greenStart+4*math.Pi, 2*math.Pi)
	left := f.sweep*2*math.Pi - delta
	return clamp01(left / 0.15), math.Exp(-left * left / 0.08)
}

func (f *logoFrame) pixel(x, y float64) rgb {
	p := f.pal
	c := p.bg
	r := math.Hypot(x, y)
	if f.glow > 0 {
		g := math.Exp(-math.Pow((r-20)/42, 2)) * f.glow
		c = c.mix(p.cyan.mix(p.violet, 0.5), g*0.2)
	}

	u := x*f.tiltC + y*f.tiltS
	v := -x*f.tiltS + y*f.tiltC
	dRing := ellipseDist(u, v, ringRX, ringRY)
	on, head := f.sweepAt(math.Atan2(v/ringRY, u/ringRX))
	ringCol, ringA := f.ring(u)
	ringCol = ringCol.mix(p.text, head)
	ringA = math.Max(ringA, head)
	ringCov := coverage(math.Abs(dRing)-f.hw, f.aa) * on * ringA
	front := v > 0

	if !front {
		c = c.mix(ringCol, ringCov)
	}
	for _, d := range f.dots {
		if !d.front {
			c = f.dot(c, d, x, y)
		}
	}
	if f.core > 0 {
		col := p.cyan.mix(p.violet, (x+y)/(2*f.coreGradR)+0.5)
		c = c.mix(col, coverage(math.Abs(r-coreR*f.core)-f.hw*f.core, f.aa))
		c = c.mix(col, coverage(r-coreDot*f.core*(1+f.pulse), f.aa))
	}
	if front {
		c = c.mix(p.bg, coverage(math.Abs(dRing)-f.gap, f.aa)*on)
		c = c.mix(ringCol, ringCov)
	}
	for _, d := range f.dots {
		if d.front {
			c = f.dot(c, d, x, y)
		}
	}
	return c
}

// ellipseDist approximates the signed distance from (u, v) to the ellipse, good enough near its outline.
func ellipseDist(u, v, rx, ry float64) float64 {
	f := u*u/(rx*rx) + v*v/(ry*ry) - 1
	gx, gy := 2*u/(rx*rx), 2*v/(ry*ry)
	g := math.Hypot(gx, gy)
	if g < 1e-9 {
		return -ry
	}
	return f / g
}

func drawLogo(l splashLayout, pal splashPalette, t float64) *pixels {
	f := &logoFrame{
		aa: l.scale, pal: pal, tiltC: math.Cos(ringTilt), tiltS: math.Sin(ringTilt), coreGradR: 40,
	}
	f.hw = math.Max(3.5, 0.6*l.scale)
	f.gap = f.hw * 19 / 7
	f.core = easeOutBack(phase(t, 0.05, 0.5))
	if t < 0.05 {
		f.core = 0
	}
	f.pulse = 0.05 * math.Sin(t*2*math.Pi/1.6) * phase(t, 0.55, 0.3)
	f.glow = easeOutCubic(phase(t, 0.1, 0.6)) * (0.75 + 0.25*math.Sin(t*2*math.Pi/1.6))
	f.sweep = easeInOutCubic(phase(t, 0.25, 0.7))
	pop := easeOutBack(phase(t, 0.8, 0.35))
	if t < 0.8 {
		pop = 0
	}
	f.satellite(greenStart-t*2*math.Pi/3.2, -1, 11, 17, pal.green, pop)
	f.satellite(violetStart-t*2*math.Pi/4.6, -1, 7, 12, pal.violet, pop)

	c := &pixels{w: l.logoW, h: l.logoRows * 2}
	c.px = make([]rgb, c.w*c.h)
	for j := range c.h {
		y := (float64(j)+0.5)*l.scale - sceneH/2
		for i := range c.w {
			x := (float64(i)+0.5)*l.scale - sceneW/2
			c.px[j*c.w+i] = f.pixel(x, y)
		}
	}
	return c
}

// ---------------------------------------------------------------- wordmark

// The wordmark, one string per row of pixels: '#' is ink, '@' the dot of the i, a satellite.
var wordmarkGlyphs = [][]string{
	{ // o
		".......", ".......", ".......", ".#####.", "##...##", "##...##", "##...##", "##...##", "##...##", ".#####.",
	},
	{ // r
		"......", "......", "......", "##.###", "###...", "##....", "##....", "##....", "##....", "##....",
	},
	{ // b
		"##.....", "##.....", "##.....", "######.", "##...##", "##...##", "##...##", "##...##", "##...##", "######.",
	},
	{ // i
		"@@", "@@", "..", "##", "##", "##", "##", "##", "##", "##",
	},
	{ // t
		".....", ".##..", ".##..", "#####", ".##..", ".##..", ".##..", ".##..", ".##..", "..###",
	},
}

const (
	wordmarkHeight = 10
	wordmarkGap    = 2
)

var wordmarkWidth = func() int {
	w := 0
	for i, g := range wordmarkGlyphs {
		if i > 0 {
			w += wordmarkGap
		}
		w += len(g[0])
	}
	return w
}()

func drawWordmark(k int, pal splashPalette, t float64) *pixels {
	c := &pixels{w: wordmarkWidth * k, h: wordmarkHeight * k}
	c.px = make([]rgb, c.w*c.h)
	for i := range c.px {
		c.px[i] = pal.bg
	}
	shine := pal.text.mix(rgb{255, 255, 255}, 0.5)
	band := -0.3 + 1.6*phase(t, 1.45, 0.55)
	x0 := 0
	for li, g := range wordmarkGlyphs {
		gw := len(g[0])
		e := easeOutCubic(phase(t, 0.9+0.07*float64(li), 0.35))
		drop := int(math.Round((1 - e) * 4 * float64(k)))
		for j := range c.h {
			gy := (j - drop) / k
			if j-drop < 0 || gy >= wordmarkHeight {
				continue
			}
			for i := range gw * k {
				ch := g[gy][i/k]
				if ch == '.' {
					continue
				}
				px := x0*k + i
				fx, fy := float64(px)/float64(c.w), float64(j)/float64(c.h)
				col := pal.cyan.mix(pal.violet, fx*0.85+fy*0.15)
				if ch == '@' {
					col = pal.green
				}
				if band > -0.3 && band < 1.3 {
					s := fx + fy*0.2 - band
					col = col.mix(shine, math.Exp(-s*s/0.006)*0.85)
				}
				c.px[j*c.w+px] = pal.bg.mix(col, e)
			}
		}
		x0 += gw + wordmarkGap
	}
	return c
}

// ---------------------------------------------------------------- the frame

func (s *splashState) drawStars(g *splashGrid, pal splashPalette, t float64) {
	n := min(len(s.stars), g.w*g.h/40)
	for _, st := range s.stars[:n] {
		b := st.bright * (0.55 + 0.45*math.Sin(st.phase+st.speed*t)) * easeOutCubic(phase(t, st.delay, 0.5))
		col := pal.text
		switch st.tint {
		case 3:
			col = pal.cyan
		case 4:
			col = pal.violet
		}
		if c := g.at(int(st.x*float64(g.w)), int(st.y*float64(g.h))); c != nil {
			c.ch, c.fg = st.glyph, pal.bg.mix(col, b*0.7)
		}
	}
}

// shootingStar crosses the top left of the screen once.
func shootingStar(g *splashGrid, pal splashPalette, t float64) {
	p := phase(t, 1.0, 0.6)
	if p <= 0 || p >= 1 {
		return
	}
	x0, y0 := float64(g.w)*0.08, float64(g.h)*0.12
	x1, y1 := float64(g.w)*0.42, float64(g.h)*0.30
	fade := math.Sin(p * math.Pi)
	for k := 0; k < 10; k++ {
		q := easeOutCubic(p) - float64(k)*0.018
		if q < 0 {
			break
		}
		c := g.at(int(x0+(x1-x0)*q), int(y0+(y1-y0)*q))
		if c == nil || c.ch != " " {
			continue
		}
		glyph := "·"
		if k == 0 {
			glyph = "✦"
		}
		c.ch, c.fg = glyph, pal.bg.mix(pal.text.mix(pal.cyan, float64(k)/10), fade*(1-float64(k)/10))
	}
}

// renderSplash draws the current frame of the splash; false when the terminal is too small for it.
func (m *Model) renderSplash() (string, bool) {
	w, h := m.width, m.height
	if !splashFits(w, h) {
		return "", false
	}
	s := m.splash
	t := time.Since(s.start).Seconds()
	pal := splashColors()
	l := layoutSplash(w, h)

	g := newSplashGrid(w, h, pal.bg)
	s.drawStars(g, pal, t)
	shootingStar(g, pal, t)
	if l.logoRows > 0 {
		g.blit(drawLogo(l, pal, t), l.logoX, l.logoY, pal.bg)
	}
	g.blit(drawWordmark(l.k, pal, t), l.wordX, l.wordY, pal.bg)

	// the tagline types itself in, then the project and the version fade in under it
	tag := []rune(splashTag)
	typed := min(len(tag), int(math.Max(0, t-1.25)*38))
	tx := (w - len(tag)) / 2
	end := g.text(tx, l.textY, string(tag[:typed]), pal.muted)
	if typed < len(tag) && t > 1.25 {
		g.text(end, l.textY, "▌", pal.cyan)
	}
	fade := easeOutCubic(phase(t, 1.55, 0.35))
	name, version := m.sup.Config().Name, m.opts.Version
	if version != "" && version != "dev" {
		version = "v" + version
	}
	meta := []struct {
		text string
		col  rgb
	}{{name, pal.text}, {" · ", pal.dim}, {version, pal.dim}}
	if name == "" || version == "" {
		meta[1].text = ""
	}
	mw := 0
	for _, part := range meta {
		mw += len([]rune(part.text))
	}
	mx := (w - mw) / 2
	for _, part := range meta {
		mx = g.text(mx, l.textY+1, part.text, pal.bg.mix(part.col, fade))
	}

	// the credits, at the foot of the screen
	credit := easeOutCubic(phase(t, 1.75, 0.4))
	cy := max(h-2, l.textY+2)
	cx := g.text((w-len(splashBy)-len(splashAuthor))/2, cy, splashBy, pal.bg.mix(pal.dim, credit))
	g.text(cx, cy, splashAuthor, pal.bg.mix(pal.cyan.mix(pal.violet, 0.4), credit))

	rows := make([]string, h)
	if s.closing.IsZero() {
		for y := range h {
			rows[y] = g.run(y, 0, w)
		}
		return strings.Join(rows, "\n"), true
	}
	return m.splashIris(g, l, pal, time.Since(s.closing).Seconds()/splashClose.Seconds()), true
}

// splashIris closes the splash into a circle on the core, with a glowing rim, and shows the app outside of it.
func (m *Model) splashIris(g *splashGrid, l splashLayout, pal splashPalette, progress float64) string {
	w, h := g.w, g.h
	app := strings.Split(m.viewApp(), "\n")
	for len(app) < h {
		app = append(app, "")
	}
	bg := theme.Active().Bg
	reach := 0.0
	for _, corner := range [][2]float64{{0, 0}, {float64(w), 0}, {0, float64(h)}, {float64(w), float64(h)}} {
		reach = math.Max(reach, math.Hypot(corner[0]-l.cx, 2*(corner[1]-l.cy)))
	}
	radius := (reach + 3) * (1 - easeInCubic(clamp01(progress)))
	const rim = 2.5
	kind := func(x, y int) (int, float64) {
		d := math.Hypot(float64(x)+0.5-l.cx, 2*(float64(y)+0.5-l.cy))
		switch {
		case d < radius:
			return 2, d
		case d < radius+rim:
			return 1, d
		}
		return 0, d
	}
	rows := make([]string, h)
	for y := range h {
		var b strings.Builder
		for x := 0; x < w; {
			k, _ := kind(x, y)
			x1 := x + 1
			for x1 < w {
				if k1, _ := kind(x1, y); k1 != k {
					break
				}
				x1++
			}
			switch k {
			case 0:
				b.WriteString(clamp(ansi.Cut(app[y], x, x1), x1-x, bg))
				b.WriteString("\x1b[0m")
			case 2:
				b.WriteString(g.run(y, x, x1))
			default:
				for cx := x; cx < x1; cx++ {
					_, d := kind(cx, y)
					angle := math.Atan2(2*(float64(y)+0.5-l.cy), float64(cx)+0.5-l.cx)
					col := pal.cyan.mix(pal.violet, (math.Cos(angle-0.6)+1)/2)
					cell := g.cells[y*w+cx]
					cell.bg = cell.bg.mix(col, (1-(d-radius)/rim)*0.9)
					if cell.ch != " " {
						cell.fg = cell.fg.mix(col, 0.5)
					}
					tmp := splashGrid{w: 1, h: 1, cells: []splashCell{cell}}
					b.WriteString(tmp.run(0, 0, 1))
				}
			}
			x = x1
		}
		rows[y] = b.String()
	}
	return strings.Join(rows, "\n")
}
