package cli

// Help is the text of `orbit --help`.
const Help = `orbit — launch, control and monitor local services

usage
  orbit [dir]                open the TUI for the orbit.yaml found in dir (or above)
  orbit open <project>       open a remembered project by name or path (P inside orbit switches)
  orbit projects             list the projects orbit remembers
  orbit up [service…]        start services headless, streaming logs (ctrl+c stops)
  orbit logs [service…]      print recent logs (-f to follow, -n lines, --grep, --since)
  orbit down                 stop everything orbit left running (also quits an orbit that is open)
  orbit status [--json]      show the state of the services of an orbit that is running
  orbit ctl <action> [svc…]  start|stop|restart|toggle services of a running orbit (no svc: start/stop all)
  orbit ctl reload           apply the edits made to orbit.yaml to a running orbit (restarts only what changed)
  orbit graph                print the dependency graph
  orbit ls                   list services
  orbit init [dir]           generate an orbit.yaml by scanning the project
  orbit validate [dir]       check the orbit.yaml (errors and unknown keys); exit code 1 if invalid
  orbit daemon [dir]         run the supervisor of a project in the background, without UI (the TUI starts it
                             by itself and reconnects to it, so closing the terminal does not stop anything)

options
  -c, --config <file>        use a specific config file
  -f, --follow               (logs) keep streaming new lines
      --json                 (status) machine-readable output
  -n, --lines <n>            (logs) lines per service, default 200
      --grep <regex>         (logs) only lines matching the regex
      --since <dur>          (logs) docker/compose only, e.g. 10m, 2h
  -u, --up                   (TUI) start all autostart services on launch
      --no-daemon            (TUI) run the services inside the TUI itself, as before
      --idle <min>           (daemon) quit after this many minutes with nothing running and nobody connected
  -h, --help                 show this help
`
