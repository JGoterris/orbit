# orbit

TUI to launch, control and monitor local services —processes (backends, frontends,
workers), Docker containers and `docker compose` services— respecting their dependencies.
Built with TypeScript, [Bun](https://bun.sh) and [OpenTUI](https://github.com/anomalyco/opentui) (`@opentui/react`).

```
 ◉ orbit · demo     1 Dashboard  2 Graph  3 Logs                                ● 7 up  ○ 1
╭─ Services 7/8 ───────────╮╭─ Dependency graph · 7/8 up ────────────────────────────────────╮
│ ● postgres       :55432  ││                               ╭────────────────────╮           │
│ ● redis          :56379  ││                         ╭────▶│ ● auth             ├────╮      │
│ ● auth           :4300   ││ ╭────────────────────╮  │     │ healthy :4300 40M  │    │      │
│▌● api            :4100   ││ │ ● postgres         ├──┤     ╰────────────────────╯    │      │
│ ● worker                 ││ │ healthy :55432 19M │  │                               │      │
│ ● web            :4200   ││ ╰────────────────────╯  │                               │  ... │
```

## Features

- **Dependency graph** by layers (level 0 on the left), colored by status; selecting
  a node highlights its dependencies (cyan) and its dependents (purple).
  Navigable with arrow keys and mouse.
- **Ordered startup**: `start` brings up dependencies first and waits for them to be *ready*
  (healthcheck OK, or running if they don't have one); `stop` stops dependents first.
- **Healthchecks**: HTTP, TCP, command, or Docker's `HEALTHCHECK`. If you set `port` without
  `health`, a TCP check to that port is used.
- **Restart policy** `no | on-failure | always` with exponential backoff (1s → 30s).
- **Live logs** per service or combined with color prefix, regex filter, scroll
  (wheel/PgUp/PgDn), timestamps, error/warning highlighting.
- **CPU and memory** per process tree (via `/proc`) and per container (`docker stats`), with
  sparklines.
- **Docker / compose**: automatically imports `docker-compose.yml` (with `depends_on`, ports,
  healthchecks and `${VAR:-def}` interpolation), and re-attaches to containers that were
  already running. Those containers **are not stopped when exiting orbit** (only with explicit `x`/`X`).
- **Detach / resume**: on `q` choose *stop all* or *leave running*. Left-running processes keep
  their own process group and write their output to `~/.local/state/orbit/<project>/logs/`; the next
  `orbit` re-attaches (status, logs, metrics, stop/restart). `orbit down` stops them from outside.
- **`oneshot` tasks** (builds, migrations, provisioning): count as ready once they finish with
  exit code 0, are shown as `✓ done`, and aren't re-run when another dependent starts.
- Detects **occupied ports** before starting (and tells you which process is using them).
- Kills **entire process trees** (its own process group): no orphaned
  `vite`/`esbuild` processes hogging ports.
- Command palette with fuzzy search, service groups, open in browser.
- Press `L` to open the service's git repo in [lazygit](https://github.com/jesseduffield/lazygit)
  (if installed); orbit resumes when you quit it and services keep running meanwhile.

## Install

```bash
npm install -g @jgoterris/orbit
# or
bun add -g @jgoterris/orbit
# or, without installing:
bunx @jgoterris/orbit
```

Requires [Bun](https://bun.sh) ≥ 1.3 — orbit runs on Bun regardless of which package manager
installed it. Docker is only needed for `docker`/`compose` services. CPU/memory sparklines for
process trees read `/proc`, so they're Linux-only (container metrics via `docker stats` work
anywhere).

## Usage

```bash
orbit [dir]          # TUI using the orbit.yaml from dir (or a parent directory)
orbit --up           # TUI starting all autostart services
orbit up [svc|group] # without TUI: starts and shows logs (ctrl+c to stop)
orbit down           # stops whatever orbit left running (processes, containers)
orbit graph          # prints the dependency graph
orbit ls             # lists services
orbit init [dir]     # generates an orbit.yaml by scanning the project
```

Without `orbit.yaml`, orbit opens the services from a `docker-compose.yml` directly.

### Keys

| Key | Action |
| --- | --- |
| `↑↓` / `j k` | select service (in the graph, `←↑↓→` move spatially) |
| `space` | start / stop the selected one (with dependencies) |
| `s` `x` `r` | start / stop / restart the selected one |
| `S` `X` `R` | start / stop / restart everything |
| `1` `2` `3` | Dashboard / Graph / Logs |
| `tab` / `shift+tab` | move focus between panels |
| `z` · `esc` | zoom the focused panel to full screen · back |
| `+` `-` `=` | grow / shrink the focused panel · reset sizes |
| `j k` `ctrl+u/d` `g G` (logs focused) | scroll logs by line / half page / top / bottom |
| `enter` / `l` | logs of the selected one · `a` toggles selected ⇄ all |
| `/` | filter logs (regex) · `esc` clears |
| `f` · `PgUp PgDn` · wheel | follow / scroll logs |
| `t` · `c` | timestamps · clear logs |
| `o` | open `http://localhost:<port>` (or `url`) in the browser |
| `e` | environment variables of the selected service (secrets masked, `v` reveals) |
| `L` | open the selected service's git repo in [lazygit](https://github.com/jesseduffield/lazygit) (if installed) |
| `:` / `ctrl+p` | command palette |
| `?` | help |
| `q` | quit: `s` stops everything, `d` leaves services running (reopen `orbit` to resume them) |

## orbit.yaml

```yaml
name: my-stack
compose: ./docker-compose.yml   # optional; by default the compose in this directory is imported. false = don't import
env:                            # variables for all services. ${VAR} and ${VAR:-def} are interpolated (+ .env)
  LOG_LEVEL: info
env_file: .env.shared           # .env files for all services (optional)

services:
  api:                          # type: process (default if there is a cmd)
    cmd: bun run dev
    cwd: ./api
    env: { PORT: "3000" }
    env_file:                   # path, list of paths, or { path, required: false } if optional
      - ./api/.env
      - { path: ./api/.env.local, required: false }
    port: 3000                  # checks for occupied port, TCP health by default, `o` key
    depends_on: [postgres, redis]
    health: { http: "http://localhost:3000/health", interval: 2s, timeout: 2s }
    restart: on-failure         # no | on-failure | always
    start_timeout: 60s
    stop_timeout: 8s

  redis:                        # type: docker (if there is an image)
    image: redis:7-alpine
    ports: ["6379:6379"]
    volumes: ["./data:/data"]
    cmd: redis-server --appendonly yes

  postgres:                     # defined in docker-compose.yml: only fields overridden here
    restart: always

  docs:
    cmd: bun run docs
    autostart: false            # not started with S / --up

  build-lib:                    # task: build, migration, provisioning…
    cmd: mvn -q install -pl shared-kernel -am -DskipTests
    oneshot: true               # "ready" when it finishes with exit code 0; its dependents wait

groups:
  backend: [postgres, redis, api]
```

**Environment variables.** A service receives, from lowest to highest priority: your shell's
environment, the global `env_file`s, the service's `env_file`s (in order: the last one wins), and then the global
`env` and the service's `env` (inline values always win over files). Paths are relative to
`orbit.yaml`. Files are **re-read on every startup**, so after editing an `.env`
it's enough to restart the service (`r`). A required file that doesn't exist is an error when loading
the config and when starting. The service detail shows which files it uses and the log indicates how many
variables were loaded. In `compose` services, the environment is managed by compose itself (`env_file`
inside the `docker-compose.yml`).

The `.env` next to `orbit.yaml` (and the one next to each compose) is used to interpolate `${VAR}`
in the configuration, just like in docker compose. It is not injected into the services unless you
put it in `env_file`.

`health` accepts shortcuts: `"http://…"`, `"tcp:5432"`, `"container"` or a command (`"pg_isready"`).

## Structure

```
src/
  index.tsx            CLI + renderer startup
  cli.ts               up / down / graph / ls / init
  config/              schema, orbit.yaml loading, compose import
  core/
    graph.ts           DAG: cycles, topological order, levels
    supervisor.ts      states, dependencies, healthchecks, restarts, metrics
    runners.ts         process / docker / compose
    health.ts metrics.ts logs.ts exec.ts
  ui/
    App.tsx            layout, keyboard, views, overlays
    graphLayout.ts     graph layout by layers (pure, testable)
    GraphView.tsx ServiceList.tsx ServiceDetail.tsx LogView.tsx Overlays.tsx theme.ts hooks.ts
test/                  bun test (config, graph, supervisor with real processes, TUI with test renderer)
```

## Development

```bash
bun install
bun test
bun run typecheck

bun link            # installs the `orbit` command globally from this checkout
```
