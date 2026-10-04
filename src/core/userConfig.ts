import { mkdirSync, readdirSync, readFileSync, renameSync, writeFileSync } from "node:fs"
import { homedir } from "node:os"
import { basename, dirname, join } from "node:path"
import { DEFAULT_THEME, PALETTE_COLOR_KEYS, THEMES, type Palette } from "../ui/themes.ts"

/** Per-user (not per-project) settings: $XDG_CONFIG_HOME/orbit or ~/.config/orbit. */
export function configDir(): string {
  return join(process.env.XDG_CONFIG_HOME || join(homedir(), ".config"), "orbit")
}

export interface UserConfig {
  theme?: string
}

export function readUserConfig(): UserConfig {
  try {
    const raw = JSON.parse(readFileSync(join(configDir(), "config.json"), "utf8")) as UserConfig
    return typeof raw.theme === "string" ? { theme: raw.theme } : {}
  } catch {
    return {}
  }
}

/** Writes `data` as JSON next to its final path and renames it into place (never leaves a half-written file). */
export function writeJsonAtomic(file: string, data: unknown): boolean {
  try {
    mkdirSync(dirname(file), { recursive: true })
    const tmp = `${file}.${process.pid}.tmp`
    writeFileSync(tmp, JSON.stringify(data, null, 2) + "\n")
    renameSync(tmp, file)
    return true
  } catch {
    return false
  }
}

/** Merges `patch` into config.json. Returns false if it could not be written. */
export function writeUserConfig(patch: UserConfig): boolean {
  return writeJsonAtomic(join(configDir(), "config.json"), { ...readUserConfig(), ...patch })
}

const HEX = /^#[0-9a-fA-F]{6}$/

function hex(value: unknown, path: string): string {
  if (typeof value !== "string" || !HEX.test(value)) throw new Error(`${path}: expected a color like "#rrggbb", got ${JSON.stringify(value)}`)
  return value.toLowerCase()
}

/** Builds a palette from `{ "extends": "<builtin>", "colors": { "accent": "#ff00ff", "services": ["#..."] } }`. */
export function parseThemeFile(doc: unknown): Palette {
  if (typeof doc !== "object" || doc === null || Array.isArray(doc)) throw new Error("expected a JSON object")
  const { extends: parent = DEFAULT_THEME, colors = {} } = doc as { extends?: unknown; colors?: unknown }
  if (typeof parent !== "string" || !THEMES[parent]) throw new Error(`extends: unknown built-in theme ${JSON.stringify(parent)} (one of ${Object.keys(THEMES).join(", ")})`)
  if (typeof colors !== "object" || colors === null || Array.isArray(colors)) throw new Error("colors: expected an object")
  const out: Palette = { ...THEMES[parent]!, services: [...THEMES[parent]!.services] }
  for (const [key, value] of Object.entries(colors)) {
    if (key === "services") {
      if (!Array.isArray(value) || !value.length) throw new Error("colors.services: expected a non-empty list of colors")
      out.services = value.map((v, i) => hex(v, `colors.services[${i}]`))
    } else if ((PALETTE_COLOR_KEYS as readonly string[]).includes(key)) {
      out[key as (typeof PALETTE_COLOR_KEYS)[number]] = hex(value, `colors.${key}`)
    } else {
      throw new Error(`colors.${key}: unknown color (known: ${PALETTE_COLOR_KEYS.join(", ")}, services)`)
    }
  }
  return out
}

/** Reads themes/*.json from the config dir. Broken files are reported in `errors`, never thrown. */
export function loadCustomThemes(): { themes: Record<string, Palette>; errors: string[] } {
  const themes: Record<string, Palette> = {}
  const errors: string[] = []
  const dir = join(configDir(), "themes")
  let files: string[] = []
  try {
    files = readdirSync(dir).filter((f) => f.endsWith(".json")).sort()
  } catch {
    return { themes, errors }
  }
  for (const file of files) {
    const name = basename(file, ".json")
    try {
      if (THEMES[name]) throw new Error("name clashes with a built-in theme")
      themes[name] = parseThemeFile(JSON.parse(readFileSync(join(dir, file), "utf8")))
    } catch (err) {
      errors.push(`theme ${file}: ${(err as Error).message}`)
    }
  }
  return { themes, errors }
}
