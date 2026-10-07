// Windows replacement for the `sh -c '...; echo $? > exitfile'` wrapper of ProcessRunner:
// runs the command (stdio inherited, so the output goes to the service's log files) and leaves its exit code in a file.
// usage: bun wrap.ts <exit file> <command...>
import { writeFileSync } from "node:fs"

const [exitFile, ...argv] = process.argv.slice(2)
if (!exitFile || argv.length === 0) process.exit(2)

const proc = Bun.spawn(argv, { stdin: "ignore", stdout: "inherit", stderr: "inherit" })
const code = await proc.exited
try {
  writeFileSync(exitFile, String(code))
} catch {}
process.exit(code)
