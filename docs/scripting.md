# Scripting & editors (socket)

While a project is running (its daemon, a TUI with `--no-daemon`, or `orbit up`) it listens on a local socket, so scripts and editor extensions can query
and drive it from outside: `~/.local/state/orbit/<project>/orbit.sock` (a shorter path under `$XDG_RUNTIME_DIR` or
`$TMPDIR` when that one would exceed the kernel's socket-path limit). The socket is `0600` inside a `0700` directory.
`orbit status --json`, `orbit ctl` and `orbit logs -f` are clients of it, and so is the TUI. The daemon writes
its own messages to `daemon.log` next to the socket.

Protocol: JSON-RPC 2.0, one JSON message per line, version 4 (see `internal/ipc/protocol.go`; `hello` reports it and a client
should refuse a server that speaks another one).

```bash
echo '{"jsonrpc":"2.0","id":1,"method":"snapshot"}' | socat - UNIX-CONNECT:$SOCK
```

Requests: `hello`, `snapshot`, `logs`, `start` / `stop` / `restart` / `toggle` (`{"services": [...]}`, groups expand),
`startAll`, `stopAll`, `toggleWatch`, `clearLogs`, `history` (cpu / memory buckets), `reload`, `subscribe`
(`{"logs": true}` to receive `state` and `log` notifications; `config` and `configPending` come with `states`) and
`shutdown`. Linux, macOS and WSL2 use Unix sockets; Windows uses a named pipe
(`\\.\pipe\orbit-<hash>`).
