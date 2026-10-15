# Contributing

Thanks for your interest in orbit! Bug reports, ideas and pull requests are welcome.

## Before you start

- For anything bigger than a small fix, open an issue first so we can agree on the approach.
- Security problems go through [SECURITY.md](SECURITY.md), not public issues.

## Development

You need Go (the version in `go.mod`; newer ones work too). Docker is only needed to try `docker` / `compose`
services.

```bash
go build -o orbit ./cmd/orbit
go test ./...                    # the e2e tests run the real binary in a PTY
go test -race ./...
go vet ./...
gofmt -l .                       # must print nothing
go run honnef.co/go/tools/cmd/staticcheck@latest ./...
```

`test/fixtures/stack/` is a demo project to try changes by hand: `go run ./cmd/orbit test/fixtures/stack`.

CI runs all of the above on Linux, macOS and Windows. Code that depends on the operating system lives in
`internal/platform/`. The rest of the layout:

```
cmd/orbit/             the binary: argument dispatch, version
orbit.go               embeds orbit.schema.json
internal/
  cli/                 argument parsing, --help, up / down / logs / status / ctl / graph / ls / init / projects, TUI startup
  config/              orbit.yaml loading (YAML → services), compose import, env files, diff, schema validation
  graph/               DAG: cycles, topological order, levels
  core/
    supervisor/        states, dependencies, health, restarts, readiness, hooks, watchers, reload, metrics
    runner/            process / docker / compose / external
    health/ hooks/ watch/ logs/ resources/ metrics/ notify/ console/
  ipc/                 local socket: JSON-RPC server and client, Remote (the TUI's mirror of a daemon), daemon
  git/                 status, diff parsing, operations, cached repo state, multi-repo runs, repo discovery
  platform/            everything that differs per OS: shells, process trees, metrics, ports, per-user dirs
  projects/ session/   project registry and switching the open project
  state/ userconfig/   per-project state dir and lock, per-user settings
  ui/                  Bubble Tea model: views, panels, overlays, input, mouse
    theme/ term/       palettes and visual helpers; the terminal emulator widget
    graphlayout/       graph layout by layers (pure, testable)
    e2e/               the real binary driven in a pseudo-terminal
docs/                  user documentation (linked from README.md)
```

## Pull requests

- Keep each one focused on one change, with tests for new behaviour and bug fixes.
- Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/): `feat:`, `fix:`, `docs:`,
  `chore:`… The release notes are built from them.
- Update the docs (`docs/`, and `README.md` if it is affected) and `orbit.schema.json` when you change commands,
  keys or `orbit.yaml`.

By contributing you agree that your work is released under the [MIT License](LICENSE).

## Releases

Maintainers push a `vX.Y.Z` tag; `.github/workflows/release.yml` runs the CI and then GoReleaser.

A snapshot release to try locally (all platforms, `.deb` and `.rpm`, no publishing):
`goreleaser release --snapshot --clean --skip=announce,publish`.
