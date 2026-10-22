package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/JGoterris/orbit/internal/cli"
	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/core/runner"
	"github.com/JGoterris/orbit/internal/ipc"
	"github.com/JGoterris/orbit/internal/projects"
	"github.com/JGoterris/orbit/internal/state"
)

// version is set at build time (-ldflags "-X main.version=...").
var version = "dev"

const red = "\x1b[31morbit:\x1b[0m"

func main() { os.Exit(run(os.Args[1:])) }

// buildVersion is the version stamped by the release build, or, for `go install` / `go build`, the module
// version Go recorded.
func buildVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return strings.TrimPrefix(info.Main.Version, "v")
	}
	return version
}

func run(args []string) int {
	cli.Version = buildVersion()
	// the hidden wrapper a process service runs under on Windows (see runner.RunWrap)
	if len(args) > 0 && args[0] == runner.WrapCommand {
		return runner.RunWrap(args[1:])
	}
	inv, err := cli.Parse(args)
	if err != nil {
		var ue *cli.UsageError
		if errors.As(err, &ue) {
			fmt.Fprintf(os.Stderr, "%s %s\nrun `orbit --help` for usage\n", red, ue.Msg)
			return 1
		}
		fmt.Fprintf(os.Stderr, "%s %v\n", red, err)
		return 1
	}
	if inv.Options.Version {
		fmt.Println("orbit " + buildVersion())
		return 0
	}
	if inv.Options.Help {
		fmt.Print(cli.Help + "\n")
		return 0
	}

	// `orbit open <project>` opens a remembered project by name or path
	if inv.Sub == "open" {
		entry, err := projects.Find(inv.Rest[0], projects.Read())
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s %v\nrun `orbit --help` for usage\n", red, err)
			return 1
		}
		if st, err := os.Stat(entry.Path); err != nil || !st.IsDir() {
			fmt.Fprintf(os.Stderr, "%s unknown command or directory %q\nrun `orbit --help` for usage\n", red, entry.Path)
			return 1
		}
		inv.Dir, inv.Sub = entry.Path, ""
	}

	opts := config.LoadOptions{Dir: inv.Dir, File: inv.Options.Config, SavedProfiles: state.SavedProfiles}
	opts.Profiles, opts.ProfilesSet = inv.Options.ProfileSelection()
	// a --profile on the command line becomes the selection of the project, until another one replaces it
	remember := func(cfg *config.OrbitConfig) {
		if opts.ProfilesSet && cfg.File != "" {
			state.WriteProfiles(state.Dir(cfg), cfg.Profiles)
		}
	}
	load := func() (*config.OrbitConfig, bool) {
		cfg, err := config.LoadConfig(opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s %v\n", red, err)
			return nil, false
		}
		return cfg, true
	}
	// status and down also work while orbit.yaml is broken
	loadRunning := func() (*config.OrbitConfig, bool) {
		cfg, err := config.LoadConfig(opts)
		if err == nil {
			return cfg, true
		}
		file := config.FindConfigFile(inv.Dir)
		if opts.File != "" {
			file, _ = filepath.Abs(opts.File)
		}
		if file != "" {
			if running := cli.RunningConfig(file); running != nil {
				fmt.Fprintf(os.Stderr, "%s %v\n(acting on the orbit that is running, with the configuration it loaded)\n", red, err)
				return running, true
			}
		}
		fmt.Fprintf(os.Stderr, "%s %v\n", red, err)
		return nil, false
	}
	// the commands that act on the project make a --profile its selection; the ones that only look do not
	loadAndRemember := func() (*config.OrbitConfig, bool) {
		cfg, ok := load()
		if ok {
			remember(cfg)
		}
		return cfg, ok
	}

	switch inv.Sub {
	case "projects":
		return cli.RunProjects()
	case "init":
		dir := inv.Dir
		if dir == "" {
			dir, _ = os.Getwd()
		}
		return cli.RunInit(dir, inv.Options.Force)
	case "validate":
		return cli.RunValidate(opts)
	case "graph":
		if cfg, ok := load(); ok {
			return cli.RunGraph(cfg)
		}
		return 1
	case "ls":
		if cfg, ok := load(); ok {
			return cli.RunList(cfg)
		}
		return 1
	case "daemon":
		var idle float64
		if inv.Options.Idle != "" {
			n, err := strconv.ParseFloat(inv.Options.Idle, 64)
			if err != nil || !(n > 0) {
				fmt.Fprintf(os.Stderr, "%s --idle expects a number of minutes\nrun `orbit --help` for usage\n", red)
				return 1
			}
			idle = n
		}
		if cfg, ok := load(); ok {
			return ipc.RunDaemon(cfg, ipc.DaemonOptions{IdleMinutes: idle, Version: buildVersion()})
		}
		return 1
	case "status":
		if cfg, ok := loadRunning(); ok {
			return cli.RunStatus(cfg, inv.Options.JSON)
		}
		return 1
	case "ctl":
		if cfg, ok := loadAndRemember(); ok {
			return cli.RunCtl(cfg, inv.Rest, opts.ProfilesSet)
		}
		return 1
	case "run":
		if cfg, ok := load(); ok {
			name := ""
			if len(inv.Rest) > 0 {
				name = inv.Rest[0]
			}
			return cli.RunTask(cfg, name, inv.Args)
		}
		return 1
	case "up":
		if cfg, ok := loadAndRemember(); ok {
			return cli.RunUp(cfg, inv.Rest, inv.Options.Detach)
		}
		return 1
	case "down":
		if cfg, ok := loadRunning(); ok {
			return cli.RunDown(cfg)
		}
		return 1
	case "logs":
		lines := 200
		if inv.Options.Lines != "" {
			n, err := strconv.Atoi(inv.Options.Lines)
			if err != nil || n < 0 {
				fmt.Fprintf(os.Stderr, "%s --lines expects a non-negative number\n", red)
				return 1
			}
			lines = n
		}
		if cfg, ok := load(); ok {
			return cli.RunLogs(cfg, inv.Rest, cli.LogsOptions{
				Follow: inv.Options.Follow, Lines: lines, Grep: inv.Options.Grep, Since: inv.Options.Since,
				JSON: inv.Options.JSON,
			})
		}
		return 1
	}

	if inv.Sub == "" {
		tuiOpts := opts
		tuiOpts.AllowEmpty = true
		cfg, err := config.LoadConfig(tuiOpts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s %v\n", red, err)
			return 1
		}
		remember(cfg)
		// a folder with neither orbit.yaml nor compose is not a project: the picker opens instead
		bare := cfg.File == "" && len(cfg.Services) == 0 && inv.Dir == ""
		return cli.RunTUI(cfg, cli.TUIOptions{Up: inv.Options.Up, NoDaemon: inv.Options.NoDaemon, Bare: bare})
	}
	// cli.Parse only accepts the commands handled above
	fmt.Fprintf(os.Stderr, "%s unknown command %q\nrun `orbit --help` for usage\n", red, inv.Sub)
	return 1
}
