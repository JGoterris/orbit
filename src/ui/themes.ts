/**
 * Built-in color themes. The palettes of third-party themes are taken from their public
 * repositories (all MIT, see THIRD_PARTY_NOTICES.md); orbit is not affiliated with them.
 */

export interface Palette {
  bg: string
  panel: string
  panelAlt: string
  selection: string
  cursor: string
  match: string
  border: string
  borderFocus: string
  text: string
  muted: string
  dim: string
  accent: string
  accent2: string
  cyan: string
  green: string
  yellow: string
  orange: string
  red: string
  edge: string
  upstream: string
  downstream: string
  /** colors for service name prefixes in the combined log view */
  services: string[]
}

export const PALETTE_COLOR_KEYS = [
  "bg",
  "panel",
  "panelAlt",
  "selection",
  "cursor",
  "match",
  "border",
  "borderFocus",
  "text",
  "muted",
  "dim",
  "accent",
  "accent2",
  "cyan",
  "green",
  "yellow",
  "orange",
  "red",
  "edge",
  "upstream",
  "downstream",
] as const satisfies ReadonlyArray<Exclude<keyof Palette, "services">>

type PaletteInput = Omit<Palette, "services" | "borderFocus" | "upstream" | "downstream"> & Partial<Pick<Palette, "services" | "borderFocus" | "upstream" | "downstream">>

/** Fills the derived fields: focus border follows the accent, graph arrows follow cyan/accent2. */
function palette(p: PaletteInput): Palette {
  return {
    ...p,
    borderFocus: p.borderFocus ?? p.accent,
    upstream: p.upstream ?? p.cyan,
    downstream: p.downstream ?? p.accent2,
    services: p.services ?? [p.accent, p.green, p.yellow, p.accent2, p.cyan, p.orange, p.red],
  }
}

export const DEFAULT_THEME = "orbit"

