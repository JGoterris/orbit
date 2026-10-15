package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/core/logs"
	"github.com/JGoterris/orbit/internal/core/metrics"
	"github.com/JGoterris/orbit/internal/core/resources"
	"github.com/JGoterris/orbit/internal/core/supervisor"
	"github.com/JGoterris/orbit/internal/ui/theme"
)

func nowMs() int64 { return time.Now().UnixMilli() }

// baseRows are the lines every service shows: status, what, fields, needs, spacer, cpu/mem, error.
const baseRows = 7

type item struct{ label, value string }

// packItems greedily packs `label value` items into lines of at most width columns.
func packItems(items []item, width int) [][]item {
	lines := [][]item{{}}
	used := 0
	for _, it := range items {
		valW := max(4, min(len([]rune(it.value)), width-len(it.label)-4))
		value := strings.TrimRight(theme.Fit(it.value, valW), " ")
		w := len(it.label) + len([]rune(value)) + 4
		if used+w > width && len(lines[len(lines)-1]) > 0 {
			lines = append(lines, nil)
			used = 0
		}
		lines[len(lines)-1] = append(lines[len(lines)-1], item{it.label, value})
		used += w
	}
	return lines
}

func stats(vs []float64, format func(float64) string) string {
	if len(vs) == 0 {
		return "no samples"
	}
	sum, mx := 0.0, vs[0]
	for _, v := range vs {
		sum += v
		mx = max(mx, v)
	}
	return fmt.Sprintf("now %s · avg %s · max %s", format(vs[len(vs)-1]), format(sum/float64(len(vs))), format(mx))
}

func maxOf(vs []float64, floor float64) float64 {
	m := floor
	for _, v := range vs {
		m = max(m, v)
	}
	return m
}

func (m *Model) headerLine(label string, width int) string {
	p := theme.Active()
	return newLine(p.Panel).add(p.Border, "── ").add(p.Muted, label).
		add(p.Border, " "+strings.Repeat("─", max(0, width-len(label)-4))).String()
}

func field(l *line, label, value, color string) {
	p := theme.Active()
	l.add(p.Dim, label+" ").add(color, value).add(p.Dim, "   ")
}

