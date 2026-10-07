// Windows replacement for the `sh -c '...; echo $? > exitfile'` wrapper of ProcessRunner:
// runs the command through the shell (stdio inherited, so the output goes to the service's log files) and leaves its
// exit code in a file. It builds the shell command line itself because only here the quoting can be kept verbatim.
// usage: bun wrap.ts <exit file> <command> [shell]
import { writeFileSync } from "node:fs"
import { shellCommand } from "./index.ts"

const [exitFile, cmd, shell] = process.argv.slice(2)
if (!exitFile || !cmd) process.exit(2)

const sh = shellCommand(cmd, shell || undefined)
const proc = Bun.spawn(sh.argv, { stdin: "ignore", stdout: "inherit", stderr: "inherit", windowsVerbatimArguments: sh.verbatim })
const code = await proc.exited
try {
  writeFileSync(exitFile, String(code))
} catch {}
process.exit(code)
