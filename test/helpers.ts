import { isWindows } from "../src/core/platform/index.ts"

/** Tests that need a POSIX shell (`;`, `&`, `$VAR`, `exit N`...) are skipped on Windows. */
export const win = isWindows

/** A command that just stays alive for `seconds`: `sleep` on POSIX, a Bun one-liner where there is none (Windows). */
export function sleepCmd(seconds: number): string {
  return isWindows ? `bun -e "setTimeout(()=>{},${Math.round(seconds * 1000)})"` : `sleep ${seconds}`
}

/** Prints `text`, then stays alive for `seconds`: `echo text; sleep n` on POSIX, a Bun one-liner on Windows (cmd has no `;`). */
export function echoSleepCmd(text: string, seconds: number): string {
  return isWindows ? `bun -e "console.log('${text}'); setTimeout(()=>{},${Math.round(seconds * 1000)})"` : `echo ${text}; sleep ${seconds}`
}

/** A temp root with a short path: some assertions read paths off a fixed-width screen, and macOS' tmpdir() is long. */
export const shortTmp = isWindows ? (process.env.TEMP ?? "C:\\Temp") : "/tmp"

/**
 * The message a promise rejects with ("resolved" if it does not). Use it instead of `expect(promise).rejects` for requests
 * over a socket: under bun test on Windows, that form stops seeing replies from the event loop after the first one.
 */
export const rejection = (p: Promise<unknown>): Promise<string> => p.then(() => "resolved", (e: Error) => e.message)
