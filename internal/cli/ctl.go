package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/core/logs"
	"github.com/JGoterris/orbit/internal/core/metrics"
	"github.com/JGoterris/orbit/internal/core/supervisor"
	"github.com/JGoterris/orbit/internal/ipc"
	"github.com/JGoterris/orbit/internal/state"
)

const notRunning = "orbit is not running for this project (open it with `orbit`, or `orbit up -d` in the background)"

// connectIPC connects to the orbit that has this project open (TUI, `orbit up` or daemon), if any.
func connectIPC(cfg *config.OrbitConfig) *ipc.Client {
	c, err := ipc.Connect(ipc.SocketPath(state.Dir(cfg)), 500*time.Millisecond)
	if err != nil {
		return nil
	}
	return c
}

func age(ms int64) string {
	s := max(0, (ms+500)/1000)
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm%02ds", s/60, s%60)
	}
	return fmt.Sprintf("%dh%02dm", s/3600, s%3600/60)
}

// RunStatus shows the state of the services of an orbit that is running (`orbit status`).
func RunStatus(cfg *config.OrbitConfig, asJSON bool) int {
	client := connectIPC(cfg)
	if client == nil {
		if asJSON {
			b, _ := json.Marshal(map[string]any{"running": false, "project": cfg.Name, "services": []any{}})
			fmt.Println(string(b))
		} else {
			fmt.Fprintln(os.Stderr, dim(notRunning))
		}
		return 1
	}
	defer client.Close()
	var services []supervisor.ServiceState
	if err := client.Call(ipc.MethodSnapshot, nil, &services); err != nil {
		fmt.Fprintln(os.Stderr, red(err.Error()))
		return 1
	}
	if asJSON {
		b, _ := json.MarshalIndent(map[string]any{"running": true, "project": cfg.Name, "services": services}, "", "  ")
		fmt.Println(string(b))
		return 0
	}
	now := time.Now().UnixMilli()
	memOf := func(s supervisor.ServiceState) string {
		if len(s.Mem) == 0 || s.StartedAt == 0 {
			return ""
		}
		out := metrics.FormatBytes(s.Mem[len(s.Mem)-1])
		if r := s.Resources; r != nil {
			if r.MemLimit > 0 {
				out += "/" + metrics.FormatBytes(float64(r.MemLimit))
			}
			if r.Level == "over" {
				out += " !"
			}
			if r.Leak != nil {
				out += " ↗"
			}
		}
		return out
	}
	header := []string{"SERVICE", "STATUS", "PID", "UP", "MEM", "DETAIL"}
	var rows [][]string
	for _, s := range services {
		pid, up := "", ""
		if s.Pid != 0 {
			pid = fmt.Sprint(s.Pid)
			if s.StartedAt != 0 {
				up = age(now - s.StartedAt)
			}
		}
		detail := s.Error
		if detail == "" {
			detail = s.Health
		}
		rows = append(rows, []string{s.Name, string(s.Status), pid, up, memOf(s), detail})
	}
	widths := make([]int, len(header))
	for i, h := range header {
		widths[i] = len(h)
		for _, r := range rows {
			widths[i] = max(widths[i], len([]rune(r[i])))
		}
	}
	cells := make([]string, len(header))
	for i, h := range header {
		cells[i] = padEnd(h, widths[i])
	}
	fmt.Println(bold(strings.Join(cells, "  ")))
	for i, r := range rows {
		out := make([]string, len(r))
		for j, v := range r {
			out[j] = padEnd(v, widths[j])
			if j == 1 {
				if paint := statusColor[services[i].Status]; paint != nil {
					out[j] = paint(out[j])
				}
			}
		}
		fmt.Println(strings.TrimRight(strings.Join(out, "  "), " "))
	}
	return 0
}

func printDiff(diff config.ConfigDiff) {
	for _, line := range diff.Describe() {
		paint := cyan
		switch {
		case strings.HasPrefix(line, "+"):
			paint = green
		case strings.HasPrefix(line, "-"):
			paint = red
		case strings.HasPrefix(line, "!"):
			paint = yellow
		}
		fmt.Printf("  %s\n", paint(line))
	}
}

