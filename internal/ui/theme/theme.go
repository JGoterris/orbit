// Package theme holds orbit's colours and the small visual helpers every view shares: status icons, type badges,
// sparklines and charts.
package theme

import (
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/charmbracelet/x/ansi"
)

var (
	mu     sync.RWMutex
	active = clone(Themes[DefaultName])
)

func clone(p Palette) Palette {
	p.Services = append([]string(nil), p.Services...)
	return p
}

// Active is the palette in use. It is a copy: Apply swaps it, views read it every time they draw.
func Active() Palette {
	mu.RLock()
	defer mu.RUnlock()
	return clone(active)
}

// Apply makes p the active palette.
func Apply(p Palette) {
	mu.Lock()
	defer mu.Unlock()
	active = clone(p)
}

// Get reads a colour of the palette by its json name ("accent", "panelAlt"...).
func (p Palette) Get(key string) (string, bool) {
	f, ok := field(&p, key)
	if !ok {
		return "", false
	}
	return f.String(), true
}

// Set writes a colour of the palette by its json name.
func (p *Palette) Set(key, value string) bool {
	f, ok := field(p, key)
	if !ok {
		return false
	}
	f.SetString(value)
	return true
}

func field(p *Palette, key string) (reflect.Value, bool) {
	v := reflect.ValueOf(p).Elem()
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name == key && t.Field(i).Type.Kind() == reflect.String {
			return v.Field(i), true
		}
	}
	return reflect.Value{}, false
}

// ---------------------------------------------------------------- status

// Status mirrors supervisor.Status without importing it: the names are the same.
type Status = string

// StatusStyle is how a status is drawn.
type StatusStyle struct {
	Icon  string
	Label string
	Color string
}

// StyleFor is the style of a status, with finished oneshot tasks shown as done instead of exited.
func StyleFor(status Status, oneshot bool) StatusStyle {
	p := Active()
	switch {
	case oneshot && status == "exited":
		return StatusStyle{"✓", "done", p.Green}
	case oneshot && status == "starting":
		return StatusStyle{"◐", "running", p.Yellow}
	}
	switch status {
	case "stopped":
		return StatusStyle{"○", "stopped", p.Dim}
	case "waiting":
		return StatusStyle{"◌", "waiting", p.Yellow}
	case "starting":
		return StatusStyle{"◐", "starting", p.Yellow}
	case "running":
		return StatusStyle{"●", "running", p.Cyan}
	case "healthy":
		return StatusStyle{"●", "healthy", p.Green}
	case "unhealthy":
		return StatusStyle{"●", "unhealthy", p.Orange}
	case "stopping":
		return StatusStyle{"◑", "stopping", p.Yellow}
	case "exited":
		return StatusStyle{"○", "exited", p.Muted}
	case "crashed", "failed":
		return StatusStyle{"✖", status, p.Red}
	}
	return StatusStyle{"○", status, p.Dim}
}

var spinner = []string{"◐", "◓", "◑", "◒"}

// StatusIcon is the icon of a status; transitional states spin with tick.
func StatusIcon(status Status, tick int, oneshot bool) string {
	switch status {
	case "starting", "stopping", "waiting":
		return spinner[tick%len(spinner)]
	}
	return StyleFor(status, oneshot).Icon
}

// Badge is the short tag of a service type.
type Badge struct {
	Label string
	Color string
}

var (
	pluginBadgesMu sync.RWMutex
	pluginBadges   map[string]string
)

// SetPluginBadges registers the short tags of the service types plugins provide (type → tag).
func SetPluginBadges(tags map[string]string) {
	pluginBadgesMu.Lock()
	pluginBadges = tags
	pluginBadgesMu.Unlock()
}

// TypeBadge is the badge of a service type.
func TypeBadge(serviceType string) Badge {
	p := Active()
	switch serviceType {
	case "docker":
		return Badge{"dock", p.Cyan}
	case "compose":
		return Badge{"comp", p.Accent2}
	case "external":
		return Badge{"ext", p.Orange}
	case "process", "":
		return Badge{"proc", p.Accent}
	}
	pluginBadgesMu.RLock()
	tag, ok := pluginBadges[serviceType]
	pluginBadgesMu.RUnlock()
	if !ok {
		tag = serviceType
	}
	return Badge{truncateRunes(tag, 4), p.Yellow}
}

