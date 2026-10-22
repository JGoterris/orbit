# Features

- **Dependency graph** by layers (level 0 on the left), colored by status; selecting
  a node highlights its dependencies (cyan) and its dependents (purple).
  Navigable with arrow keys and mouse.
- **External services** (`type: external`): a SaaS API or managed database you do not run. It only needs a
  `health`; it shows up in the graph and dashboard as healthy/unhealthy and works as a `depends_on` target
  (dependents wait until it answers). `start` re-checks it, `stop` does nothing, and `i` still opens its `console:`.
- **Desktop notifications** when a service crashes or turns unhealthy, even with the terminal closed.
  On by default; toggle it from the command palette (`:`).
- **Ordered startup**: `start` brings up dependencies first and waits for them to be *ready*
  (healthcheck OK, or running if they don't have one); `stop` stops dependents first.
- **Readiness by log line**: `ready_when: { log: "Listening on" }` keeps a service in `starting` until its
  output matches (a substring, or `"/regex/i"`). For workers and queues with no health endpoint; combined
  with `health`, both must pass.
- **Healthchecks**: HTTP, TCP, command, or Docker's `HEALTHCHECK`. If you set `port` without
  `health`, a TCP check to that port is used.
- **Restart policy** `no | on-failure | always` with exponential backoff (1s → 30s).
- **Embedded console**: `i` opens `psql`, `rails console` or a container shell in a floating
  terminal (PTY + terminal emulator) without leaving orbit; it survives hiding the modal.
- **Watch mode**: `watch:` globs restart a service when its files change (for stacks without
  hot-reload: Go, Rust, plain backends). Changes are debounced and rate-limited (`cooldown`), so
  editing never causes a restart storm; `W` pauses it. Never starts a service you stopped.
  On WSL2, edits made from Windows tools to files under `/mnt/c` don't trigger inotify: keep the project in the Linux filesystem.
- **Long log lines wrap** (`w` toggles), the view stays put while you scroll up, and copy mode
  (`v`) selects across scrolling and copies whole lines via OSC 52 (or `wl-copy`/`xclip`/`pbcopy`/`clip.exe`).
- **Stack traces fold** (`b` toggles): Java, Node and Python traces collapse into one row
  (`▸ TypeError: … ⋯ +12 lines`). `b` folds/unfolds them all; to open just one, enter copy mode (`v`), move to its row and press `enter` or `space`. Copying and exporting keep every line.
- **Live logs** per service or combined with color prefix, regex filter, scroll
  (wheel/PgUp/PgDn), timestamps, error/warning highlighting.
- **CPU and memory** per process tree (via `/proc`) and per container (`docker stats`), with
  sparklines and up to an hour of history (`h` cycles 2m · 15m · 1h).
- **Memory alerts**: `mem_limit` turns memory orange at 85 % and red at 100 %, and a steady growth
  (leak) shows `↗`; both raise a toast and a desktop notification.
- **Docker / compose**: automatically imports `docker-compose.yml` (with `depends_on`, ports,
  healthchecks and `${VAR:-def}` interpolation), and re-attaches to containers that were
  already running. Those containers **are not stopped when exiting orbit** (only with explicit `x`/`X`).
- **Daemon**: the TUI is only a client. The services of a project run in an `orbit daemon` that the
  first `orbit` starts by itself (one per project, in its own session), so you can close the terminal, open
  another one (or tmux, or two at once) and find everything as it was, logs included. Health checks, restart
  policies, watchers and hooks keep working with no UI open. Closing the terminal never stops anything:
  on `q` choose *stop all* (the daemon quits too) or *leave running* (it keeps supervising).
  `orbit down` stops everything from outside. `orbit --no-daemon` runs the services inside the TUI instead:
  then *leave running* keeps the processes (own process group, output in
  `~/.local/state/orbit/<project>/logs/`) but nobody supervises them until the next `orbit` re-attaches.
  Those files keep the output of earlier runs too (a crash and the restart after it read together): past 10 MB
  the next start moves a file aside to `<service>.out.1.log` / `.err.1.log`.
- **`oneshot` tasks** (builds, migrations, provisioning): count as ready once they finish with
  exit code 0, are shown as `✓ done`, and aren't re-run when another dependent starts.
- **Profiles** (`--profile debug`, or `p` in the TUI): optional sets of changes that add, change or remove services
  on top of `orbit.yaml`, applied live (only what differs restarts) and remembered per project.
- **`tasks:`** for what you run by hand (migrate, seed, test, lint): `orbit run <task> [-- args]` or the command
  palette, with the project's env and optional `depends_on` services started first. Not services: never started by `S`.
- Detects **occupied ports** before starting (and tells you which process is using them).
- Kills **entire process trees** (its own process group): no orphaned
  `vite`/`esbuild` processes hogging ports.
- Command palette with fuzzy search, service groups, open in browser.
- **Git at a glance** (`4`): one row per repo your services live in (branch, ahead/behind, changes, stashes, last
  commit, services, and how the last action went), with the selected repo's changes, branches, graph and a
  read-only diff below. **Multi-repo actions** (`m`): fetch, pull, push, create a branch or switch branch in
  several repos at once. Stage files and commit quickly from here; everything else (rebase, amend, stash,
  hunks, conflicts…) is [lazygit](https://github.com/jesseduffield/lazygit), one key away (`L`).
- **Several projects, one orbit**: press `P` to switch to another project (pinned and recent ones, or
  type a folder path and `tab` to complete it). Services of the project you leave can be stopped or left
  running; opening it again re-attaches. Starting `orbit` in a folder that is not a project opens the picker.
- Press `L` to open the service's git repo in [lazygit](https://github.com/jesseduffield/lazygit)
  (if installed); orbit resumes when you quit it and services keep running meanwhile.