// renderDetail is the detail panel of a service. rows is the inner height available.
func (m *Model) renderDetail(name string, w, h int, focused, expanded bool, rows int) []string {
	p := theme.Active()
	svc := m.sup.Service(name)
	if svc == nil {
		return panel(panelOpts{title: name, focused: focused, padX: 1}, w, h, nil)
	}
	st := m.sup.State(name)
	cfg := m.sup.Config()
	style := theme.StyleFor(string(st.Status), svc.Oneshot)
	badge := theme.TypeBadge(string(svc.Type))
	inner := max(20, w-4)
	up := st.StartedAt != 0 && supervisor.IsUp(st.Status)
	uptime := ""
	switch {
	case up:
		uptime = metrics.FormatDuration(float64(nowMs() - st.StartedAt))
	case st.StoppedAt != 0:
		uptime = metrics.FormatDuration(float64(nowMs()-st.StoppedAt)) + " ago"
	}

	rel := func(path string) string {
		if r, err := filepath.Rel(cfg.Root, path); err == nil && r != "" {
			return r
		}
		return path
	}
	var what string
	switch svc.Type {
	case config.TypeExternal:
		what = "external · " + svc.Health.Describe()
	case config.TypeProcess:
		what = "$ " + svc.Cmd
	case config.TypeDocker:
		what = "image " + svc.Image
		if svc.Cmd != "" {
			what += " · " + svc.Cmd
		}
	default:
		what = fmt.Sprintf("compose %s · %s", rel(svc.ComposeFile), svc.ComposeService)
	}
	cwd := rel(svc.Cwd)
	if cwd == "" || cwd == "." {
		cwd = "."
	}
	users := m.sup.Dependents()[name]

	sparkW := max(10, (inner-36)/2)
	var cpuNow, memNow float64
	if len(st.CPU) > 0 {
		cpuNow = st.CPU[len(st.CPU)-1]
	}
	if len(st.Mem) > 0 {
		memNow = st.Mem[len(st.Mem)-1]
	}
	res := st.Resources
	memColor := p.Text
	var memText string
	if res != nil {
		switch res.Level {
		case resources.LevelOver:
			memColor = p.Red
		case resources.LevelWarn:
			memColor = p.Orange
		}
	}
	if res != nil && res.MemLimit > 0 {
		memText = fmt.Sprintf("%s/%s %d%%", metrics.FormatBytes(memNow), metrics.FormatBytes(float64(res.MemLimit)), int(memNow/float64(res.MemLimit)*100+0.5))
	} else {
		memText = padStart(metrics.FormatBytes(memNow), 6)
	}

	// 2m is what was sampled live; longer ranges come from the history buckets (averaged down to the chart's width)
	cpuSeries, memSeries := st.CPU, st.Mem
	longer := m.resRange != resources.Range2m && m.historyFor == name && m.historyRng == m.resRange && len(m.history) > 0
	if longer {
		cpuSeries = make([]float64, len(m.history))
		memSeries = make([]float64, len(m.history))
		for i, b := range m.history {
			cpuSeries[i], memSeries[i] = b.Cpu, b.Mem
		}
	}
	var memTop float64
	if res != nil && res.MemLimit > 0 {
		memTop = maxOf(memSeries, float64(res.MemLimit))
	}

	// ---- extra blocks, in priority order, as far as the height allows
	extra := max(0, rows-baseRows)
	var items []item
	if svc.Health != nil {
		items = append(items, item{"check", fmt.Sprintf("every %s · timeout %s", metrics.FormatDuration(float64(svc.Health.Interval)), metrics.FormatDuration(float64(svc.Health.Timeout)))})
	}
	if svc.ReadyWhen != nil {
		items = append(items, item{"ready when", fmt.Sprintf("log %q", svc.ReadyWhen.Log)})
	}
	items = append(items,
		item{"start_timeout", metrics.FormatDuration(float64(svc.StartTimeout))},
		item{"stop_timeout", metrics.FormatDuration(float64(svc.StopTimeout))})
	if svc.Autostart {
		items = append(items, item{"autostart", "yes"})
	} else {
		items = append(items, item{"autostart", "no"})
	}
	if svc.Oneshot {
		items = append(items, item{"oneshot", "yes"})
	}
	if svc.Hooks != nil {
		for _, ph := range []struct {
			label string
			hooks []config.Hook
		}{{"pre_start", svc.Hooks.PreStart}, {"post_start", svc.Hooks.PostStart}, {"post_stop", svc.Hooks.PostStop}} {
			if len(ph.hooks) > 0 {
				cmds := make([]string, len(ph.hooks))
				for i, h := range ph.hooks {
					cmds[i] = h.Cmd
				}
				items = append(items, item{ph.label, strings.Join(cmds, " · ")})
			}
		}
	}
	if svc.URL != "" {
		items = append(items, item{"url", svc.URL})
	}
	if len(svc.Ports) > 0 {
		items = append(items, item{"ports", strings.Join(svc.Ports, ", ")})
	}
	if len(svc.Volumes) > 0 {
		items = append(items, item{"volumes", strings.Join(svc.Volumes, ", ")})
	}
	if len(svc.DockerArgs) > 0 {
		items = append(items, item{"args", strings.Join(svc.DockerArgs, " ")})
	}
	if svc.ComposeProject != "" {
		items = append(items, item{"project", svc.ComposeProject})
	}
	if st.ExitCode != nil {
		items = append(items, item{"exit", fmt.Sprint(*st.ExitCode)})
	}
	// each block gets a one-row header so they stay distinguishable (charts are empty while stopped)
	var configLines [][]item
	if extra >= 2 {
		configLines = packItems(items, inner)
		if len(configLines) > extra-1 {
			configLines = configLines[:extra-1]
		}
	}
	rem := extra
	if len(configLines) > 0 {
		rem = extra - (len(configLines) + 1)
	}
	chartExtra := 0
	if rem >= 3 {
		chartExtra = min(rem-1, max(2, min(7, (rem+1)/2)))
	}
	eventRows := 0
	if left := rem - func() int {
		if chartExtra > 0 {
			return chartExtra + 1
		}
		return 0
	}(); left >= 2 {
		eventRows = left
	}
	var events []logs.Line
	if eventRows > 0 {
		for _, l := range m.sup.LogStore().Lines(name) {
			if l.Stream != logs.Stdout {
				events = append(events, l)
			}
		}
		if n := eventRows - 1; len(events) > n {
			events = events[len(events)-n:]
		}
	}

	chartW := max(10, (inner-12)/2)
	var cpuChart, memChart []string
	if chartExtra > 0 {
		cpuChart = theme.AreaChart(resources.Resample(cpuSeries, chartW), chartW, chartExtra, maxOf(cpuSeries, 100))
		memChart = theme.AreaChart(resources.Resample(memSeries, chartW), chartW, chartExtra, memTop)
	}

	status := func(n string) (string, string) {
		s := m.sup.Service(n)
		stl := theme.StyleFor(string(m.sup.State(n).Status), s != nil && s.Oneshot)
		return stl.Color, stl.Icon
	}
	var out []string
	add := func(l *line) { out = append(out, l.String()) }

	l := newLine(p.Panel)
	l.add(style.Color, style.Icon+" ").bold(style.Color, style.Label)
	if uptime != "" {
		l.add(p.Muted, "  "+uptime)
	}
	l.add(p.Dim, "   ").add(badge.Color, string(svc.Type))
	if svc.Description != "" {
		l.add(p.Dim, "  "+svc.Description)
	}
	add(l)

	l = newLine(p.Panel)
	l.add(p.Accent2, theme.Fit(what, max(10, inner-len([]rune(cwd))-6))).add(p.Dim, "  in ").add(p.Muted, cwd)
	add(l)

	l = newLine(p.Panel)
	if svc.Port != 0 {
		field(l, "port", fmt.Sprintf(":%d", svc.Port), p.Cyan)
	}
	hv := svc.Health.Describe()
	if st.Health != "" {
		hv += " (" + st.Health + ")"
	}
	field(l, "health", hv, p.Text)
	if st.Pid != 0 {
		field(l, "pid", fmt.Sprint(st.Pid), p.Text)
	}
	if st.ContainerID != "" {
		field(l, "container", st.ContainerID[:min(12, len(st.ContainerID))], p.Text)
	}
	field(l, "restart", string(svc.Restart), p.Text)
	if svc.Watch != nil {
		wv := "paused"
		wc := p.Muted
		if st.Watch != "paused" {
			wv = fmt.Sprintf("%s · %gs/%gs", strings.Join(svc.Watch.Paths, ", "), float64(svc.Watch.Debounce)/1000, float64(svc.Watch.Cooldown)/1000)
			if st.Watch == "pending" {
				wv += " · changes queued"
				wc = p.Yellow
			}
		}
		field(l, "watch", wv, wc)
	}
	if len(svc.EnvFiles) > 0 {
		files := make([]string, len(svc.EnvFiles))
		for i, f := range svc.EnvFiles {
			files[i] = rel(f.Path)
		}
		field(l, "env_file", strings.Join(files, ", "), p.Muted)
	}
	if st.Restarts > 0 {
		field(l, "restarts", fmt.Sprint(st.Restarts), p.Orange)
	}
	add(l)

	if len(configLines) > 0 {
		out = append(out, m.headerLine("config", inner))
		for _, cl := range configLines {
			l = newLine(p.Panel)
			for _, it := range cl {
				field(l, it.label, it.value, p.Muted)
			}
			add(l)
		}
	}

	l = newLine(p.Panel)
	l.add(p.Dim, "needs ")
	if len(svc.DependsOn) == 0 {
		l.add(p.Dim, "—")
	}
	for i, d := range svc.DependsOn {
		c, ic := status(d)
		sep := ""
		if i < len(svc.DependsOn)-1 {
			sep = "  "
		}
		l.add(c, ic+" "+d+sep)
	}
	l.add(p.Dim, "     used by ")
	if len(users) == 0 {
		l.add(p.Dim, "—")
	}
	for i, d := range users {
		c, ic := status(d)
		sep := ""
		if i < len(users)-1 {
			sep = "  "
		}
		l.add(c, ic+" "+d+sep)
	}
	add(l)
	out = append(out, blank(inner, p.Panel))

	if chartExtra > 0 {
		out = append(out, m.headerLine(fmt.Sprintf("usage · %s (h)", m.resRange), inner))
		for i := range cpuChart {
			l = newLine(p.Panel)
			if i == 0 {
				l.add(p.Dim, "cpu ")
			} else {
				l.add(p.Dim, "    ")
			}
			l.add(p.Green, cpuChart[i])
			if i == 0 {
				l.add(p.Dim, "    mem ")
			} else {
				l.add(p.Dim, "        ")
			}
			l.add(p.Accent, memChart[i])
			add(l)
		}
		l = newLine(p.Panel)
		l.add(p.Muted, "    "+theme.Fit(stats(cpuSeries, func(n float64) string { return fmt.Sprintf("%.1f%%", n) }), chartW+4)).
			add(p.Muted, "    "+theme.Fit(stats(memSeries, func(n float64) string { return metrics.FormatBytes(n) }), chartW))
		add(l)
	} else {
		l = newLine(p.Panel)
		l.add(p.Dim, "cpu ").add(p.Green, theme.Sparkline(resources.Resample(cpuSeries, sparkW), sparkW, maxOf(cpuSeries, 100))).
			add(p.Text, fmt.Sprintf(" %5.1f%%", cpuNow)).
			add(p.Dim, "     mem ").add(p.Accent, theme.Sparkline(resources.Resample(memSeries, sparkW), sparkW, memTop)).
			add(memColor, " "+memText)
		add(l)
	}

	if eventRows > 0 {
		out = append(out, m.headerLine("recent", inner))
		if len(events) == 0 {
			out = append(out, newLine(p.Panel).add(p.Dim, "no recent events").String())
		}
		for _, e := range events {
			c := p.Muted
			if e.Stream == logs.Stderr {
				c = p.Red
			}
			add(newLine(p.Panel).add(p.Dim, time.UnixMilli(e.Ts).Format("15:04:05")+" ").add(c, theme.Fit(e.Text, max(10, inner-9))))
		}
	}

	l = newLine(p.Panel)
	switch {
	case st.Error != "":
		l.add(p.Red, theme.Fit("✖ "+st.Error, inner))
	case len(st.WaitingOn) > 0:
		var waiting []string
		for _, d := range st.WaitingOn {
			if !m.sup.IsReady(d) {
				waiting = append(waiting, d)
			}
		}
		w := strings.Join(waiting, ", ")
		if w == "" {
			w = "dependencies"
		}
		l.add(p.Yellow, "waiting for "+w+"…")
	case res != nil && res.Level != resources.LevelNone:
		c, suffix := p.Orange, ""
		if res.Level == resources.LevelOver {
			c, suffix = p.Red, " — over its limit"
		}
		l.add(c, theme.Fit("▲ memory "+memText+suffix, inner))
	case res != nil && res.Leak != nil:
		s := fmt.Sprintf("↗ leak: +%s/min for %s", metrics.FormatBytes(res.Leak.PerMin), metrics.FormatDuration(float64(nowMs())-res.Leak.Since))
		if res.Leak.EtaMs > 0 {
			s += " · limit in ~" + metrics.FormatDuration(res.Leak.EtaMs)
		}
		l.add(p.Orange, theme.Fit(s, inner))
	}
	add(l)

	// the panel clips what does not fit, like overflow: hidden
	if !expanded && len(out) > h-2 {
		out = out[:max(0, h-2)]
	}
	return panel(panelOpts{title: name, focused: focused, padX: 1}, w, h, out)
}
