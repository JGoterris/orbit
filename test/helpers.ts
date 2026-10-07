import { isWindows } from "../src/core/platform/index.ts"

/** Tests that need a POSIX shell (`;`, `&`, `$VAR`, `exit N`...) are skipped on Windows. */
export const win = isWindows

/** A command that just stays alive for `seconds`: `sleep` on POSIX, a Bun one-liner where there is none (Windows). */
export function sleepCmd(seconds: number): string {
  return isWindows ? `bun -e "setTimeout(()=>{},${Math.round(seconds * 1000)})"` : `sleep ${seconds}`
}
