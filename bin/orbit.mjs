#!/usr/bin/env node
// orbit itself runs on Bun (see src/index.tsx); this shim exists only so that an `npm i -g`
// install — which invokes this file with whatever Node happens to be on PATH — can find Bun and
// hand off to it, instead of the cryptic "env: 'bun': No such file or directory" a raw
// `#!/usr/bin/env bun` produces when Bun isn't installed.
import { spawnSync } from "node:child_process"
import { accessSync, constants } from "node:fs"
import { homedir } from "node:os"
import { dirname, join } from "node:path"
import { fileURLToPath } from "node:url"

function findBun() {
  const exe = process.platform === "win32" ? "bun.exe" : "bun"
  const candidates = [
    ...(process.env.PATH ?? "").split(process.platform === "win32" ? ";" : ":"),
    join(homedir(), ".bun", "bin"),
  ]
  for (const dir of candidates) {
    if (!dir) continue
    const candidate = join(dir, exe)
    try {
      accessSync(candidate, constants.X_OK)
      return candidate
    } catch {}
  }
  return null
}

const bun = findBun()
if (!bun) {
  console.error("orbit needs Bun to run (https://bun.sh) — install it, then try again.")
  process.exit(1)
}

const entry = join(dirname(fileURLToPath(import.meta.url)), "..", "src", "index.tsx")
const result = spawnSync(bun, [entry, ...process.argv.slice(2)], { stdio: "inherit" })
process.exit(result.status ?? 1)
