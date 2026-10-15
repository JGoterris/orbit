<p align="center">
  <img src="assets/logo.svg" alt="orbit" width="128" height="128">
</p>

# orbit

TUI to launch, control and monitor local services —processes (backends, frontends,
workers), Docker containers and `docker compose` services— respecting their dependencies.
A single static binary written in Go ([Bubble Tea](https://github.com/charmbracelet/bubbletea)): no runtime to install.

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

- **Dependency-aware startup**: dependencies first, waiting until they are ready (healthcheck, log line or
  finished `oneshot` task); stop goes in reverse.
- **Processes, Docker containers and `docker compose` services**, plus `external` ones you only health-check.
  Your `docker-compose.yml` is imported automatically.
- **Live dependency graph**, logs (filter, search, folded stack traces, copy mode), CPU and memory with history.
- **Runs in a daemon**: close the terminal and everything keeps running and supervised; reopen to resume.
- **Healthchecks, restart policies, watch mode, lifecycle hooks** and desktop notifications on crashes.
- **Embedded console** (`psql`, `rails console`, a container shell) without leaving orbit.
- **Git view** across all your service repos with multi-repo fetch / pull / push, and [lazygit](https://github.com/jesseduffield/lazygit) one key away.
- Several projects, themes, and a local socket to script it from outside.

See [all the features](docs/features.md).

## Install

```bash
# Homebrew (macOS and Linux)
brew install JGoterris/tap/orbit

# Debian / Ubuntu: add the repository once (Cloudsmith), then it updates with `apt upgrade`
curl -1sLf 'https://dl.cloudsmith.io/public/jgoterris/orbit/setup.deb.sh' | sudo -E bash
sudo apt install orbit
# or install the .deb of a release by hand: sudo apt install ./orbit_<version>_linux_amd64.deb

# Go
go install github.com/JGoterris/orbit/cmd/orbit@latest

# or grab a binary from the GitHub Releases page (Linux, macOS, Windows) and put it in your PATH
```

orbit is one self-contained executable: it needs no runtime. Docker is only needed for `docker`/`compose`
services, and [lazygit](https://github.com/jesseduffield/lazygit) only for the `L` key.

Linux, macOS and Windows (and WSL2) are supported natively; see [platforms](docs/platforms.md) for the differences.

## Quick start

```bash
cd my-project
orbit init      # scans the project and writes an orbit.yaml
orbit           # opens the TUI; press ? for help
```

A minimal `orbit.yaml`:

```yaml
name: my-stack
services:
  postgres:
    image: postgres:16
    ports: ["5432:5432"]
  api:
    cmd: npm run dev
    cwd: ./api
    port: 3000
    depends_on: [postgres]
```

## Documentation

The full documentation is at **[jgoterris.github.io/orbit](https://jgoterris.github.io/orbit/)** (sources in [`docs/`](docs/)):

- [Usage](docs/usage.md): commands and live reload of `orbit.yaml`
- [Keys](docs/keys.md)
- [Configuration](docs/configuration.md): full `orbit.yaml` reference, editor support, hooks, env vars
- [Git](docs/git.md) · [Themes](docs/themes.md) · [Scripting & editors](docs/scripting.md)
- [Platforms](docs/platforms.md) · [Upgrading from 0.x](docs/upgrading.md)

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) to send changes and [SECURITY.md](SECURITY.md) to report a vulnerability.
