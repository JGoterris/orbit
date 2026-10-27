package cli

import (
	"encoding/json"
	"fmt"
	"github.com/JGoterris/orbit/internal/plugins"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/core/logs"
	"github.com/JGoterris/orbit/internal/core/notify"
	"github.com/JGoterris/orbit/internal/core/runner"
	"github.com/JGoterris/orbit/internal/core/supervisor"
	"github.com/JGoterris/orbit/internal/ipc"
	"github.com/JGoterris/orbit/internal/state"
)

// Version is the version orbit reports to its clients over the socket; main sets it.
var Version = "dev"

var (
	blue    = ansi("34")
	magenta = ansi("35")
)

var prefixColors = []func(string) string{blue, green, yellow, magenta, cyan}

var outMu sync.Mutex

func printf(format string, a ...any) {
	outMu.Lock()
	defer outMu.Unlock()
	fmt.Printf(format, a...)
}

// prefixer prints `svc │ text` with a stable colour per service, shared by `up` and `logs`.
type prefixer struct {
	names []string
	width int
}

func newPrefixer(names []string) *prefixer {
	w := 0
	for _, n := range names {
		w = max(w, cellWidth(n))
	}
	return &prefixer{names: names, width: w}
}

func (p *prefixer) color(name string) func(string) string {
	for i, n := range p.names {
		if n == name {
			return prefixColors[i%len(prefixColors)]
		}
	}
	return prefixColors[0]
}

func (p *prefixer) print(service string, stream logs.Stream, raw string) {
	text := raw
	switch stream {
	case logs.System:
		text = dim("» " + raw)
	case logs.Stderr:
		text = yellow(raw)
	}
	printf("%s %s\n", p.color(service)(padEnd(service, p.width)+" │"), text)
}

var statusColor = map[supervisor.Status]func(string) string{
	supervisor.Healthy: green, supervisor.Running: cyan, supervisor.Unhealthy: yellow,
	supervisor.Crashed: red, supervisor.Failed: red, supervisor.Stopped: dim,
}

func waitSignal() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	<-sigs
}

func unknownTargets(cfg *config.OrbitConfig, names []string) (string, bool) {
	for _, n := range names {
		if cfg.Services[n] == nil && cfg.Groups[n] == nil {
			return n, true
		}
	}
	return "", false
}