// ServiceColor is a stable colour per service name, for prefixes in the combined log view.
func ServiceColor(name string, names []string) string {
	p := Active()
	i := 0
	for j, n := range names {
		if n == name {
			i = j
			break
		}
	}
	return p.Services[i%len(p.Services)]
}

// ---------------------------------------------------------------- charts

const bars = "▁▂▃▄▅▆▇█"

var barRunes = []rune(bars)

// Sparkline is a one-row chart of the last `width` values, newest on the right; max 0 means the largest value.
func Sparkline(values []float64, width int, max float64) string {
	if len(values) > width {
		values = values[len(values)-width:]
	}
	top := max
	if top <= 0 {
		top = 1e-9
		for _, v := range values {
			top = math.Max(top, v)
		}
	}
	var b strings.Builder
	for _, v := range values {
		i := int(math.Round(v / top * float64(len(barRunes)-1)))
		b.WriteRune(barRunes[min(len(barRunes)-1, max0(i))])
	}
	return padLeft(b.String(), width, ' ')
}

func max0(i int) int {
	if i < 0 {
		return 0
	}
	return i
}

func padLeft(s string, width int, pad rune) string {
	n := len([]rune(s))
	if n >= width {
		return s
	}
	return strings.Repeat(string(pad), width-n) + s
}

// AreaChart is a multi-row bar chart, newest value on the right. It returns `rows` strings of exactly `width`
// columns, top row first; max 0 means the largest value.
func AreaChart(values []float64, width, rows int, max float64) []string {
	if len(values) > width {
		values = values[len(values)-width:]
	}
	top := max
	if top <= 0 {
		top = 1e-9
		for _, v := range values {
			top = math.Max(top, v)
		}
	}
	levels := make([]int, len(values))
	for i, v := range values {
		levels[i] = int(math.Round(math.Min(1, math.Max(0, v/top)) * float64(rows*8)))
	}
	var out []string
	for r := rows - 1; r >= 0; r-- {
		var b strings.Builder
		for _, l := range levels {
			cell := l - r*8
			switch {
			case cell >= 8:
				b.WriteRune('█')
			case cell > 0:
				b.WriteRune(barRunes[cell-1])
			case r == 0:
				b.WriteRune('▁')
			default:
				b.WriteRune(' ')
			}
		}
		pad := ' '
		if r == 0 {
			pad = '▁'
		}
		out = append(out, padLeft(b.String(), width, pad))
	}
	return out
}

// ---------------------------------------------------------------- text

// Fit cuts s with … or pads it with spaces to exactly width columns.
func Fit(s string, width int) string {
	if width <= 0 {
		return ""
	}
	w := ansi.StringWidth(s)
	if w > width {
		s = ansi.Truncate(s, width, "…")
		w = ansi.StringWidth(s)
	}
	return s + strings.Repeat(" ", max(0, width-w))
}

// FitMid is Fit for names: a name that does not fit loses its middle, not its end, because the end is usually what
// tells two similar names apart (api-gateway-pr…eu-1 against api-gateway-pr…us-2).
func FitMid(s string, width int) string {
	if width <= 0 {
		return ""
	}
	w := ansi.StringWidth(s)
	if w <= width {
		return s + strings.Repeat(" ", width-w)
	}
	if width < 5 {
		return Fit(s, width)
	}
	head := ansi.Truncate(s, width/2, "")
	r := []rune(s)
	tailW, i := width-1-ansi.StringWidth(head), len(r)
	for i > 0 && ansi.StringWidth(string(r[i-1:])) <= tailW {
		i--
	}
	return Fit(head+"…"+string(r[i:]), width)
}

// Mix blends colour over base (both #rrggbb), amount 0..1 of colour: a tint for diff backgrounds.
func Mix(base, color string, amount float64) string {
	ch := func(hex string, i int) float64 {
		n, _ := strconv.ParseUint(hex[1+i*2:3+i*2], 16, 8)
		return float64(n)
	}
	out := "#"
	for i := 0; i < 3; i++ {
		out += fmt.Sprintf("%02x", int(math.Round(ch(base, i)*(1-amount)+ch(color, i)*amount)))
	}
	return out
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
