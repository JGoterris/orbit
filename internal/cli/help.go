package cli

// Help is the text of `orbit --help`.
const Help = `orbit — launch, control and monitor local services

usage
  orbit [dir]                open the TUI for the orbit.yaml found in dir (or above)
  orbit open <project>       open a remembered project by name or path (P inside orbit switches)
  orbit projects             list the projects orbit remembers
  orbit up [service…]        start services headless, streaming logs (ctrl+c stops; -d runs them in the background)
  orbit logs [service…]      print recent logs (-f to follow, -n lines, --grep, --since, --json)
  orbit down [--all]         stop everything orbit left running (also quits an orbit that is open); --all: every project
  orbit status [--json]      show the state of the services of an orbit that is running
  orbit ctl <action> [svc…]  start|stop|restart|toggle services of a running orbit (no svc: start/stop all;
                             with nothing running, start, restart and toggle start the daemon first)
  orbit ctl reload           apply the edits made to orbit.yaml to a running orbit (restarts only what changed)
  orbit run [task] [-- args] run a task of orbit.yaml (no task: list them); args go to the task
  orbit graph                print the dependency graph
  orbit ls                   list services
  orbit init [dir]           generate an orbit.yaml by scanning the project
  orbit validate [dir]       check the orbit.yaml (errors and unknown keys); exit code 1 if invalid
  orbit update [--check]     update orbit through the channel it was installed with (brew, apt, go, binary)
  orbit plugin <command>     install, link, list, upgrade, remove, enable, disable and check the plugins that add
                             to orbit (orbit plugin help); a plugin with a command is run as orbit <plugin> …
  orbit daemon [dir]         run the supervisor of a project in the background, without UI (the TUI starts it
                             by itself and reconnects to it, so closing the terminal does not stop anything)

options
  -c, --config <file>        use a specific config file
  -p, --project <name|path>  act on a running or remembered project from anywhere (up, down, status, ctl, logs, run, ls, graph)
      --profile <names>      activate profiles of orbit.yaml (a,b or repeat the flag); it is remembered for the project
      --no-profiles          deactivate every profile (ORBIT_PROFILES and the saved selection are ignored too)
  -d, --detach               (up) start the services in an orbit daemon and return once they are ready
  -f, --follow               (logs) keep streaming new lines
      --json                 (status, logs) machine-readable output (logs: one JSON object per line)
  -n, --lines <n>            (logs) lines per service, default 200
      --grep <regex>         (logs) only lines matching the regex
      --since <dur>          (logs) docker/compose only, e.g. 10m, 2h
  -u, --up                   (TUI) start all autostart services on launch
      --no-daemon            (TUI) run the services inside the TUI itself, as before
      --idle <min>           (daemon) quit after this many minutes with nothing running and nobody connected
      --check                (update) only say whether there is a newer version (exit code 1 if so)
  -y, --yes                  (plugin) do not ask before installing or upgrading
  -V, --version              show the version
  -h, --help                 show this help
`