export const THEMES: Record<string, Palette> = {
  // orbit's own palette
  orbit: palette({
    bg: "#11121b",
    panel: "#161824",
    panelAlt: "#1c1f2e",
    selection: "#283052",
    cursor: "#3d4a7a",
    match: "#5a4a1e",
    border: "#2f3450",
    text: "#c0caf5",
    muted: "#7a82ab",
    dim: "#4b5275",
    accent: "#7aa2f7",
    accent2: "#bb9af7",
    cyan: "#7dcfff",
    green: "#9ece6a",
    yellow: "#e0af68",
    orange: "#ff9e64",
    red: "#f7768e",
    edge: "#3b4261",
    services: ["#7aa2f7", "#9ece6a", "#e0af68", "#bb9af7", "#7dcfff", "#ff9e64", "#73daca", "#f7768e", "#c3e88d", "#89ddff"],
  }),

  // Catppuccin — https://github.com/catppuccin/palette — MIT, Copyright (c) 2021 Catppuccin
  "catppuccin-mocha": palette({
    bg: "#11111b",
    panel: "#181825",
    panelAlt: "#1e1e2e",
    selection: "#313244",
    cursor: "#45475a",
    match: "#4b4430",
    border: "#45475a",
    text: "#cdd6f4",
    muted: "#7f849c",
    dim: "#6c7086",
    accent: "#89b4fa",
    accent2: "#cba6f7",
    cyan: "#89dceb",
    green: "#a6e3a1",
    yellow: "#f9e2af",
    orange: "#fab387",
    red: "#f38ba8",
    edge: "#585b70",
    services: ["#89b4fa", "#a6e3a1", "#f9e2af", "#cba6f7", "#89dceb", "#fab387", "#94e2d5", "#f38ba8", "#b4befe", "#f5c2e7"],
  }),
  "catppuccin-macchiato": palette({
    bg: "#181926",
    panel: "#1e2030",
    panelAlt: "#24273a",
    selection: "#363a4f",
    cursor: "#494d64",
    match: "#4e4833",
    border: "#494d64",
    text: "#cad3f5",
    muted: "#8087a2",
    dim: "#6e738d",
    accent: "#8aadf4",
    accent2: "#c6a0f6",
    cyan: "#91d7e3",
    green: "#a6da95",
    yellow: "#eed49f",
    orange: "#f5a97f",
    red: "#ed8796",
    edge: "#5b6078",
    services: ["#8aadf4", "#a6da95", "#eed49f", "#c6a0f6", "#91d7e3", "#f5a97f", "#8bd5ca", "#ed8796", "#b7bdf8", "#f5bde6"],
  }),
  "catppuccin-frappe": palette({
    bg: "#232634",
    panel: "#292c3c",
    panelAlt: "#303446",
    selection: "#414559",
    cursor: "#51576d",
    match: "#575037",
    border: "#51576d",
    text: "#c6d0f5",
    muted: "#838ba7",
    dim: "#737994",
    accent: "#8caaee",
    accent2: "#ca9ee6",
    cyan: "#99d1db",
    green: "#a6d189",
    yellow: "#e5c890",
    orange: "#ef9f76",
    red: "#e78284",
    edge: "#626880",
    services: ["#8caaee", "#a6d189", "#e5c890", "#ca9ee6", "#99d1db", "#ef9f76", "#81c8be", "#e78284", "#babbf1", "#f4b8e4"],
  }),
  // light flavor
  "catppuccin-latte": palette({
    bg: "#dce0e8",
    panel: "#e6e9ef",
    panelAlt: "#eff1f5",
    selection: "#ccd0da",
    cursor: "#bcc0cc",
    match: "#f2dfa8",
    border: "#bcc0cc",
    text: "#4c4f69",
    muted: "#6c6f85",
    dim: "#8c8fa1",
    accent: "#1e66f5",
    accent2: "#8839ef",
    cyan: "#04a5e5",
    green: "#40a02b",
    yellow: "#df8e1d",
    orange: "#fe640b",
    red: "#d20f39",
    edge: "#acb0be",
    services: ["#1e66f5", "#40a02b", "#df8e1d", "#8839ef", "#04a5e5", "#fe640b", "#179299", "#d20f39", "#7287fd", "#ea76cb"],
  }),

  // Tokyo Night — https://github.com/tokyo-night/tokyo-night-vscode-theme — MIT, Copyright (c) 2018-present Enkia
  "tokyo-night": palette({
    bg: "#16161e",
    panel: "#1a1b26",
    panelAlt: "#1f2335",
    selection: "#283457",
    cursor: "#364a82",
    match: "#4b4126",
    border: "#292e42",
    text: "#a9b1d6",
    muted: "#787c99",
    dim: "#565f89",
    accent: "#7aa2f7",
    accent2: "#bb9af7",
    cyan: "#7dcfff",
    green: "#9ece6a",
    yellow: "#e0af68",
    orange: "#ff9e64",
    red: "#f7768e",
    edge: "#3b4261",
    services: ["#7aa2f7", "#9ece6a", "#e0af68", "#bb9af7", "#7dcfff", "#ff9e64", "#73daca", "#f7768e", "#2ac3de", "#c0caf5"],
  }),

  // Dracula — https://github.com/dracula/dracula-theme — MIT, Copyright (c) 2023 Dracula Theme
  dracula: palette({
    bg: "#191a21",
    panel: "#21222c",
    panelAlt: "#282a36",
    selection: "#44475a",
    cursor: "#565a75",
    match: "#4d4b2a",
    border: "#343746",
    text: "#f8f8f2",
    muted: "#9aa3c9",
    dim: "#6272a4",
    accent: "#bd93f9",
    accent2: "#ff79c6",
    cyan: "#8be9fd",
    green: "#50fa7b",
    yellow: "#f1fa8c",
    orange: "#ffb86c",
    red: "#ff5555",
    edge: "#44475a",
    services: ["#bd93f9", "#50fa7b", "#f1fa8c", "#ff79c6", "#8be9fd", "#ffb86c", "#ff5555"],
  }),

  // Nord — https://github.com/nordtheme/nord — MIT, Copyright (c) 2016-present Sven Greb
  nord: palette({
    bg: "#242933",
    panel: "#2e3440",
    panelAlt: "#3b4252",
    selection: "#434c5e",
    cursor: "#4c566a",
    match: "#5a5238",
    border: "#434c5e",
    text: "#d8dee9",
    muted: "#9aa5b8",
    dim: "#616e88",
    accent: "#88c0d0",
    accent2: "#b48ead",
    cyan: "#8fbcbb",
    green: "#a3be8c",
    yellow: "#ebcb8b",
    orange: "#d08770",
    red: "#bf616a",
    edge: "#4c566a",
    services: ["#88c0d0", "#a3be8c", "#ebcb8b", "#b48ead", "#8fbcbb", "#d08770", "#81a1c1", "#bf616a"],
  }),

  // Gruvbox — https://github.com/morhetz/gruvbox — MIT/X11 (as stated in its README)
  gruvbox: palette({
    bg: "#1d2021",
    panel: "#282828",
    panelAlt: "#32302f",
    selection: "#3c3836",
    cursor: "#504945",
    match: "#55481f",
    border: "#504945",
    text: "#ebdbb2",
    muted: "#a89984",
    dim: "#7c6f64",
    accent: "#83a598",
    accent2: "#d3869b",
    cyan: "#8ec07c",
    green: "#b8bb26",
    yellow: "#fabd2f",
    orange: "#fe8019",
    red: "#fb4934",
    edge: "#665c54",
    services: ["#83a598", "#b8bb26", "#fabd2f", "#d3869b", "#8ec07c", "#fe8019", "#fb4934"],
  }),

  // Rosé Pine (moon accents where main has no suitable green/orange) — https://github.com/rose-pine/rose-pine-theme — MIT, Copyright (c) 2023 Rosé Pine
  "rose-pine": palette({
    bg: "#191724",
    panel: "#1f1d2e",
    panelAlt: "#26233a",
    selection: "#403d52",
    cursor: "#524f67",
    match: "#4a3f2d",
    border: "#403d52",
    text: "#e0def4",
    muted: "#908caa",
    dim: "#6e6a86",
    accent: "#c4a7e7",
    accent2: "#ebbcba",
    cyan: "#9ccfd8",
    green: "#3e8fb0",
    yellow: "#f6c177",
    orange: "#ea9a97",
    red: "#eb6f92",
    edge: "#524f67",
    services: ["#c4a7e7", "#3e8fb0", "#f6c177", "#ebbcba", "#9ccfd8", "#ea9a97", "#eb6f92"],
  }),

  // One Dark — https://github.com/atom/one-dark-syntax — MIT, Copyright (c) 2016 GitHub Inc.
  "one-dark": palette({
    bg: "#1e2127",
    panel: "#21252b",
    panelAlt: "#282c34",
    selection: "#3e4451",
    cursor: "#4b5263",
    match: "#4d4326",
    border: "#3b4048",
    text: "#abb2bf",
    muted: "#828997",
    dim: "#5c6370",
    accent: "#61afef",
    accent2: "#c678dd",
    cyan: "#56b6c2",
    green: "#98c379",
    yellow: "#e5c07b",
    orange: "#d19a66",
    red: "#e06c75",
    edge: "#4b5263",
    services: ["#61afef", "#98c379", "#e5c07b", "#c678dd", "#56b6c2", "#d19a66", "#e06c75"],
  }),
}
