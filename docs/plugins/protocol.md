# Protocol

For plugins that do not use the Go SDK. orbit starts the program of `run` and speaks JSON-RPC 2.0 with it over its
stdin and stdout, **one JSON object per line**. Anything on stdout that is not a message is ignored and written to the
log of the plugin; stderr goes to the log too (`orbit plugin logs <name>`). The Go types are in
[`pluginsdk/protocol`](https://github.com/JGoterris/orbit/tree/main/pluginsdk/protocol).

The plugin starts in the project folder and receives `ORBIT_BIN`, `ORBIT_PLUGIN_DIR`, `ORBIT_PLUGIN_CONFIG`,
`ORBIT_PLUGIN_STATE` and `ORBIT_PROJECT_DIR`. Requests may arrive while others are being answered: answer each by its
`id`, in any order.

## From orbit to the plugin

| Method | Params | Result |
| --- | --- | --- |
| `initialize` | `{api, orbitVersion, project, projectDir, configDir, stateDir, settings, services: [{name, type, cwd}]}` | `{}` (the first request; an error makes the plugin fail to start) |
| `shutdown` | | `{}`, then exit within a second or orbit kills it |
| `view.data` | `{view, pane, context}` | `{list}` or `{detail}` or `{text}`: see [widgets](widgets.md) |
| `action.invoke` | `{action, context, inputs}` | `{toast?: {text, tone}, invalidate?: ["view", "view/pane", "none"]}` |
| `action.options` | `{action, input, context}` | `[{value, label}]` |
| `service.sections` | `{service, section}` | `[section]` |
| `service.validate` · `service.start` · `service.stop` | `{name, type, with, cwd, stopTimeoutMs}` | `{}` |
| `service.attach` | the same | `{running}` |

`context` is `{service, selection: {<pane id>: <item id>}, filter, width}`; `width` is how many columns of text fit in
the pane as it was last drawn (0 before it ever was). An error is a JSON-RPC error; its `message` is shown
to the user. Notifications from orbit: `event` with `{kind: "state"|"config"|"log", service, status, ready, stream, text, services}`,
for the kinds in `events` (`services` is the list of the project again, in a `config` event).

## From the plugin to orbit (notifications)

| Method | Params |
| --- | --- |
| `ui.invalidate` | `{view?, pane?}`: what is shown is out of date |
| `ui.toast` | `{text, tone}` |
| `badges.set` | `{target: "service:<name>" \| "header", items: [badge]}`: replaces them (none removes) |
| `service.log` | `{name, stream?: "stdout"\|"stderr", text}` |
| `service.exited` | `{name, code?, reason?}` |

orbit does not take requests from plugins: to look at or drive orbit, run `$ORBIT_BIN` (`status --json`,
`logs --json`, `ctl restart <service>`, …) or use its [socket](../scripting.md).

## Example

```text
→ {"jsonrpc":"2.0","id":1,"method":"initialize","params":{"api":1,"orbitVersion":"1.2.0","projectDir":"/src/shop",…}}
← {"jsonrpc":"2.0","id":1,"result":{}}
→ {"jsonrpc":"2.0","id":2,"method":"view.data","params":{"view":"main","pane":"list","context":{"service":"api"}}}
← {"jsonrpc":"2.0","id":2,"result":{"list":{"items":[{"id":"a","cells":{"name":{"text":"hello"}}}]}}}
← {"jsonrpc":"2.0","method":"ui.toast","params":{"text":"ready","tone":"ok"}}
```

orbit bounds what it waits for: `initialize` 10 s, data 10 s, an action 60 s, `service.start` the `start_timeout` of the
service. A plugin that dies is started again up to three times (it counts again after it runs for 30 seconds).