// runReload is `orbit ctl reload`: the running orbit re-reads orbit.yaml and applies the difference. With
// sendConfig (a --profile was given) it applies the configuration loaded here instead, which is how the profiles of
// a running orbit change.
func runReload(cfg *config.OrbitConfig, sendConfig bool) int {
	client := connectIPC(cfg)
	if client == nil {
		fmt.Fprintln(os.Stderr, red(notRunning))
		return 1
	}
	defer client.Close()
	var res struct {
		Diff config.ConfigDiff `json:"diff"`
	}
	var params any
	if sendConfig {
		params = map[string]any{"config": cfg}
	}
	if err := client.Call(ipc.MethodReload, params, &res); err != nil {
		fmt.Fprintln(os.Stderr, red(err.Error()))
		return 1
	}
	if res.Diff.IsEmpty() {
		fmt.Println(dim("orbit.yaml: nothing to apply"))
	} else {
		fmt.Println(green("orbit.yaml applied"))
		printDiff(res.Diff)
	}
	return 0
}

var ctlActions = []string{"start", "stop", "restart", "toggle"}

// RunCtl drives the services of a running orbit (`orbit ctl <action> [service or group…]`).
func RunCtl(cfg *config.OrbitConfig, args []string, sendConfig bool) int {
	var action string
	var names []string
	if len(args) > 0 {
		action, names = args[0], args[1:]
	}
	if action == "reload" {
		if len(names) > 0 {
			fmt.Fprintln(os.Stderr, red("usage: orbit ctl reload"))
			return 1
		}
		return runReload(cfg, sendConfig)
	}
	if action == "" || !slices.Contains(ctlActions, action) {
		fmt.Fprintln(os.Stderr, red(fmt.Sprintf("usage: orbit ctl <%s|reload> [service or group…]", strings.Join(ctlActions, "|"))))
		return 1
	}
	if n, bad := unknownTargets(cfg, names); bad {
		fmt.Fprintln(os.Stderr, red(fmt.Sprintf("unknown service or group %q", n)))
		return 1
	}
	if len(names) == 0 && action != "start" && action != "stop" {
		fmt.Fprintln(os.Stderr, red(action+" needs at least one service or group"))
		return 1
	}
	client := connectIPC(cfg)
	if client == nil && action != "stop" {
		// starting something with nothing running: start the daemon in the background, as `orbit up -d` does
		if err := ipc.EnsureDaemon(cfg); err != nil {
			fmt.Fprintln(os.Stderr, red(err.Error()))
			return 1
		}
		fmt.Println(dim("started orbit in the background"))
		client = connectIPC(cfg)
	}
	if client == nil {
		fmt.Fprintln(os.Stderr, red(notRunning))
		return 1
	}
	defer client.Close()
	var res struct {
		OK bool `json:"ok"`
	}
	var err error
	if len(names) > 0 {
		err = client.Call(action, map[string]any{"services": names}, &res)
	} else if action == "start" {
		err = client.Call(ipc.MethodStartAll, nil, &res)
	} else {
		err = client.Call(ipc.MethodStopAll, nil, &res)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, red(err.Error()))
		return 1
	}
	label := "all services"
	if len(names) > 0 {
		label = strings.Join(names, ", ")
	}
	if res.OK {
		fmt.Println(green(fmt.Sprintf("%s: %s", action, label)))
		return 0
	}
	fmt.Println(red(fmt.Sprintf("%s: %s did not become ready (see `orbit logs`)", action, label)))
	return 1
}

