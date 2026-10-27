# Widgets, tones and icons

What a pane answers with (`view.data`). orbit draws it with its own components; a plugin cannot choose colours, glyphs
or layout beyond this. Text is cleaned before it is drawn: escape sequences and control characters are dropped,
tabs become spaces, and a text is cut at 4096 bytes.

## list

```json
{
  "columns": [{ "id": "title" }, { "id": "author", "priority": 1, "width": 8 }],
  "items": [
    { "id": "41", "icon": "open", "cells": { "title": { "text": "!41 Fix login" }, "author": { "text": "ana", "tone": "muted" } },
      "badges": [{ "text": "CI", "tone": "ok", "icon": "ok" }] }
  ],
  "empty": "no merge requests",
  "footer": "2 draft · 1 merged"
}
```

The **first column** takes the space the others leave. Of the others, the ones with the highest `priority` number are
hidden first when the pane is narrow. `width` is in columns; without it a column is as wide as its content, up to 24.
`id` identifies a row between reloads: the selection stays on it. At most 5000 rows.

The user can filter a list with `/` (fuzzy, on the text of the cells).

## detail

```json
{ "sections": [ { "title": "MR !41", "fields": [{ "label": "author", "value": "ana", "tone": "info" }],
                  "text": [[{ "text": "A description", "tone": "muted" }]],
                  "badges": [{ "text": "approved", "tone": "ok" }] } ] }
```

Each section has a title line, its fields packed in lines, its text (lines of spans) and its badges. It scrolls with
`j` / `k`.

## text

```json
{ "lines": [[{ "text": "build ", "tone": "muted" }, { "text": "failed", "tone": "error", "bold": true }]] }
```

Lines of spans, scrolled like the logs. With `follow: true` in the manifest it stays at the end.

## badges

`{ "text": "CI 2", "tone": "error", "icon": "fail", "action": "open-ci" }`. `action` is the id of an action of the plugin
that a click runs (only in the header). Up to eight per place.

## Tones

A tone is a colour by role, mapped to the active theme, so a plugin looks right in a light or a dark one:

| Tone | |
| --- | --- |
| *(none)* | normal text |
| `ok` · `warn` · `error` · `info` | green · yellow · red · cyan |
| `muted` | secondary text |
| `accent` | the colour of orbit |
| `service:<name>` | the colour of that service in the logs |

## Icons

`ok` ✓ · `fail` ✖ · `run` (a spinner) · `wait` ◌ · `skip` ⊘ · `off` ○ · `warn` ▲ · `info` ● · `open` ● · `merge` ◆ ·
`closed` ○ · `draft` ◌. Any other name is not drawn.
