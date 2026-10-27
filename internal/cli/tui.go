package cli

import (
	"context"
	"fmt"
	"github.com/JGoterris/orbit/internal/plugins"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/core/supervisor"
	"github.com/JGoterris/orbit/internal/ipc"
	"github.com/JGoterris/orbit/internal/projects"
	"github.com/JGoterris/orbit/internal/selfupdate"
	"github.com/JGoterris/orbit/internal/session"
	"github.com/JGoterris/orbit/internal/state"
	"github.com/JGoterris/orbit/internal/ui"
	"github.com/JGoterris/orbit/internal/ui/theme"
	"github.com/JGoterris/orbit/internal/userconfig"
)

// TUIOptions are the flags that matter to the interface.
type TUIOptions struct {
	// Up starts every autostart service on launch
	Up bool
	// NoDaemon runs the services inside the TUI itself instead of in an `orbit daemon`
	NoDaemon bool
	// Bare: the folder is not a project (no orbit.yaml, no compose, none given): show the project picker
	Bare bool
}

// terminalAvailable reports whether Bubble Tea will find a terminal to read keys from.
func terminalAvailable() bool {
	if term.IsTerminal(os.Stdin.Fd()) {
		return true
	}
	name := "/dev/tty"
	if runtime.GOOS == "windows" {
		name = "CONIN$"
	}
	f, err := os.Open(name)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

// RunTUI opens the interface for a project (`orbit`).
func RunTUI(cfg *config.OrbitConfig, opts TUIOptions) int {
	// before spawning a daemon that would be left behind
	if !terminalAvailable() {
		errorln("the interface needs a terminal (without one: `orbit up -d`, `orbit status`, `orbit logs`)")
		return 1
	}
	custom, customNames, themeErrors := theme.LoadCustom()
	themes := map[string]theme.Palette{}
	order := append([]string(nil), theme.Builtin...)
	for n, p := range theme.Themes {
		themes[n] = p
	}
	for _, n := range customNames {
		themes[n] = custom[n]
		order = append(order, n)
	}
	initial := userconfig.Read().Theme
	if initial == "" {
		initial = theme.DefaultName
	}
	if _, ok := themes[initial]; !ok {
		themeErrors = append(themeErrors, fmt.Sprintf("unknown theme %q, using %s", initial, theme.DefaultName))
		initial = theme.DefaultName
	}

	// the notice of a newer release comes from the last daily check; the next one runs in the background, so
	// opening the TUI never waits for the network
	if Version != "dev" && os.Getenv("ORBIT_NO_UPDATE_CHECK") == "" && userconfig.Read().UpdateCheckEnabled() {
		if latest := selfupdate.Cached(); selfupdate.Newer(latest, Version) {
			themeErrors = append(themeErrors, fmt.Sprintf("orbit %s is available: run `orbit update`", latest))
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			selfupdate.RefreshIfStale(ctx)
		}()
	}

	// Services run in an `orbit daemon` that this TUI only mirrors, unless the folder has none or the user opted
	// out: then they run in this process, as a plain Supervisor.
	useDaemon := !opts.NoDaemon && len(cfg.Services) > 0
	var first supervisor.Like
	var relaunch *ui.Relaunch
	if useDaemon {
		remote, err := ipc.ConnectProject(cfg)
		if err != nil {
			errorln(err.Error())
			return 1
		}
		// shown once as a toast: an old daemon keeps running its old code after an upgrade
		if remote.Version() != Version {
			themeErrors = append(themeErrors, fmt.Sprintf("the daemon runs orbit %s, this is %s: `orbit down` restarts it", remote.Version(), Version))
		}
		// a daemon that was already running keeps the config it started with: offer what changed since
		if diff := config.DiffConfig(remote.Config(), cfg); !diff.IsEmpty() {
			relaunch = &ui.Relaunch{Diff: diff, Config: cfg}
		}
		first = remote
	} else {
		local := supervisor.New(cfg, supervisor.Options{Plugins: plugins.ForProject(cfg, Version)})
		if holder := state.AcquireLock(local.StateDir()); holder != 0 {
			errorln(fmt.Sprintf("already open for this project (pid %d). Quit it first.", holder))
			return 1
		}
		first = local
	}
	// a folder with neither orbit.yaml nor compose is not a project: show the picker instead of remembering it
	if !opts.Bare {
		projects.Register(cfg.Root, cfg.Name, 0)
	}

	var prog *tea.Program
	var mu sync.Mutex
	quitting, disconnected := false, false
	var sess *session.Session
	quit := func(how string) {
		mu.Lock()
		if quitting {
			mu.Unlock()
			return
		}
		quitting = true
		mu.Unlock()
		sess.CloseIPC()
		sup := sess.Sup()
		if how == "detach" {
			sup.Detach()
		} else {
			sup.Dispose()
		}
		state.ReleaseLock(sup.StateDir())
	}
	quitProgram := func() {
		mu.Lock()
		p := prog
		mu.Unlock()
		if p != nil {
			p.Quit()
		}
	}
	sess = session.New(first, session.Options{
		Version: Version, Daemon: !opts.NoDaemon,
		OnShutdown: func(how string) { go func() { quit(how); quitProgram() }() },
	})
	sess.Serve()
	defer func() {
		sess.CloseIPC()
		sess.Sup().KillAllSync()
		state.ReleaseLock(sess.Sup().StateDir())
	}()

	// the daemon of the open project went away (`orbit down`, or it was killed): nothing left to show
	var unwatch func()
	watch := func(sup supervisor.Like) {
		r, ok := sup.(*ipc.Remote)
		if !ok {
			return
		}
		cancel := r.OnClose(func() {
			mu.Lock()
			current := sess.Sup() == supervisor.Like(r)
			skip := quitting || !current
			if !skip {
				quitting, disconnected = true, true
			}
			mu.Unlock()
			if !skip {
				quitProgram()
			}
		})
		mu.Lock()
		unwatch = cancel
		mu.Unlock()
	}
	// leaving a project on purpose closes its connection: that is not the daemon going away
	stopWatching := func() {
		mu.Lock()
		cancel := unwatch
		unwatch = nil
		mu.Unlock()
		if cancel != nil {
			cancel()
		}
	}

	// a signal to a TUI that only mirrors a daemon (closing the terminal sends SIGHUP) must not stop the services
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		<-sigs
		how := "stop"
		if _, remote := sess.Sup().(*ipc.Remote); remote {
			how = "detach"
		}
		quit(how)
		quitProgram()
	}()

	err := ui.Run(ui.Options{
		Sup: first, OnQuit: quit,
		Themes: themes, ThemeOrder: order, CustomThemes: customNames, InitialTheme: initial, ThemeErrors: themeErrors,
		PluginKeys: userconfig.Read().Keys,
		Relaunch:   relaunch, StartWithPicker: opts.Bare,
		// without colours the splash is only noise
		Splash: userconfig.Read().SplashEnabled() && os.Getenv("NO_COLOR") == "", Version: Version,
		LoadProfiles: func(names []string) (*config.OrbitConfig, error) {
			cur := sess.Sup().Config()
			return config.LoadConfig(config.LoadOptions{File: cur.File, Dir: cur.Root, AllowEmpty: true, Profiles: names, ProfilesSet: true})
		},
		SaveProfiles:     func(names []string) { state.WriteProfiles(state.Dir(sess.Sup().Config()), names) },
		SaveTheme:        func(name string) bool { return userconfig.Write(userconfig.Config{Theme: name}) },
		Notifications:    func() bool { return userconfig.Read().NotificationsEnabled() },
		SetNotifications: func(on bool) bool { return userconfig.Write(userconfig.Config{Notifications: &on}) },
		OpenProject: func(dir, how string) (supervisor.Like, *ui.Relaunch, error) {
			stopWatching()
			next, rl, err := sess.SwitchTo(dir, how)
			if err != nil {
				// nothing was disconnected on failure: keep watching the project that stays open
				watch(sess.Sup())
				return nil, nil, err
			}
			watch(next)
			var out *ui.Relaunch
			if rl != nil {
				out = &ui.Relaunch{Diff: rl.Diff, Config: rl.Config}
			}
			return next, out, nil
		},
		Started: func(p *tea.Program) {
			mu.Lock()
			prog = p
			mu.Unlock()
			watch(first)
			go func() {
				first.Init()
				if opts.Up {
					first.StartAll()
				}
			}()
		},
	})
	mu.Lock()
	gone := disconnected
	mu.Unlock()
	if gone {
		fmt.Fprintln(os.Stderr, "orbit: the daemon stopped")
		return 0
	}
	if err != nil {
		errorln(err.Error())
		return 1
	}
	return 0
}

