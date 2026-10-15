# Configuration (`orbit.yaml`)

A full example with every option. Services defined in a `docker-compose.yml` are imported automatically; in
`orbit.yaml` you only override what you need.

```yaml
name: my-stack
compose: ./docker-compose.yml   # optional; by default the compose in this directory is imported. false = don't import
env:                            # variables for all services. ${VAR} and ${VAR:-def} are interpolated (+ .env)
  LOG_LEVEL: info
env_file: .env.shared           # .env files for all services (optional)
shell: pwsh                     # optional: shell for cmd, hooks, health and console (default: sh, or cmd.exe on Windows)

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
    ready_when: { log: "Listening on" }   # not ready until a stdout/stderr line matches ("/regex/i" also works)
    restart: on-failure         # no | on-failure | always
    watch: ["src/**", "package.json"]   # restart on change; globs relative to `cwd`
    # long form:
    # watch:
    #   paths: ["**/*.go", "go.mod"]
    #   ignore: ["**/*_test.go"]   # .git, node_modules and editor swap files are always ignored
    #   debounce: 1s               # quiet time needed after the last change (default 1s)
    #   cooldown: 10s              # at most one restart per cooldown; changes in between are batched (default 10s)
    shell: bash                 # optional, overrides the global one for this service
    console: bin/rails console  # `i` opens it in a terminal (process: in cwd/env; docker: inside the container)
    start_timeout: 60s
    stop_timeout: 8s

  redis:                        # type: docker (if there is an image)
    image: redis:7-alpine
    ports: ["6379:6379"]
    volumes: ["./data:/data"]
    cmd: redis-server --appendonly yes
    console: redis-cli          # `docker exec -it`; without `console:` you get bash/sh

  auth0:                        # type: external — orbit does not run it, only health-checks it
    type: external
    health: "https://my-tenant.eu.auth0.com/.well-known/openid-configuration"
    url: https://manage.auth0.com   # `o` opens it
  managed-db:
    type: external
    health: "tcp:db.example.com:5432"
    console: psql "$DATABASE_URL"   # `i` opens it on the host (with the service's env / env_file)

  postgres:                     # defined in docker-compose.yml: only fields overridden here
    restart: always

  docs:
    cmd: bun run docs
    autostart: false            # not started with S / --up

  worker:
    cmd: bun run worker
    mem_limit: 1G               # alert (orange ≥85 %, red ≥100 %); 512m, 1.5G, or bytes
    leak_detection: false       # default true: warn about sustained memory growth

  build-lib:                    # task: build, migration, provisioning…
    cmd: mvn -q install -pl shared-kernel -am -DskipTests
    oneshot: true               # "ready" when it finishes with exit code 0; its dependents wait

  seeded-api:
    cmd: bun run dev
    pre_start: "rm -rf tmp/*"   # before launching; if it fails the service is `failed`
    post_start:                 # once ready; a failure is only reported
      - ./scripts/check-seed.sh
      - { cmd: "curl -fsS localhost:3000/warmup", timeout: 10s }
    post_stop: "echo cleaned"   # after it goes down, however it went down

groups:
  backend: [postgres, redis, api]
```

## Editor support

`orbit.schema.json` (shipped with the package) gives autocompletion, hover docs and
validation in editors with the [YAML language server](https://github.com/redhat-developer/yaml-language-server)
(VS Code's Red Hat YAML extension, Neovim, JetBrains…). `orbit init` writes the first line for you:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/JGoterris/orbit/main/orbit.schema.json
```

or map it in VS Code's `settings.json`: `"yaml.schemas": { "https://raw.githubusercontent.com/JGoterris/orbit/main/orbit.schema.json": "orbit.y*ml" }`.
`orbit validate` runs the same checks from the terminal or CI.

## Lifecycle hooks

`pre_start`, `post_start` and `post_stop` take a command, a `{ cmd, timeout }` or a
list of them (run in order, stopping at the first failure; default timeout 60s). They run on the host in the
service's `cwd`, with its environment plus `ORBIT_SERVICE`, `ORBIT_HOOK` and, in `post_stop` after the
service exited on its own, `ORBIT_EXIT_CODE`; their output goes to the service's log. They work on
`process`, `docker` and `compose` services.

- `pre_start` runs before every start (restarts included). If it fails, the service is `failed` and doesn't start.
- `post_start` runs once the service is ready and never blocks its dependents; if it fails it is logged and
  shown as the service's error, but the service stays up. Not allowed on `oneshot` tasks.
- `post_stop` runs whenever the service goes down: stopped, restarted, crashed or a finished `oneshot`. A
  restart waits for it. It doesn't run when orbit detaches or leaves an adopted container running.

## Environment variables

A service receives, from lowest to highest priority: your shell's
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

## Healthchecks

`health` accepts shortcuts: `"http://…"`, `"tcp:5432"`, `"container"` or a command (`"pg_isready"`).
