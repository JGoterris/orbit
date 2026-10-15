# Themes

Press `T` (or `: Change theme…`) to pick one; the choice is saved in `~/.config/orbit/config.json` and applies to every project.

Built in: `orbit` (default), `catppuccin-mocha`, `catppuccin-macchiato`, `catppuccin-frappe`, `catppuccin-latte` (light), `tokyo-night`, `dracula`, `nord`, `gruvbox`, `rose-pine`, `one-dark`. Palettes come from the upstream projects (all MIT, see [THIRD_PARTY_NOTICES.md](../THIRD_PARTY_NOTICES.md)); orbit is not affiliated with them.

Your own themes are JSON files in `~/.config/orbit/themes/<name>.json` (`$XDG_CONFIG_HOME` is respected). They extend a built-in one (default `orbit`) and override any color:

```json
{
  "extends": "catppuccin-mocha",
  "colors": { "accent": "#ff79c6", "services": ["#ff79c6", "#8be9fd", "#50fa7b"] }
}
```

Colors are `#rrggbb`. Keys: `bg panel panelAlt selection cursor match border borderFocus text muted dim accent accent2 cyan green yellow orange red edge upstream downstream`, plus `services` (list used for log prefixes). Broken files are skipped and reported on startup.