func expand(cfg *config.OrbitConfig, names []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, n := range names {
		members := []string{n}
		if g, ok := cfg.Groups[n]; ok {
			members = g
		}
		for _, m := range members {
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	return out
}

// RunUp starts services headless, streaming their logs, until interrupted (`orbit up`).
func RunUp(cfg *config.OrbitConfig, names []string, detach bool) int {
	if n, bad := unknownTargets(cfg, names); bad {
		errorln(fmt.Sprintf("unknown service or group %q", n))
		return 1
	}
	if ipc.IsServed(cfg) {
		return upRunning(cfg, names, detach)
	}
	// -d: the services run in an `orbit daemon` and this returns once they are ready
	if detach {
		if err := ipc.EnsureDaemon(cfg); err != nil {
			errorln(err.Error())
			return 1
		}
		return upRunning(cfg, names, true)
	}
	sup := supervisor.New(cfg, supervisor.Options{Plugins: plugins.ForProject(cfg, Version)})
	stopNotifier := notify.Attach(sup, notify.Options{})
	defer stopNotifier()

	pf := newPrefixer(sup.Names())
	sup.Logs.OnLine(func(l logs.Line) { pf.print(l.Service, l.Stream, l.Text) })
	sup.OnConfigPending(func(p *supervisor.PendingConfig) {
		switch {
		case p == nil:
		case p.Error != "":
			printf("%s\n", yellow("orbit.yaml changed but does not load: "+p.Error))
		case p.Diff != nil:
			printf("%s\n", yellow("orbit.yaml changed:"))
			for _, line := range p.Diff.Describe() {
				printf("  %s\n", line)
			}
			printf("%s\n", dim("`orbit ctl reload` in another terminal applies it"))
		}
	})
	var lastMu sync.Mutex
	last := map[string]supervisor.Status{}
	sup.OnChange(func(name string) {
		if name == "" {
			return
		}
		st := sup.State(name)
		lastMu.Lock()
		same := last[name] == st.Status
		last[name] = st.Status
		lastMu.Unlock()
		if same {
			return
		}
		if paint := statusColor[st.Status]; paint != nil {
			printf("%s %s\n", pf.color(name)(padEnd(name, pf.width)+" │"), paint("● "+string(st.Status)))
		}
	})

	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	var srv *ipc.Server
	var stopOnce sync.Once
	stopAll := func() {
		stopOnce.Do(func() {
			printf("%s", dim("\nstopping… (ctrl+c again to force)\n"))
			if srv != nil {
				srv.Close()
			}
			sup.Dispose()
			close(done)
		})
	}
	go func() {
		<-sigs
		go func() {
			<-sigs
			sup.KillAllSync()
			os.Exit(130)
		}()
		stopAll()
	}()

	sup.Init()
	srv = ipc.NewServer(sup, ipc.ServerOptions{Version: Version, OnShutdown: func(string) { go stopAll() }})
	if started, err := srv.Start(); err != nil || !started {
		fmt.Fprintln(os.Stderr, yellow("another orbit started serving this project meanwhile: `orbit status` / `orbit ctl` talk to that one"))
		srv = nil
	}
	targets := expand(cfg, names)
	go func() {
		if len(targets) > 0 {
			sup.StartMany(targets)
		} else {
			sup.StartAll()
		}
	}()
	<-done
	return 0
}

// RunDown stops what a previous orbit session left running (`orbit down`).
func RunDown(cfg *config.OrbitConfig) int {
	code := 0
	dir := state.Dir(cfg)
	// an orbit that is running (TUI, `orbit up` or daemon) answers on its socket: ask it to stop everything and quit
	if served := ipc.ServedDir(cfg); served != "" {
		dir = served
	}
	if client := connectIPC(cfg); client != nil {
		err := client.Call(ipc.MethodShutdown, map[string]any{"how": "stop"}, nil)
		client.Close()
		if err != nil {
			errorln(err.Error())
			return 1
		}
		for i := 0; i < 300; i++ {
			again := connectIPC(cfg)
			if again == nil {
				break
			}
			again.Close()
			time.Sleep(100 * time.Millisecond)
		}
		// the daemon closes its socket before stopping the services: wait for the lock too
		for i := 0; i < 300 && state.ReadLock(dir) != 0; i++ {
			time.Sleep(100 * time.Millisecond)
		}
		if left := connectIPC(cfg); left != nil || state.ReadLock(dir) != 0 {
			if left != nil {
				left.Close()
			}
			errorln("orbit is taking too long to stop")
			return 1
		}
		fmt.Println(green("orbit stopped"))
		return 0
	}
	if holder := state.ReadLock(dir); holder != 0 && holder != os.Getpid() {
		errorln(fmt.Sprintf("orbit is open for this project (pid %d) but does not answer: quit it first, or stop services from there", holder))
		return 1
	}
	said := false
	report := func(format string, a ...any) {
		said = true
		printf(format, a...)
	}
	// processes a previous orbit session left running
	saved := state.Read(dir)
	for _, name := range cfg.Order {
		svc := cfg.Services[name]
		entry, ok := saved.Services[name]
		if !ok || svc.Type != config.TypeProcess {
			delete(saved.Services, name)
			continue
		}
		r := runner.New(svc, cfg.Name, runner.Callbacks{Log: func(logs.Stream, string) {}, Exit: func(*int, string) {}}, state.Files(dir, name))
		if attached, _ := r.Attach(&entry); attached {
			r.Stop(svc.StopTimeout)
			report("%s %s %s\n", padEnd(name, 16), green("stopped"), dim(fmt.Sprintf("pid %d", entry.Pid)))
		}
		delete(saved.Services, name)
	}
	// services removed from orbit.yaml since they were started
	for name, entry := range saved.Services {
		if cfg.Services[name] != nil {
			continue
		}
		switch {
		case entry.Pid != 0 && runner.StopLeftover(name, entry, state.Files(dir, name)):
			report("%s %s %s\n", padEnd(name, 16), green("stopped"), dim(fmt.Sprintf("pid %d, no longer in orbit.yaml", entry.Pid)))
			delete(saved.Services, name)
		case entry.Pid == 0 && runner.RemoveLeftoverContainer(cfg.Name, name):
			report("%s %s %s\n", padEnd(name, 16), green("removed"), dim(runner.ContainerName(cfg.Name, name)+", no longer in orbit.yaml"))
			delete(saved.Services, name)
		}
	}
	state.Write(dir, saved)
	for _, name := range cfg.Order {
		svc := cfg.Services[name]
		switch svc.Type {
		case config.TypeDocker:
			cname := runner.ContainerName(cfg.Name, svc.Name)
			// docker rm -f also succeeds when there is no container
			status := dim("not running")
			if exec.Command("docker", "inspect", "-f", "{{.Id}}", cname).Run() == nil && exec.Command("docker", "rm", "-f", cname).Run() == nil {
				status = green("removed")
			}
			report("%s %s %s\n", padEnd(svc.Name, 16), status, dim(cname))
		case config.TypeCompose:
			argv := []string{"compose", "-f", svc.ComposeFile}
			if svc.ComposeProject != "" {
				argv = append(argv, "-p", svc.ComposeProject)
			}
			argv = append(argv, "stop", svc.ComposeService)
			out, err := exec.Command("docker", argv...).CombinedOutput()
			if err != nil {
				lines := strings.Split(strings.TrimSpace(string(out)), "\n")
				msg := lines[len(lines)-1]
				if msg == "" {
					msg = "error"
				}
				report("%s %s\n", padEnd(svc.Name, 16), red(msg))
				code = 1
			} else {
				report("%s %s\n", padEnd(svc.Name, 16), green("stopped"))
			}
		}
	}
	if !said {
		fmt.Println(dim("nothing was running"))
	}
	return code
}

// LogsOptions are the flags of `orbit logs`.
type LogsOptions struct {
	Follow bool
	Lines  int
	Grep   string
	Since  string
	// JSON prints one JSON object per line instead of `svc │ text`
	JSON bool
}

// jsonLine is a log line of `orbit logs --json`; Ts is only known for lines that come from a running orbit.
type jsonLine struct {
	Ts      int64       `json:"ts,omitempty"`
	Service string      `json:"service"`
	Stream  logs.Stream `json:"stream"`
	Text    string      `json:"text"`
}

// RunLogs prints recent logs, optionally following them (`orbit logs`).
func RunLogs(cfg *config.OrbitConfig, names []string, opts LogsOptions) int {
	if n, bad := unknownTargets(cfg, names); bad {
		errorln(fmt.Sprintf("unknown service or group %q", n))
		return 1
	}
	targets := cfg.Order
	if len(names) > 0 {
		targets = expand(cfg, names)
	}
	pf := newPrefixer(cfg.Order)
	keep := logs.Matcher(opts.Grep)
	show := func(l logs.Line) {
		if !keep(l) {
			return
		}
		if opts.JSON {
			b, _ := json.Marshal(jsonLine{Ts: l.Ts, Service: l.Service, Stream: l.Stream, Text: l.Text})
			printf("%s\n", b)
			return
		}
		pf.print(l.Service, l.Stream, l.Text)
	}
	emit := func(service string, stream logs.Stream, raw string) {
		show(logs.Line{Service: service, Stream: stream, Text: logs.CleanLine(raw)})
	}
	// a running orbit has the logs in memory, already interleaved (--since needs docker)
	if opts.Follow || opts.Since == "" {
		if client := connectIPC(cfg); client != nil {
			if opts.Follow {
				return followLive(client, targets, opts, show)
			}
			history, err := liveHistory(client, targets, opts.Lines)
			client.Close()
			if err != nil {
				errorln(err.Error())
				return 1
			}
			for _, l := range history {
				show(l)
			}
			return 0
		}
	}
	dir := state.Dir(cfg)
	var tails []*logs.FileTail
	var children []*exec.Cmd
	var wg sync.WaitGroup
	warnedSince := false

	for _, name := range targets {
		svc := cfg.Services[name]
		switch svc.Type {
		case config.TypeExternal:
			emit(name, logs.System, "no logs (external service)")
		case config.TypeProcess:
			if opts.Since != "" && !warnedSince {
				warnedSince = true
				fmt.Fprintln(os.Stderr, yellow("--since is ignored for process services (their log files have no timestamps)"))
			}
			files := state.Files(dir, name)
			// no timestamps in the files: stdout, then stderr
			errl := logs.ReadTail(files.Err, opts.Lines, 0)
			out := logs.ReadTail(files.Out, opts.Lines-len(errl), 0)
			if len(out) == 0 && len(errl) == 0 {
				emit(name, logs.System, "no logs")
			}
			for _, l := range out {
				emit(name, logs.Stdout, l)
			}
			for _, l := range errl {
				emit(name, logs.Stderr, l)
			}
			if opts.Follow {
				// start at the current end of file: the history was just printed
				for _, f := range []struct {
					path   string
					stream logs.Stream
				}{{files.Out, logs.Stdout}, {files.Err, logs.Stderr}} {
					t := logs.NewFileTail(f.path, func(l string) { emit(name, f.stream, l) })
					t.Start(&logs.Backlog{})
					tails = append(tails, t)
				}
			}
		default:
			var argv []string
			if svc.Type == config.TypeDocker {
				argv = []string{"docker", "logs"}
			} else {
				argv = []string{"docker", "compose", "-f", svc.ComposeFile}
				if svc.ComposeProject != "" {
					argv = append(argv, "-p", svc.ComposeProject)
				}
				argv = append(argv, "logs", "--no-color", "--no-log-prefix")
			}
			argv = append(argv, "--tail", fmt.Sprint(opts.Lines))
			if opts.Since != "" {
				argv = append(argv, "--since", opts.Since)
			}
			if opts.Follow {
				argv = append(argv, "-f")
			}
			if svc.Type == config.TypeDocker {
				argv = append(argv, runner.ContainerName(cfg.Name, name))
			} else {
				argv = append(argv, svc.ComposeService)
			}
			cmd := exec.Command(argv[0], argv[1:]...)
			stdout, e1 := cmd.StdoutPipe()
			stderr, e2 := cmd.StderrPipe()
			if e1 != nil || e2 != nil || cmd.Start() != nil {
				emit(name, logs.System, "docker is not available")
				continue
			}
			children = append(children, cmd)
			wg.Add(1)
			go func() {
				defer wg.Done()
				// `docker logs` writes the container's stderr to its own stderr: tell it apart from docker's errors by exit code
				var errLines []string
				var inner sync.WaitGroup
				inner.Add(2)
				go func() { defer inner.Done(); logs.PipeLines(stdout, func(l string) { emit(name, logs.Stdout, l) }) }()
				go func() { defer inner.Done(); logs.PipeLines(stderr, func(l string) { errLines = append(errLines, l) }) }()
				inner.Wait()
				code := cmd.Wait()
				stream := logs.Stderr
				if code != nil {
					stream = logs.System
				}
				for _, l := range errLines {
					emit(name, stream, l)
				}
			}()
		}
	}
	wg.Wait()
	if !opts.Follow {
		return 0
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	<-sigs
	for _, t := range tails {
		t.Stop(false)
	}
	for _, c := range children {
		if c.Process != nil {
			c.Process.Kill()
		}
	}
	return 0
}
