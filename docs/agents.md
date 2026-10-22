# AI agents

Coding agents (Claude Code, Codex, Cursor, OpenCode…) can drive orbit through its CLI: start the services they need,
read their logs after a change, restart one and check it came back healthy. They talk to the same daemon as the
TUI, so whatever an agent does shows up in your orbit window.

orbit ships an optional [Agent Skill](https://github.com/JGoterris/orbit/tree/main/skills/orbit) that teaches
agents how to do that without blocking on commands that never return (`orbit up`, `orbit logs -f`, the TUI).
Nothing installs it for you; add it only if you want it.

## Install the skill

With the [`skills`](https://skills.sh) CLI, for any supported agent:

```bash
npx skills add JGoterris/orbit --skill orbit              # this project
npx skills add JGoterris/orbit --skill orbit -g           # every project
npx skills add JGoterris/orbit --skill orbit -a claude-code
```

As a Claude Code plugin:

```text
/plugin marketplace add JGoterris/orbit
/plugin install orbit@orbit
```

Or copy [`skills/orbit`](https://github.com/JGoterris/orbit/tree/main/skills/orbit) into your agent's skills folder
(`~/.claude/skills/` for Claude Code).

## The commands it relies on

They are plain CLI commands, so they are just as handy in your own scripts:

| Command | What it is for |
| --- | --- |
| `orbit status --json` | state of every service; `"running": false` (exit code 1) when nothing runs |
| `orbit up -d [svc…]` | starts the services in the background daemon and returns once they are ready (exit code 1 if not) |
| `orbit ctl restart <svc…>` | restarts and waits until ready; with nothing running it starts the daemon first |
| `orbit logs <svc…> -n 100 --json` | recent logs, one `{"service","stream","text"}` object per line |
| `orbit logs <svc…> --grep <re>` | only the matching lines |
| `orbit validate` / `orbit ctl reload` | check and apply edits to `orbit.yaml` |
| `orbit run [task] [-- args]` | list or run a task |
| `orbit graph` | dependency graph |

The skill needs orbit 1.4 or later (`up -d`, `logs --json`, and `ctl` starting the daemon).
