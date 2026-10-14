#!/usr/bin/env node
// Builds the npm packages of a release from the binaries GoReleaser left in dist/.
//   node scripts/npm-packages.mjs <version> [dist-dir] [out-dir]
// out-dir gets one folder per package: `orbit` (the launcher) and `orbit-<os>-<arch>` (one binary each).
// Publishing (platform packages first, then the launcher) is the release workflow's job.
import { chmodSync, copyFileSync, cpSync, existsSync, mkdirSync, readdirSync, readFileSync, writeFileSync } from "node:fs"
import { join, resolve } from "node:path"

const [version, distArg = "dist", outArg = "npm-out"] = process.argv.slice(2)
if (!version || !/^\d+\.\d+\.\d+(-[\w.]+)?$/.test(version)) {
  console.error("usage: node scripts/npm-packages.mjs <version, e.g. 1.0.0> [dist-dir] [out-dir]")
  process.exit(2)
}
const root = resolve(import.meta.dirname, "..")
const dist = resolve(distArg)
const out = resolve(outArg)

// goreleaser os/arch -> node's process.platform / process.arch
const OS = { linux: "linux", darwin: "darwin", windows: "win32" }
const ARCH = { amd64: "x64", arm64: "arm64" }

const platforms = []
for (const dir of readdirSync(dist, { withFileTypes: true })) {
  const m = /^orbit_(linux|darwin|windows)_(amd64|arm64)/.exec(dir.name)
  if (!dir.isDirectory() || !m) continue
  const exe = m[1] === "windows" ? "orbit.exe" : "orbit"
  const file = join(dist, dir.name, exe)
  if (existsSync(file)) platforms.push({ os: OS[m[1]], cpu: ARCH[m[2]], exe, file })
}
if (!platforms.length) {
  console.error(`no binaries found in ${dist} (run goreleaser first)`)
  process.exit(1)
}

const main = JSON.parse(readFileSync(join(root, "npm/orbit/package.json"), "utf8"))
for (const p of platforms) {
  const name = `@jgoterris/orbit-${p.os}-${p.cpu}`
  const dir = join(out, `orbit-${p.os}-${p.cpu}`)
  mkdirSync(join(dir, "bin"), { recursive: true })
  copyFileSync(p.file, join(dir, "bin", p.exe))
  if (p.os !== "win32") chmodSync(join(dir, "bin", p.exe), 0o755)
  writeFileSync(
    join(dir, "package.json"),
    JSON.stringify(
      {
        name, version, description: `orbit binary for ${p.os}-${p.cpu}`, license: "MIT", author: main.author,
        os: [p.os], cpu: [p.cpu], files: ["bin"], publishConfig: { access: "public" },
      },
      null, 2,
    ) + "\n",
  )
  copyFileSync(join(root, "LICENSE"), join(dir, "LICENSE"))
}

const dir = join(out, "orbit")
cpSync(join(root, "npm/orbit/bin"), join(dir, "bin"), { recursive: true })
chmodSync(join(dir, "bin", "orbit.js"), 0o755)
for (const f of ["orbit.schema.json", "README.md", "LICENSE", "THIRD_PARTY_NOTICES.md"]) copyFileSync(join(root, f), join(dir, f))
main.version = version
main.optionalDependencies = Object.fromEntries(platforms.map((p) => [`@jgoterris/orbit-${p.os}-${p.cpu}`, version]))
writeFileSync(join(dir, "package.json"), JSON.stringify(main, null, 2) + "\n")

console.log(`${platforms.length} platform packages + launcher @ ${version} in ${out}`)