// RunProjects lists the projects orbit remembers and what is going on in them (`orbit projects`).
func RunProjects() int {
	list := projects.Sort(projects.Read())
	if len(list) == 0 {
		fmt.Println(dim("no projects yet: they are remembered the first time orbit opens them"))
		return 0
	}
	type row struct{ label, path, flags string }
	var rows []row
	width := 0
	for _, p := range list {
		st := projects.StatusOf(p)
		var flags []string
		if st.OpenIn != 0 {
			flags = append(flags, cyan(fmt.Sprintf("open (pid %d)", st.OpenIn)))
		}
		if st.Running > 0 {
			flags = append(flags, green(fmt.Sprintf("● %d up", st.Running)))
		}
		if !st.Exists {
			flags = append(flags, red("missing"))
		}
		mark := " "
		if p.Pinned {
			mark = "★"
		}
		label := mark + " " + p.Name
		width = max(width, cellWidth(label))
		rows = append(rows, row{label, p.Path, joinSpace(flags)})
	}
	for _, r := range rows {
		line := padEnd(r.label, width) + "  " + dim(r.path)
		if r.flags != "" {
			line += "  " + r.flags
		}
		fmt.Println(line)
	}
	return 0
}

func joinSpace(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " "
		}
		out += p
	}
	return out
}
