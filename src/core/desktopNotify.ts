import { readFileSync } from "node:fs"
import { readUserConfig } from "./userConfig.ts"
import type { Status, SupervisorLike } from "./supervisor.ts"

export interface Notice {
  title: string
  body: string
  /** a service went down (as opposed to coming back) */
  urgent: boolean
}

export interface BackendEnv {
  platform: string
  wsl: boolean
  which: (cmd: string) => string | null
}

export interface Backend {
  name: "powershell" | "osascript" | "notify-send"
  argv(n: Notice): string[]
}

const MAX_BODY = 240

/** PowerShell also treats the typographic single quotes as quote characters inside '…'. */
const psString = (s: string) => `'${s.replace(/['‘’‚‛]/g, "''")}'`

/** A Windows toast. The texts are PowerShell strings assigned with InnerText: no script or XML injection. */
export function powershellScript(n: Notice): string {
  return [
    "$ErrorActionPreference = 'Stop'",
    "[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] > $null",
    "$t = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02)",
    "$x = $t.GetElementsByTagName('text')",
    `$x.Item(0).InnerText = ${psString(n.title)}`,
    `$x.Item(1).InnerText = ${psString(n.body.slice(0, MAX_BODY))}`,
    "$app = '{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\\WindowsPowerShell\\v1.0\\powershell.exe'",
    "[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($app).Show([Windows.UI.Notifications.ToastNotification]::new($t))",
  ].join("\n")
}

/** The program that shows a desktop notification on this system, if there is one. */
export function pickBackend(env: BackendEnv): Backend | undefined {
  const ps = env.which("powershell.exe") ?? env.which("powershell")
  // WSLg rarely shows notify-send, so on WSL the Windows side is the one that reaches the user
  if ((env.wsl || env.platform === "win32") && ps) {
    return {
      name: "powershell",
      argv: (n) => [ps, "-NoProfile", "-NonInteractive", "-EncodedCommand", Buffer.from(powershellScript(n), "utf16le").toString("base64")],
    }
  }
  const osascript = env.platform === "darwin" ? env.which("osascript") : null
  if (osascript) {
    return {
      name: "osascript",
      // the texts travel as argv, never inside the script
      argv: (n) => [osascript, "-e", "on run argv", "-e", "display notification (item 2 of argv) with title (item 1 of argv)", "-e", "end run", n.title, n.body.slice(0, MAX_BODY)],
    }
  }
  const notifySend = env.platform === "linux" ? env.which("notify-send") : null
  if (notifySend) {
    return {
      name: "notify-send",
      argv: (n) => [notifySend, "-a", "orbit", "-u", n.urgent ? "critical" : "normal", "--", n.title, n.body.slice(0, MAX_BODY)],
    }
  }
}

function isWsl(): boolean {
  if (process.env.WSL_DISTRO_NAME) return true
  try {
    return /microsoft/i.test(readFileSync("/proc/version", "utf8"))
  } catch {
    return false
  }
}

let cached: { backend?: Backend } | undefined

export function defaultBackend(): Backend | undefined {
  return (cached ??= { backend: pickBackend({ platform: process.platform, wsl: isWsl(), which: (c) => Bun.which(c) }) }).backend
}

/** Fire and forget: a notification that cannot be shown is not worth an error. */
export function sendDesktop(n: Notice, backend = defaultBackend()) {
  if (!backend) return
  try {
    const proc = Bun.spawn(backend.argv(n), { stdin: "ignore", stdout: "ignore", stderr: "ignore" })
    const timer = setTimeout(() => proc.kill(), 10_000)
    timer.unref()
    void proc.exited.finally(() => clearTimeout(timer))
    proc.unref()
  } catch {}
}

type Kind = "crashed" | "failed" | "unhealthy" | "recovered"
interface Event {
  kind: Kind
  name: string
  detail?: string
}

export interface NotifierOptions {
  send?: (n: Notice) => void
  /** asked every time something is about to be sent, so toggling it takes effect without a restart */
  enabled?: () => boolean
  /** events this close together go out as one notification (a failure often takes its dependents with it) */
  groupMs?: number
  /** the same event of the same service is reported at most this often (a restart loop) */
  throttleMs?: number
  now?: () => number
}

const describe = (e: Event) =>
  e.kind === "recovered"
    ? `✔  ${e.name} recovered`
    : e.kind === "unhealthy"
      ? `●  ${e.name} is unhealthy${e.detail ? `: ${e.detail}` : ""}`
      : `✖  ${e.name} ${e.kind}${e.detail ? `: ${e.detail}` : ""}`

/**
 * Sends a desktop notification when a service of `sup` crashes, fails or turns unhealthy, and when it
 * comes back. Meant for whoever runs the services (the daemon), so it works with every terminal closed.
 * Returns what stops it.
 */
export function attachDesktopNotifier(
  sup: Pick<SupervisorLike, "on" | "off" | "state" | "names" | "config">,
  opts: NotifierOptions = {},
): () => void {
  const send = opts.send ?? ((n: Notice) => sendDesktop(n))
  const enabled = opts.enabled ?? (() => readUserConfig().notifications !== false)
  const groupMs = opts.groupMs ?? 1500
  const throttleMs = opts.throttleMs ?? 60_000
  const now = opts.now ?? Date.now

  const last = new Map<string, Status>(sup.names.map((n) => [n, sup.state(n).status]))
  const down = new Set<string>()
  const sent = new Map<string, number>()
  let pending: Event[] = []
  let timer: ReturnType<typeof setTimeout> | undefined

  const flush = () => {
    timer = undefined
    const events = pending
    pending = []
    if (!events.length || !enabled()) return
    const downs = events.filter((e) => e.kind !== "recovered")
    const ups = events.filter((e) => e.kind === "recovered")
    const lines: string[] = []
    if (downs.length === 1) lines.push(describe(downs[0]!))
    else if (downs.length > 1) lines.push(`✖  ${downs.length} services down: ${downs.map((e) => e.name).join(", ")}`)
    if (ups.length === 1) lines.push(describe(ups[0]!))
    else if (ups.length > 1) lines.push(`✔  ${ups.length} services recovered: ${ups.map((e) => e.name).join(", ")}`)
    send({ title: `orbit · ${sup.config.name}`, body: lines.join("\n"), urgent: downs.length > 0 })
  }

  const onChange = (name?: string) => {
    if (!name) return
    const st = sup.state(name)
    const prev = last.get(name)
    last.set(name, st.status)
    if (prev === st.status) return
    let kind: Kind | undefined
    if (st.status === "crashed" || st.status === "failed") kind = st.status
    else if (st.status === "unhealthy") kind = "unhealthy"
    else if ((st.status === "healthy" || st.status === "running") && down.has(name)) kind = "recovered"
    if (!kind) return
    const key = `${name}:${kind}`
    const at = now()
    if (at - (sent.get(key) ?? -Infinity) < throttleMs) return
    sent.set(key, at)
    if (kind === "recovered") down.delete(name)
    else down.add(name)
    pending.push({ kind, name, detail: kind === "unhealthy" ? st.health : st.error })
    timer ??= setTimeout(flush, groupMs)
  }

  sup.on("change", onChange)
  return () => {
    sup.off("change", onChange)
    clearTimeout(timer)
  }
}
