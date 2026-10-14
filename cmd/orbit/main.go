package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/JGoterris/orbit/internal/cli"
	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/core/runner"
	"github.com/JGoterris/orbit/internal/ipc"
	"github.com/JGoterris/orbit/internal/projects"
)

// version is set at build time (-ldflags "-X main.version=...").
var version = "dev"

const red = "\x1b[31morbit:\x1b[0m"

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	cli.Version = version
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

	opts := config.LoadOptions{Dir: inv.Dir, File: inv.Options.Config}
	load := func() (*config.OrbitConfig, bool) {
		cfg, err := config.LoadConfig(opts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s %v\n", red, err)
			return nil, false
		}
		return cfg, true
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
			return ipc.RunDaemon(cfg, ipc.DaemonOptions{IdleMinutes: idle, Version: version})
		}
		return 1
	case "status":
		if cfg, ok := load(); ok {
			return cli.RunStatus(cfg, inv.Options.JSON)
		}
		return 1
	case "ctl":
		if cfg, ok := load(); ok {
			return cli.RunCtl(cfg, inv.Rest)
		}
		return 1
	case "up":
		if cfg, ok := load(); ok {
			return cli.RunUp(cfg, inv.Rest)
		}
		return 1
	case "down":
		if cfg, ok := load(); ok {
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
			})
		}
		return 1
	}

	if inv.Sub == "" {
		cfg, err := config.LoadConfig(config.LoadOptions{Dir: inv.Dir, File: inv.Options.Config, AllowEmpty: true})
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s %v\n", red, err)
			return 1
		}
		// a folder with neither orbit.yaml nor compose is not a project: the picker opens instead
		bare := cfg.File == "" && len(cfg.Services) == 0 && inv.Dir == ""
		return cli.RunTUI(cfg, cli.TUIOptions{Up: inv.Options.Up, NoDaemon: inv.Options.NoDaemon, Bare: bare})
	}
	name := inv.Sub
	// The remaining commands are wired up by a later phase of the migration (see the plan).
	fmt.Fprintf(os.Stderr, "%s %q is not implemented yet in this Go build (%s)\n", red, name, version)
	return 1
}
