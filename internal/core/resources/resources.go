// Package resources keeps an hour of cpu / memory per service and judges it: memory against its limit, and
// sustained growth (a leak).
package resources

import (
	"math"
	"sort"
)

const (
	// BucketMs: 10 s per bucket, 360 buckets = 1 h of history per service.
	BucketMs = 10_000
	Buckets  = 360
)

// Bucket is one stretch of samples.
type Bucket struct {
	// T is the start of the bucket (ms since epoch)
	T      int64   `json:"t"`
	Cpu    float64 `json:"cpu"`
	CpuMax float64 `json:"cpuMax"`
	Mem    float64 `json:"mem"`
	MemMin float64 `json:"memMin"`
	MemMax float64 `json:"memMax"`
}

type MemLevel string

const (
	LevelNone MemLevel = ""
	LevelWarn MemLevel = "warn"
	LevelOver MemLevel = "over"
)

// Leak is sustained memory growth.
type Leak struct {
	// PerMin is the bytes of growth per minute
	PerMin float64 `json:"perMin"`
	// Since is when the sustained growth started (ms since epoch)
	Since float64 `json:"since"`
	// EtaMs is the ms until the memory limit is reached at this rate, when there is a limit
	EtaMs float64 `json:"etaMs,omitempty"`
}

// Info is what the UI shows about a service's memory; nil when there is nothing to say.
type Info struct {
	MemLimit int64    `json:"memLimit,omitempty"`
	Level    MemLevel `json:"level,omitempty"`
	Leak     *Leak    `json:"leak,omitempty"`
}

const (
	warnAt  = 0.85
	warnOff = 0.8
)

// Level is memory against its limit; prev gives hysteresis so a service hovering at 85 % does not flap.
func Level(mem float64, limit int64, prev MemLevel) MemLevel {
	if limit <= 0 {
		return LevelNone
	}
	ratio := mem / float64(limit)
	switch {
	case ratio >= 1:
		return LevelOver
	case ratio >= warnAt:
		return LevelWarn
	case prev != LevelNone && ratio >= warnOff:
		return LevelWarn
	}
	return LevelNone
}

// History aggregates 2 s samples into fixed buckets, per service.
type History struct {
	rings  map[string][]Bucket
	counts map[string]int
}

func NewHistory() *History {
	return &History{rings: map[string][]Bucket{}, counts: map[string]int{}}
}

func (h *History) Push(name string, cpu, mem float64, at int64) {
	t := at / BucketMs * BucketMs
	ring := h.rings[name]
	if n := len(ring); n > 0 && ring[n-1].T == t {
		last := &ring[n-1]
		c := float64(h.counts[name] + 1)
		if h.counts[name] == 0 {
			c = 2
		}
		h.counts[name] = int(c)
		last.Cpu += (cpu - last.Cpu) / c
		last.Mem += (mem - last.Mem) / c
		last.CpuMax = math.Max(last.CpuMax, cpu)
		last.MemMin = math.Min(last.MemMin, mem)
		last.MemMax = math.Max(last.MemMax, mem)
		return
	}
	h.counts[name] = 1
	ring = append(ring, Bucket{T: t, Cpu: cpu, CpuMax: cpu, Mem: mem, MemMin: mem, MemMax: mem})
	if len(ring) > Buckets {
		ring = ring[1:]
	}
	h.rings[name] = ring
}

// Buckets are the buckets newer than sinceMs ago (all of them when sinceMs <= 0), oldest first.
func (h *History) Buckets(name string, sinceMs, now int64) []Bucket {
	ring := h.rings[name]
	if sinceMs <= 0 {
		return append([]Bucket(nil), ring...)
	}
	var out []Bucket
	for _, b := range ring {
		if b.T >= now-sinceMs {
			out = append(out, b)
		}
	}
	return out
}

func (h *History) Clear(name string) {
	delete(h.rings, name)
	delete(h.counts, name)
}

const (
	minMs         = 60_000
	LeakMinData   = 15 * minMs
	leakWindow    = 30 * minMs
	minGrowth     = 20 * 1024 * 1024
	leakMinPoints = 10
)

// DetectLeak looks for sustained memory growth. It looks at the per-minute floor of memory (a garbage collector's
// sawtooth does not move it) over the last 30 minutes, and asks for a clean upward line: a high R², a real amount
// of growth and almost no minute-to-minute drops. buckets must only hold the current run of the service.
func DetectLeak(buckets []Bucket, limit int64) *Leak {
	if len(buckets) < 2 {
		return nil
	}
	end := buckets[len(buckets)-1].T
	if end-buckets[0].T < LeakMinData {
		return nil
	}
	byMinute := map[int64]float64{}
	for _, b := range buckets {
		if b.T < end-leakWindow {
			continue
		}
		minute := b.T / minMs
		if cur, ok := byMinute[minute]; !ok || b.MemMin < cur {
			byMinute[minute] = b.MemMin
		}
	}
	type pt struct{ x, y float64 }
	minutes := make([]int64, 0, len(byMinute))
	for m := range byMinute {
		minutes = append(minutes, m)
	}
	sort.Slice(minutes, func(i, j int) bool { return minutes[i] < minutes[j] })
	points := make([]pt, len(minutes))
	for i, m := range minutes {
		points[i] = pt{float64(m), byMinute[m]}
	}
	if len(points) < leakMinPoints {
		return nil
	}
	n := float64(len(points))
	var mx, my float64
	for _, p := range points {
		mx += p.x
		my += p.y
	}
	mx /= n
	my /= n
	var sxx, sxy, syy float64
	for _, p := range points {
		sxx += (p.x - mx) * (p.x - mx)
		sxy += (p.x - mx) * (p.y - my)
		syy += (p.y - my) * (p.y - my)
	}
	if sxx == 0 || syy == 0 {
		return nil
	}
	slope := sxy / sxx // bytes per minute
	if slope <= 0 {
		return nil
	}
	if r2 := sxy * sxy / (sxx * syy); r2 < 0.8 {
		return nil
	}
	first, last := points[0].y, points[len(points)-1].y
	if last-first < math.Max(first*0.1, minGrowth) {
		return nil
	}
	rising := 0
	for i := 1; i < len(points); i++ {
		if points[i].y >= points[i-1].y {
			rising++
		}
	}
	if float64(rising)/float64(len(points)-1) < 0.7 {
		return nil
	}
	leak := &Leak{PerMin: slope, Since: points[0].x * minMs}
	if limit > 0 && last < float64(limit) {
		leak.EtaMs = (float64(limit) - last) / slope * minMs
	}
	return leak
}

type Range string

const (
	Range2m  Range = "2m"
	Range15m Range = "15m"
	Range1h  Range = "1h"
)

var Ranges = []Range{Range2m, Range15m, Range1h}

var RangeMs = map[Range]int64{Range2m: 120_000, Range15m: 900_000, Range1h: 3_600_000}

// Resample averages values down to at most n points (a chart is only as wide as its panel), keeping the newest on
// the right.
func Resample(values []float64, n int) []float64 {
	if n <= 0 {
		return []float64{}
	}
	if len(values) <= n {
		return append([]float64(nil), values...)
	}
	out := make([]float64, 0, n)
	step := float64(len(values)) / float64(n)
	for i := 0; i < n; i++ {
		from := int(math.Floor(float64(i) * step))
		to := max(from+1, int(math.Floor(float64(i+1)*step)))
		sum := 0.0
		for _, v := range values[from:to] {
			sum += v
		}
		out = append(out, sum/float64(to-from))
	}
	return out
}
