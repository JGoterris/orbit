# Keys

| Key | Action |
| --- | --- |
| `↑↓` / `j k` | select service (in the graph, `←↑↓→` move spatially) |
| `space` | start / stop the selected one (with dependencies) |
| `s` `x` `r` | start / stop / restart the selected one |
| `S` `X` `R` | start / stop / restart everything |
| `W` | pause / resume file watching of the selected service |
| `1` `2` `3` `4` | Dashboard / Graph / Logs / Git |
| `tab` / `shift+tab` | move focus between panels |
| `z` · `esc` | zoom the focused panel to full screen · back |
| `+` `-` `=` | grow / shrink the focused panel · reset sizes |
| `j k` `ctrl+u/d` `g G` (logs focused) | scroll logs by line / half page / top / bottom |
| `enter` / `l` | logs of the selected one · `a` toggles selected ⇄ all |
| `/` | filter logs (regex) · `tab` switches to search (highlight, keep all lines) · `esc` clears |
| `n` · `N` | next (newer) · previous (older) search match |
| `f` · `PgUp PgDn` · wheel | follow / scroll logs |
| `t` · `w` · `c` | timestamps · line wrap · clear logs |
| `b` | fold / unfold all stack traces |
| `v` (logs focused) | copy mode: `j k` `ctrl+u/d` `g G` move, `v` start/clear selection, `enter`/`space` on a trace row opens/closes it, `y` copy, `esc` cancel |
| `Y` · `E` | copy all visible logs · export them to `~/.local/state/orbit/<project>/exports/` (the path is copied) |
| `o` | open `http://localhost:<port>` (or `url`) in the browser |
| `i` | interactive console of the selected service in a floating terminal (`console:` command, or `$SHELL` (`%ComSpec%` on Windows) / the container's shell). `ctrl+]` hides it and keeps it running; `i` brings it back |
| `e` | environment variables of the selected service (secrets masked, `r` reveals; `j`/`k` move, `v` marks a range, `y` copies the value or `KEY=value` lines, `Y` always `KEY=value`) |
| `L` | open the selected service's git repo in [lazygit](https://github.com/jesseduffield/lazygit) (if installed) |
| `4` | Git view, see [Git](git.md) |
| `P` | open another project: `enter` opens, `tab` completes a typed path, `ctrl+f` pins, `ctrl+x` forgets |
| `!` | list the tasks of `orbit.yaml` and run one (`○` = it starts the services it needs first, `✖` = one of them failed) |
| `T` | change color theme (live preview, `enter` saves, `esc` cancels) |
| `:` / `ctrl+p` | command palette |
| `?` | help |
| `q` | quit: `s` stops everything, `d` leaves services running (reopen `orbit` to resume them) |
