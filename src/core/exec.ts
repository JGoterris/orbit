export interface ExecResult {
  code: number
  stdout: string
  stderr: string
}

/** Runs a command to completion and captures its output. Never throws for non-zero exits. */
export async function exec(
  argv: string[],
  opts: {
    cwd?: string
    env?: Record<string, string | undefined>
    timeout?: number
    /** written to the command's stdin (default: stdin is closed) */
    input?: string
    /** run in a new session, without a controlling terminal: ssh/gpg cannot prompt over the TUI, they fail instead */
    detached?: boolean
  } = {},
): Promise<ExecResult> {
  try {
    const proc = Bun.spawn(argv, {
      cwd: opts.cwd,
      env: opts.env as Record<string, string> | undefined,
      stdout: "pipe",
      stderr: "pipe",
      stdin: opts.input === undefined ? "ignore" : Buffer.from(opts.input),
      detached: opts.detached,
    })
    let timer: ReturnType<typeof setTimeout> | undefined
    if (opts.timeout) timer = setTimeout(() => proc.kill("SIGKILL"), opts.timeout)
    const [stdout, stderr, code] = await Promise.all([
      new Response(proc.stdout).text(),
      new Response(proc.stderr).text(),
      proc.exited,
    ])
    if (timer) clearTimeout(timer)
    return { code, stdout, stderr }
  } catch (err) {
    return { code: 127, stdout: "", stderr: (err as Error).message }
  }
}

export function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    const t = setTimeout(resolve, ms)
    signal?.addEventListener("abort", () => {
      clearTimeout(t)
      resolve()
    })
  })
}

/** Is something listening on host:port? */
export function isPortOpen(port: number, host = "127.0.0.1", timeout = 800): Promise<boolean> {
  return new Promise((resolve) => {
    let done = false
    const finish = (v: boolean, sock?: { end(): void }) => {
      if (done) return
      done = true
      clearTimeout(timer)
      try {
        sock?.end()
      } catch {}
      resolve(v)
    }
    const timer = setTimeout(() => finish(false), timeout)
    Bun.connect({
      hostname: host,
      port,
      socket: {
        open: (s) => finish(true, s),
        error: () => finish(false),
        connectError: () => finish(false),
        data() {},
      },
    }).catch(() => finish(false))
  })
}

/** Best effort: which process listens on a TCP port (Linux 'ss'). */
export async function whoListens(port: number): Promise<string | undefined> {
  const res = await exec(["ss", "-ltnpH", `sport = :${port}`], { timeout: 2000 })
  if (res.code !== 0) return
  const m = /users:\(\("([^"]+)",pid=(\d+)/.exec(res.stdout)
  return m ? `${m[1]} (pid ${m[2]})` : res.stdout.trim() ? "another process" : undefined
}

/** Opens a URL in the host browser (Linux, WSL, macOS). */
export async function openUrl(url: string): Promise<boolean> {
  const candidates =
    process.platform === "darwin"
      ? [["open", url]]
      : [
          ["xdg-open", url],
          ["wslview", url],
          ["cmd.exe", "/c", "start", "", url.replace(/&/g, "^&")],
        ]
  for (const argv of candidates) {
    if (!Bun.which(argv[0]!)) continue
    const res = await exec(argv, { timeout: 5000 })
    if (res.code === 0) return true
  }
  return false
}
