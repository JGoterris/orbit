// Package metrics samples the cpu and memory of processes and containers.
package metrics

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/JGoterris/orbit/internal/platform"
	"github.com/JGoterris/orbit/internal/proc"
)

// Sample is one measurement.
type Sample struct {
	// Cpu is a percent of one core: a multithreaded service can pass 100
	Cpu float64
	// Mem in bytes
	Mem float64
	// Limit is the memory limit of the container, when it has one
	Limit int64
}

// ProcessSampler computes CPU% / RSS for whole process trees from successive process-table snapshots.
type ProcessSampler struct {
	last map[int]lastCPU
}

type lastCPU struct {
	cpuSeconds float64
	at         time.Time
}

func NewProcessSampler() *ProcessSampler { return &ProcessSampler{last: map[int]lastCPU{}} }

// Sample measures the trees rooted at roots. Roots that are gone are left out.
func (p *ProcessSampler) Sample(roots []int) map[int]Sample {
	out := map[int]Sample{}
	if len(roots) == 0 {
		return out
	}
	table := platform.ProcessTable()
	children := map[int][]int{}
	for pid, row := range table {
		children[row.PPid] = append(children[row.PPid], pid)
	}
	now := time.Now()
	for _, root := range roots {
		if _, ok := table[root]; !ok {
			continue
		}
		var cpuSeconds, rss float64
		seen := map[int]bool{}
		stack := []int{root}
		for len(stack) > 0 {
			pid := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			row, ok := table[pid]
			if !ok || seen[pid] {
				continue
			}
			seen[pid] = true
			cpuSeconds += row.CPUSeconds
			rss += float64(row.RSS)
			stack = append(stack, children[pid]...)
		}
		cpu := 0.0
		if prev, ok := p.last[root]; ok {
			if dt := now.Sub(prev.at).Seconds(); dt > 0 {
				cpu = math.Max(0, (cpuSeconds-prev.cpuSeconds)/dt*100)
			}
		}
		p.last[root] = lastCPU{cpuSeconds, now}
		out[root] = Sample{Cpu: cpu, Mem: rss}
	}
	for pid := range p.last {
		if _, ok := table[pid]; !ok {
			delete(p.last, pid)
		}
	}
	return out
}

var units = map[string]float64{"b": 1, "kb": 1e3, "kib": 1024, "mb": 1e6, "mib": 1 << 20, "gb": 1e9, "gib": 1 << 30}
var sizeRe = regexp.MustCompile(`(?i)([\d.]+)\s*([a-z]+)`)

// ParseSize reads what `docker stats` prints (`12.5MiB`, `1GiB`, `800kB`); 0 when it is not a size.
func ParseSize(s string) float64 {
	m := sizeRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0
	}
	n, _ := strconv.ParseFloat(m[1], 64)
	mult, ok := units[strings.ToLower(m[2])]
	if !ok {
		mult = 1
	}
	return n * mult
}

// SampleContainers runs one `docker stats` for many containers. Keys are the ids passed in (prefix match).
func SampleContainers(ids []string) map[string]Sample {
	out := map[string]Sample{}
	if len(ids) == 0 {
		return out
	}
	argv := append([]string{"docker", "stats", "--no-stream", "--format", "{{json .}}"}, ids...)
	res := proc.Exec(argv, proc.Options{Timeout: 15 * time.Second})
	for _, line := range strings.Split(res.Stdout, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var row struct{ ID, CPUPerc, MemUsage string }
		if json.Unmarshal([]byte(line), &row) != nil {
			continue
		}
		id := ""
		for _, i := range ids {
			if strings.HasPrefix(i, row.ID) || strings.HasPrefix(row.ID, i) {
				id = i
				break
			}
		}
		if id == "" {
			continue
		}
		used, max, _ := strings.Cut(row.MemUsage, "/")
		cpu, _ := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(row.CPUPerc), "%"), 64)
		out[id] = Sample{Cpu: cpu, Mem: ParseSize(used), Limit: ContainerLimit(max, platform.TotalMemory())}
	}
	return out
}

// ContainerLimit is the second half of `MemUsage`; a container without a limit reports the host's memory, which is
// not one. 0 means no limit.
func ContainerLimit(text string, host int64) int64 {
	n := ParseSize(text)
	if n > 0 && (host <= 0 || n < float64(host)*0.95) {
		return int64(n)
	}
	return 0
}

func fixed(v float64, digits int) string { return strconv.FormatFloat(v, 'f', digits, 64) }

// FormatBytes is a short human size: `512B`, `10K`, `4.2M`, `1.5G`.
func FormatBytes(n float64) string {
	const k = 1024.0
	switch {
	case n < k:
		return fixed(n, 0) + "B"
	case n < k*k:
		return fixed(n/k, 0) + "K"
	case n < k*k*k:
		d := 0
		if n < 10*k*k {
			d = 1
		}
		return fixed(n/(k*k), d) + "M"
	}
	return fixed(n/(k*k*k), 1) + "G"
}

// FormatDuration is a short elapsed time: `45s`, `3m05s`, `2h10m`, `1d4h`.
func FormatDuration(ms float64) string {
	s := int(math.Floor(ms / 1000))
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	m := s / 60
	if m < 60 {
		return fmt.Sprintf("%dm%02ds", m, s%60)
	}
	h := m / 60
	if h < 24 {
		return fmt.Sprintf("%dh%02dm", h, m%60)
	}
	return fmt.Sprintf("%dd%dh", h/24, h%24)
}
