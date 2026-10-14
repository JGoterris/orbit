#!/usr/bin/env node
// Launcher of the npm package: orbit itself is a native binary shipped in a per-platform package
// (@jgoterris/orbit-<os>-<arch>, installed through optionalDependencies); this finds it and runs it.
const { spawnSync } = require("node:child_process")
const { join, dirname } = require("node:path")

const key = `${process.platform}-${process.arch}`
const pkg = `@jgoterris/orbit-${key}`
const exe = process.platform === "win32" ? "orbit.exe" : "orbit"

function binary() {
  try {
    return join(dirname(require.resolve(`${pkg}/package.json`)), "bin", exe)
  } catch {
    return undefined
  }
}

const bin = process.env.ORBIT_BINARY || binary()
if (!bin) {
  console.error(
    `orbit: there is no binary for ${key}.\n` +
      `Reinstall without --omit=optional, or install it with Go: go install github.com/JGoterris/orbit/cmd/orbit@latest`,
  )
  process.exit(1)
}

const res = spawnSync(bin, process.argv.slice(2), { stdio: "inherit" })
if (res.error) {
  console.error(`orbit: ${res.error.message}`)
  process.exit(1)
}
// a signal ends the child the same way it would have ended us
if (res.signal) process.kill(process.pid, res.signal)
process.exit(res.status ?? 1)