// upRunning is `orbit up` while orbit already runs the project: like `docker compose up` on a running stack, it
// applies what changed in orbit.yaml (asking first on a terminal) and starts what was asked for.
func upRunning(cfg *config.OrbitConfig, names []string, detached bool) int {
	client := connectIPC(cfg)
	if client == nil {
		fmt.Fprintln(os.Stderr, red(notRunning))
		return 1
	}
	defer client.Close()
	var hello ipc.Hello
	if err := client.Call(ipc.MethodHello, nil, &hello); err != nil {
		fmt.Fprintln(os.Stderr, red(err.Error()))
		return 1
	}
	diff := config.DiffConfig(hello.Config, cfg)
	if !diff.IsEmpty() {
		fmt.Println(bold("orbit.yaml differs from the running orbit:"))
		printDiff(diff)
		// no terminal to ask on (a script, CI): apply, as compose does
		answer := "y"
		if st, err := os.Stdin.Stat(); err == nil && st.Mode()&os.ModeCharDevice != 0 {
			fmt.Print("apply? [Y/n] ")
			var in string
			fmt.Scanln(&in)
			answer = strings.ToLower(strings.TrimSpace(in))
		}
		if answer == "" || answer == "y" || answer == "yes" {
			if err := client.Call(ipc.MethodReload, map[string]any{"config": cfg}, nil); err != nil {
				fmt.Fprintln(os.Stderr, red(err.Error()))
				return 1
			}
			fmt.Println(green("applied"))
		}
	}
	var res struct {
		OK bool `json:"ok"`
	}
	var err error
	label := "all services"
	if len(names) > 0 {
		label = strings.Join(names, ", ")
		err = client.Call(ipc.MethodStart, map[string]any{"services": names}, &res)
	} else {
		err = client.Call(ipc.MethodStartAll, nil, &res)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, red(err.Error()))
		return 1
	}
	if res.OK {
		fmt.Println(green("up: " + label))
	} else {
		fmt.Println(red("some services did not become ready (see `orbit logs`)"))
	}
	if detached {
		fmt.Println(dim("orbit runs in the background: `orbit status` shows the services, `orbit logs` their output, `orbit down` stops them"))
	} else {
		fmt.Println(dim("orbit is already running for this project: `orbit logs -f` follows the services, `orbit down` stops them"))
	}
	if res.OK {
		return 0
	}
	return 1
}

// followLive follows the logs of a running orbit over its socket.
func followLive(client *ipc.Client, targets []string, opts LogsOptions, show func(logs.Line)) int {
	if opts.Since != "" {
		fmt.Fprintln(os.Stderr, yellow("--since is ignored when following a running orbit"))
	}
	var mu chan struct{} = make(chan struct{}, 1)
	mu <- struct{}{}
	last := 0
	var buffered []logs.Line
	replaying := true
	client.OnNotification(func(m ipc.Message) {
		if m.Method != ipc.NotifyLog {
			return
		}
		var l logs.Line
		if json.Unmarshal(m.Params, &l) != nil {
			return
		}
		<-mu
		defer func() { mu <- struct{}{} }()
		if replaying {
			buffered = append(buffered, l)
		} else if l.Seq > last {
			last = l.Seq
			show(l)
		}
	})
	if err := client.Call(ipc.MethodSubscribe, map[string]any{"states": false, "logs": true, "services": targets}, nil); err != nil {
		fmt.Fprintln(os.Stderr, red(err.Error()))
		return 1
	}
	var history []logs.Line
	for _, t := range targets {
		var part []logs.Line
		if err := client.Call(ipc.MethodLogs, map[string]any{"service": t, "limit": opts.Lines}, &part); err != nil {
			fmt.Fprintln(os.Stderr, red(err.Error()))
			return 1
		}
		history = append(history, part...)
	}
	slices.SortStableFunc(history, func(a, b logs.Line) int { return a.Seq - b.Seq })
	<-mu
	for _, l := range history {
		last = max(last, l.Seq)
		show(l)
	}
	replaying = false
	for _, l := range buffered {
		if l.Seq > last {
			last = l.Seq
			show(l)
		}
	}
	buffered = nil
	mu <- struct{}{}

	stop := make(chan struct{})
	go func() { waitSignal(); close(stop) }()
	select {
	case <-client.Done():
		fmt.Fprintln(os.Stderr, dim("orbit closed"))
	case <-stop:
		client.Close()
	}
	return 0
}
