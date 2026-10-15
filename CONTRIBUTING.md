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
`internal/platform/`; `README.md#structure` describes the rest of the layout.

## Pull requests

- Keep each one focused on one change, with tests for new behaviour and bug fixes.
- Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/): `feat:`, `fix:`, `docs:`,
  `chore:`… The release notes are built from them.
- Update `README.md` and `orbit.schema.json` when you change commands, keys or `orbit.yaml`.

By contributing you agree that your work is released under the [MIT License](LICENSE).

## Releases

Maintainers push a `vX.Y.Z` tag; `.github/workflows/release.yml` runs the CI and then GoReleaser.
