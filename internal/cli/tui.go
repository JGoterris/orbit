package cli

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/core/supervisor"
	"github.com/JGoterris/orbit/internal/ipc"
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
}

// RunTUI opens the interface for a project (`orbit`).
func RunTUI(cfg *config.OrbitConfig, opts TUIOptions) int {
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

	// Services run in an `orbit daemon` that this TUI only mirrors, unless the folder has none or the user opted
	// out: then they run in this process, as a plain Supervisor.
	useDaemon := !opts.NoDaemon && len(cfg.Services) > 0
	var sup supervisor.Like
	var relaunch *ui.Relaunch
	var srv *ipc.Server
	if useDaemon {
		remote, err := ipc.ConnectProject(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\x1b[31morbit:\x1b[0m %v\n", err)
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
		sup = remote
	} else {
		local := supervisor.New(cfg, supervisor.Options{})
		if holder := state.AcquireLock(local.StateDir()); holder != 0 {
			fmt.Fprintf(os.Stderr, "\x1b[31morbit:\x1b[0m already open for this project (pid %d). Quit it first.\n", holder)
			return 1
		}
		sup = local
	}

	_, isRemote := sup.(*ipc.Remote)
	var prog *tea.Program
	quitting := false
	quit := func(how string) {
		if quitting {
			return
		}
		quitting = true
		if srv != nil {
			srv.Close()
		}
		if how == "detach" {
			sup.Detach()
		} else {
			sup.Dispose()
		}
		state.ReleaseLock(sup.StateDir())
	}
	if !isRemote {
		// the open project answers on its socket too: `orbit status`, `orbit ctl`, scripts and editors
		srv = ipc.NewServer(sup, ipc.ServerOptions{Version: Version, OnShutdown: func(how string) {
			go func() {
				quit(how)
				if prog != nil {
					prog.Quit()
				}
			}()
		}})
		srv.Start()
	}

	// a signal to a TUI that only mirrors a daemon (closing the terminal sends SIGHUP) must not stop the services
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		<-sigs
		how := "stop"
		if isRemote {
			how = "detach"
		}
		quit(how)
		if prog != nil {
			prog.Quit()
		}
	}()
	defer func() { sup.KillAllSync(); state.ReleaseLock(sup.StateDir()) }()

	disconnected := false
	err := ui.Run(ui.Options{
		Sup: sup, OnQuit: quit,
		Themes: themes, ThemeOrder: order, CustomThemes: customNames, InitialTheme: initial, ThemeErrors: themeErrors,
		Relaunch:         relaunch,
		SaveTheme:        func(name string) bool { return userconfig.Write(userconfig.Config{Theme: name}) },
		Notifications:    func() bool { return userconfig.Read().NotificationsEnabled() },
		SetNotifications: func(on bool) bool { return userconfig.Write(userconfig.Config{Notifications: &on}) },
		Started: func(p *tea.Program) {
			prog = p
			if r, ok := sup.(*ipc.Remote); ok {
				// the daemon went away (`orbit down`, or it was killed): nothing left to show
				r.OnClose(func() {
					if !quitting {
						quitting, disconnected = true, true
						p.Quit()
					}
				})
			}
			go func() {
				sup.Init()
				if opts.Up {
					sup.StartAll()
				}
			}()
		},
	})
	if disconnected {
		fmt.Fprintln(os.Stderr, "orbit: the daemon stopped")
		return 0
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "\x1b[31morbit:\x1b[0m %v\n", err)
		return 1
	}
	return 0
}
