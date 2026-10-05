import { createHash } from "node:crypto"
import { tmpdir } from "node:os"
import { dirname, join } from "node:path"

/** Longest socket path the kernel accepts (sun_path minus the NUL): 104 bytes on macOS, 108 on Linux. */
const MAX_SOCKET_PATH = process.platform === "darwin" ? 103 : 107

/**
 * Where a project's supervisor listens: `<stateDir>/orbit.sock`, or a short path when that one is too long
 * for a Unix socket. On Windows it is a named pipe (node:net takes both through the same API).
 */
export function socketPath(stateDir: string): string {
  const hash = createHash("sha1").update(stateDir).digest("hex").slice(0, 12)
  if (process.platform === "win32") return `\\\\.\\pipe\\orbit-${hash}`
  const direct = join(stateDir, "orbit.sock")
  if (Buffer.byteLength(direct) <= MAX_SOCKET_PATH) return direct
  const base = process.env.XDG_RUNTIME_DIR ? join(process.env.XDG_RUNTIME_DIR, "orbit") : join(tmpdir(), `orbit-${process.getuid?.() ?? 0}`)
  return join(base, `${hash}.sock`)
}

/** Directory that must exist (mode 0700) before listening; undefined for named pipes. */
export function socketDir(path: string): string | undefined {
  return process.platform === "win32" ? undefined : dirname(path)
}
