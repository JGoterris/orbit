import { afterEach, describe, expect, test } from "bun:test"
import { mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { loadCustomThemes, parseThemeFile, readUserConfig, writeUserConfig } from "../src/core/userConfig.ts"
import { applyTheme, serviceColor, styleFor, theme } from "../src/ui/theme.ts"
import { DEFAULT_THEME, PALETTE_COLOR_KEYS, THEMES } from "../src/ui/themes.ts"

const HEX = /^#[0-9a-f]{6}$/i

afterEach(() => applyTheme(THEMES[DEFAULT_THEME]!))

function configHome() {
  const dir = mkdtempSync(join(tmpdir(), "orbit-config-"))
  process.env.XDG_CONFIG_HOME = dir
  return join(dir, "orbit")
}

describe("built-in themes", () => {
  test("ship the expected names", () => {
    expect(Object.keys(THEMES)).toEqual([
      "orbit",
      "catppuccin-mocha",
      "catppuccin-macchiato",
      "catppuccin-frappe",
      "catppuccin-latte",
      "tokyo-night",
      "dracula",
      "nord",
      "gruvbox",
      "rose-pine",
      "one-dark",
    ])
  })

  test("every palette defines every color as #rrggbb", () => {
    for (const [name, p] of Object.entries(THEMES)) {
      for (const k of PALETTE_COLOR_KEYS) expect(p[k], `${name}.${k}`).toMatch(HEX)
      expect(p.services.length, name).toBeGreaterThan(0)
      for (const c of p.services) expect(c, `${name}.services`).toMatch(HEX)
    }
  })
})

describe("applyTheme", () => {
  test("changes what the shared helpers return", () => {
    expect(styleFor("healthy").color).toBe(THEMES.orbit!.green)
    applyTheme(THEMES.dracula!)
    expect(theme.accent).toBe(THEMES.dracula!.accent)
    expect(styleFor("healthy").color).toBe(THEMES.dracula!.green)
    expect(styleFor("crashed").color).toBe(THEMES.dracula!.red)
    expect(serviceColor("a", ["a", "b"])).toBe(THEMES.dracula!.services[0]!)
  })
})

describe("user config", () => {
  test("round-trips the theme and keeps other fields on write", () => {
    const dir = configHome()
    expect(readUserConfig()).toEqual({})
    expect(writeUserConfig({ theme: "nord" })).toBe(true)
    expect(readUserConfig()).toEqual({ theme: "nord" })
    expect(JSON.parse(readFileSync(join(dir, "config.json"), "utf8"))).toEqual({ theme: "nord" })
  })

  test("a corrupt config.json reads as empty", () => {
    const dir = configHome()
    mkdirSync(dir, { recursive: true })
    writeFileSync(join(dir, "config.json"), "{ nope")
    expect(readUserConfig()).toEqual({})
  })
})

describe("custom themes", () => {
  test("extends a built-in and overrides colors", () => {
    const p = parseThemeFile({ extends: "catppuccin-mocha", colors: { accent: "#FF00FF", services: ["#112233"] } })
    expect(p.accent).toBe("#ff00ff")
    expect(p.services).toEqual(["#112233"])
    expect(p.bg).toBe(THEMES["catppuccin-mocha"]!.bg)
    expect(THEMES["catppuccin-mocha"]!.accent).not.toBe("#ff00ff") // the parent is not mutated
  })

  test("extends defaults to orbit; bad input is rejected", () => {
    expect(parseThemeFile({}).bg).toBe(THEMES.orbit!.bg)
    expect(() => parseThemeFile({ colors: { accent: "red" } })).toThrow(/#rrggbb/)
    expect(() => parseThemeFile({ colors: { nope: "#000000" } })).toThrow(/unknown color/)
    expect(() => parseThemeFile({ extends: "nope" })).toThrow(/unknown built-in/)
    expect(() => parseThemeFile([])).toThrow(/object/)
  })

  test("loads themes/*.json and reports broken files without throwing", () => {
    const dir = configHome()
    mkdirSync(join(dir, "themes"), { recursive: true })
    writeFileSync(join(dir, "themes", "mine.json"), JSON.stringify({ extends: "nord", colors: { accent: "#010203" } }))
    writeFileSync(join(dir, "themes", "broken.json"), "{")
    writeFileSync(join(dir, "themes", "nord.json"), "{}")
    writeFileSync(join(dir, "themes", "readme.txt"), "ignored")
    const { themes, errors } = loadCustomThemes()
    expect(Object.keys(themes)).toEqual(["mine"])
    expect(themes.mine!.accent).toBe("#010203")
    expect(errors).toHaveLength(2)
    expect(errors.join("\n")).toMatch(/broken\.json/)
    expect(errors.join("\n")).toMatch(/nord\.json.*clashes/)
  })

  test("no themes dir is fine", () => {
    configHome()
    expect(loadCustomThemes()).toEqual({ themes: {}, errors: [] })
  })
})
