# Platforms

orbit runs natively on **Linux, macOS and Windows** (and WSL2, as Linux).

| | Linux / WSL2 | macOS | Windows |
| --- | --- | --- | --- |
| `cmd:`, hooks, `health.cmd`, `console:` run through | `sh -c` | `sh -c` | `cmd.exe /d /s /c` |
| Stop a service | `SIGTERM` to its process group, then `SIGKILL` | same | `taskkill /T` on its process tree, then `/F` |
| CPU / memory of process trees | `/proc` | `ps` | `Get-CimInstance Win32_Process` (PowerShell) |
| `orbit` socket | Unix socket | Unix socket | named pipe |
| State (logs, pids) | `~/.local/state/orbit` | `~/.local/state/orbit` | `%LOCALAPPDATA%\orbit` |
| User config (themes) | `~/.config/orbit` | `~/.config/orbit` | `%APPDATA%\orbit` |

`$XDG_STATE_HOME` / `$XDG_CONFIG_HOME` win on every platform. To pick another shell, set `shell:` at the top of
`orbit.yaml` (or per service): `shell: pwsh`, `shell: bash` (Git Bash on Windows)… PowerShell gets
`-NoProfile -Command`, everything else `-c`.

On Windows there are no signals: a stop is a `taskkill` that console programs usually honour only when forced, so
`stop_timeout` mostly delays the forced kill. A tree whose leader died cannot be found afterwards, and the embedded
console (`i`) needs ConPTY, which Windows 10 1809 and later provide.
