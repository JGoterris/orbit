export function parseDotEnv(text: string): Record<string, string> {
  const out: Record<string, string> = {}
  for (const line of text.split(/\r?\n/)) {
    const m = /^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)?\s*$/.exec(line)
    if (!m) continue
    let value = (m[2] ?? "").trim()
    if (/^".*"$/.test(value)) value = value.slice(1, -1).replace(/\\n/g, "\n").replace(/\\"/g, '"')
    else if (/^'.*'$/.test(value)) value = value.slice(1, -1)
    else value = value.replace(/\s+#.*$/, "")
    out[m[1]!] = value
  }
  return out
}

export function interpolate<T>(value: T, vars: Record<string, string | undefined>): T {
  if (typeof value === "string") {
    return value.replace(/\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}/g, (_, name: string, def?: string) => {
      const v = vars[name]
      return v !== undefined && v !== "" ? v : (def ?? "")
    }) as T
  }
  if (Array.isArray(value)) return value.map((v) => interpolate(v, vars)) as T
  if (value && typeof value === "object") {
    return Object.fromEntries(Object.entries(value).map(([k, v]) => [k, interpolate(v, vars)])) as T
  }
  return value
}
