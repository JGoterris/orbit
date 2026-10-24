<p align="center">
  <img src="assets/logo.svg" alt="orbit" width="128" height="128">
</p>

# orbit

TUI to launch, control and monitor local services —processes (backends, frontends,
workers), Docker containers and `docker compose` services— respecting their dependencies.
A single static binary written in Go ([Bubble Tea](https://github.com/charmbracelet/bubbletea)): no runtime to install.

![orbit starting a demo stack in dependency order, then its dependency graph, logs and Git view](assets/demo.gif)

## Features

- **Dependency-aware startup**: dependencies first, waiting until they are ready (healthcheck, log line or
  finished `oneshot` task); stop goes in reverse.
- **Processes, Docker containers and `docker compose` services**, plus `external` ones you only health-check.
  Your `docker-compose.yml` is imported automatically.
- **Live dependency graph**, logs (filter, search, folded stack traces, copy mode), CPU and memory with history.
- **Runs in a daemon**: close the terminal and everything keeps running and supervised; reopen to resume.
- **Healthchecks, restart policies, watch mode, lifecycle hooks** and desktop notifications on crashes.
- **Profiles** (`--profile debug`) to turn optional services and settings on and off, live.
- **Tasks** (`orbit run migrate`, or from the command palette) for the commands you run by hand.
- **Embedded console** (`psql`, `rails console`, a container shell) without leaving orbit.
- **Git view** across all your service repos with multi-repo fetch / pull / push, and [lazygit](https://github.com/jesseduffield/lazygit) one key away.
- Several projects, themes, and a local socket to script it from outside.
- **AI agents**: an optional [skill](docs/agents.md) teaches Claude Code, Codex, Cursor… to start services,
  read their logs and restart them through the CLI.

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

To update, run `orbit update`: it uses the channel you installed with (Homebrew, `go install`, or it replaces the
binary of a GitHub release; for the apt / rpm packages it prints the `sudo` command). `orbit update --check` only
looks. The TUI shows a notice when a newer release exists (one check a day; turn it off with
`"updateCheck": false` in `~/.config/orbit/config.json` or `ORBIT_NO_UPDATE_CHECK=1`).

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

## AI agents

[![skills.sh](https://skills.sh/b/jgoterris/orbit)](https://skills.sh/jgoterris/orbit)

Coding agents (Claude Code, Codex, Cursor…) can drive orbit through its CLI: start the services they need and
restart them. They can also read the logs directly, so when something fails they get the error output and can debug
it without you pasting anything. They share the daemon with your TUI, so you see everything they do.

An optional skill teaches them how, without getting stuck on commands that never return. Nothing installs it for
you:

```bash
npx skills add JGoterris/orbit --skill orbit   # any agent (skills.sh)
```

```text
/plugin marketplace add JGoterris/orbit        # or as a Claude Code plugin
/plugin install orbit@orbit
```

It relies on commands that are handy in scripts too:

```bash
orbit up -d [svc…]          # start in the background, return once ready
orbit status --json         # state of every service
orbit logs api -n 100 --json   # recent logs, one JSON object per line
orbit ctl restart api       # restart and wait until ready
```

See [AI agents](docs/agents.md) for details.

## Documentation

The full documentation is at **[jgoterris.github.io/orbit](https://jgoterris.github.io/orbit/)** (sources in [`docs/`](docs/)):

- [Usage](docs/usage.md): commands and live reload of `orbit.yaml`
- [Keys](docs/keys.md)
- [Configuration](docs/configuration.md): full `orbit.yaml` reference, per-developer `orbit.local.yaml` overrides, editor support, hooks, env vars
- [Git](docs/git.md) · [Themes](docs/themes.md) · [Scripting & editors](docs/scripting.md) · [AI agents](docs/agents.md)
- [Platforms](docs/platforms.md) · [Upgrading from 0.x](docs/upgrading.md)

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) to send changes and [SECURITY.md](SECURITY.md) to report a vulnerability.
