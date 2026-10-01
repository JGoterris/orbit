import type { CliRenderer } from "@opentui/core"

export interface CopyResult {
  ok: boolean
  via?: string
  error?: string
}

// [binary, args] in order of preference once OSC 52 is not an option
const TOOLS: Array<[string, string[]]> = [
  ["wl-copy", []],
  ["xclip", ["-selection", "clipboard"]],
  ["xsel", ["--clipboard", "--input"]],
  ["pbcopy", []],
  ["clip.exe", []],
]

async function pipeTo(bin: string, args: string[], text: string): Promise<boolean> {
  try {
    const proc = Bun.spawn([bin, ...args], { stdin: "pipe", stdout: "ignore", stderr: "ignore" })
    proc.stdin.write(text)
    await proc.stdin.end()
    return (await proc.exited) === 0
  } catch {
    return false
  }
}

/**
 * Copies text to the system clipboard: OSC 52 when the terminal advertises it (works over ssh and in WSL),
 * otherwise a clipboard tool. If neither is known to work, OSC 52 is sent anyway as a best effort.
 */
export async function copyText(renderer: CliRenderer, text: string): Promise<CopyResult> {
  if (renderer.isOsc52Supported() && renderer.copyToClipboardOSC52(text)) return { ok: true, via: "terminal" }
  for (const [bin, args] of TOOLS) {
    if (Bun.which(bin) && (await pipeTo(bin, args, text))) return { ok: true, via: bin }
  }
  if (renderer.copyToClipboardOSC52(text)) return { ok: true, via: "terminal (OSC 52, unverified)" }
  return { ok: false, error: "no clipboard available (install wl-copy/xclip, or use a terminal with OSC 52)" }
}

/** Indirection so tests can capture what would be copied. */
export const clipboard = { copy: copyText }
