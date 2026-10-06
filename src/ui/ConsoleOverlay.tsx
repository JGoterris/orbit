import { EmbeddedTerminalRenderable } from "@opentui/core"
import { extend } from "@opentui/react"
import { useEffect, useRef } from "react"
import type { ConsoleSession } from "../core/console.ts"
import { fit, theme } from "./theme.ts"

extend({ terminal: EmbeddedTerminalRenderable })

declare module "@opentui/react" {
  interface OpenTUIComponents {
    terminal: typeof EmbeddedTerminalRenderable
  }
}

/** Size of the floating console and of the terminal inside it (border and footer line excluded). */
export function consoleSize(width: number, height: number) {
  const w = Math.max(20, Math.floor(width * 0.9))
  const h = Math.max(6, Math.floor(height * 0.85))
  return { w, h, cols: w - 2, rows: h - 3 }
}

export function ConsoleOverlay({ service, session, width, height }: { service: string; session: ConsoleSession; width: number; height: number }) {
  const { w, h, cols, rows } = consoleSize(width, height)
  const term = useRef<EmbeddedTerminalRenderable>(null)

  useEffect(() => {
    const t = term.current
    if (!t) return
    // the emulator answers the queries it sees (cursor position...): not while replaying what the program already got
    let replaying = true
    t.onData = (data, source) => {
      if (replaying && source === "response") return
      session.write(data)
    }
    t.onTerminalResize = (c, r) => session.resize(c, r)
    t.write(session.backlog)
    replaying = false
    const onData = (chunk: Uint8Array) => t.write(chunk)
    session.on("data", onData)
    t.focus()
    return () => {
      session.off("data", onData)
      if (t.onData) t.onData = undefined
      t.blur()
    }
  }, [session])

  return (
    <box position="absolute" top={0} left={0} width="100%" height="100%" zIndex={20} alignItems="center" justifyContent="center">
      <box
        width={w}
        height={h}
        flexDirection="column"
        border
        borderStyle="rounded"
        borderColor={theme.accent}
        backgroundColor={theme.bg}
        title={` › ${service} · ${session.spec.title} `}
        titleColor={theme.accent}
      >
        <terminal ref={term} cols={cols} rows={rows} flexGrow={1} width="100%" />
        <text fg={theme.dim}>{fit(" ctrl+] hide (keeps running) · exit / ctrl+d end the session", cols)}</text>
      </box>
    </box>
  )
}
