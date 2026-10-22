# orbit CLI reference

Every command looks for `orbit.yaml` in the current directory or a parent (`-c <file>` picks one explicitly). A
service name can also be a group name from `orbit.yaml`, which expands to its members.

## Commands

| Command | Blocks? | What it does |
| --- | --- | --- |
| `orbit status [--json]` | no | State of the services of the running orbit. Exit code 1 when nothing runs. |
| `orbit up -d [svc…]` | no | Starts the services in the background daemon (starting the daemon if needed), applies pending edits to `orbit.yaml`, and returns once they are ready. Exit code 1 if some did not become ready. |
| `orbit up [svc…]` | **yes** | Runs the services in this terminal, streaming logs until ctrl+c. Do not use from an agent. |
| `orbit ctl start [svc…]` | no | Starts services (all autostart ones with no names) and waits until ready. Starts the daemon when nothing runs. |
| `orbit ctl restart <svc…>` | no | Restarts and waits until ready (of a group, only the members that are up; a service named alone starts if it was down). Starts the daemon when nothing runs. |
| `orbit ctl toggle <svc…>` | no | Starts what is stopped, stops what is running. |
| `orbit ctl stop [svc…]` | no | Stops services (all with no names). Exit code 1 when nothing runs. |
| `orbit ctl reload` | no | Applies the edits to `orbit.yaml` to the running orbit; only changed services restart. |
| `orbit down` | no | Stops everything and quits the daemon (and an open TUI). Only when the user asks. |
| `orbit logs [svc…]` | no | Recent logs: `-n <lines>` per service (default 200), `--grep <regex>`, `--since <dur>` (Docker only), `--json`. |
| `orbit logs -f [svc…]` | **yes** | Follows the logs. Only with a timeout. |
| `orbit run` | no | Lists the tasks. |
| `orbit run <task> [-- args]` | until it ends | Runs a task in the foreground; its exit code is orbit's. |
| `orbit ls` | no | Services, groups, tasks and profiles. |
| `orbit graph` | no | Dependency graph. |
| `orbit validate` | no | Checks `orbit.yaml` (errors, unknown keys). Exit code 1 if invalid. |
| `orbit init` | no | Writes an `orbit.yaml` by scanning the project. Only when the user asks. |
| `orbit`, `orbit open` | **yes** | Interactive TUI. Never from an agent. |

## `orbit status --json`

```json
{
  "project": "my-stack",
  "running": true,
  "services": [
    {
      "name": "api",
      "status": "healthy",
      "pid": 41233,
      "startedAt": 1792698742450,
      "restarts": 0,
      "health": "HTTP 200",
      "watch": "active",
      "cpu": [0.4, 0.2],
      "mem": [52428800, 52690944]
    },
    {
      "name": "worker",
      "status": "crashed",
      "exitCode": 1,
      "restarts": 3,
      "cpu": null,
      "mem": null
    }
  ]
}
```

- `status`: `stopped`, `waiting`, `starting`, `running` (up, no health check), `healthy`, `unhealthy`, `stopping`,
  `exited` (code 0), `crashed` (code ≠ 0), `failed` (could not start: dependency failed, port busy, launch error,
  timeout).
- `waitingOn`: the dependencies a `waiting` service is blocked on.
- `watch`: absent without `watch:` in `orbit.yaml`; otherwise `active`, `paused` or `pending`.
- `containerId`, `adopted` (a container that was already running): Docker services.
- `cpu` / `mem`: recent samples (percent, bytes); the last one is the current value.

With nothing running: `{"project": "my-stack", "running": false, "services": []}` and exit code 1.

## `orbit logs --json`

One object per line:

```json
{"service":"api","stream":"stdout","text":"listening on :3000"}
{"service":"api","stream":"stderr","text":"Error: connect ECONNREFUSED 127.0.0.1:5432"}
{"service":"web","stream":"system","text":"no logs"}
```

- `stream`: `stdout`, `stderr` or `system` (orbit's own messages).
- `ts` (milliseconds) is only present when the lines come from a running orbit (then stdout, stderr and system lines
  come interleaved, in the order orbit read them, to within a fraction of a second). With nothing running they are read from the log files: each
  service's stdout first, then its stderr, `-n` lines in all.
- Without `-f`, a process service prints its recent stdout lines and then its recent stderr lines, so the two
  streams are not interleaved in time.
