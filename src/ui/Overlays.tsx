import { fit, theme } from "./theme.ts"

export interface Command {
  id: string
  label: string
  hint?: string
  run: () => void
}

/** Subsequence fuzzy match; lower score is better, undefined = no match. */
export function fuzzyScore(query: string, text: string): number | undefined {
  if (!query) return 0
  const q = query.toLowerCase()
  const t = text.toLowerCase()
  const direct = t.indexOf(q)
  if (direct !== -1) return direct
  let score = 100
  let ti = 0
  for (const ch of q) {
    if (ch === " ") continue
    const found = t.indexOf(ch, ti)
    if (found === -1) return undefined
    score += found - ti
    ti = found + 1
  }
  return score
}

export function filterCommands(commands: Command[], query: string): Command[] {
  return commands
    .map((c) => ({ c, s: fuzzyScore(query, c.label) }))
    .filter((x): x is { c: Command; s: number } => x.s !== undefined)
    .sort((a, b) => a.s - b.s)
    .map((x) => x.c)
}

function Modal({ title, width, height, children }: { title: string; width: number; height: number; children: React.ReactNode }) {
  return (
    <box position="absolute" top={0} left={0} width="100%" height="100%" zIndex={10} alignItems="center" justifyContent="center">
      <box
        width={width}
        height={height}
        flexDirection="column"
        border
        borderStyle="rounded"
        borderColor={theme.accent}
        backgroundColor={theme.panelAlt}
        paddingLeft={1}
        paddingRight={1}
        title={` ${title} `}
        titleColor={theme.accent}
      >
        {children}
      </box>
    </box>
  )
}

export function CommandPalette({
  commands,
  selected,
  onQuery,
  width,
}: {
  commands: Command[]
  selected: number
  onQuery: (q: string) => void
  width: number
}) {
  const w = Math.min(72, width - 4)
  const maxRows = 14
  const start = Math.max(0, Math.min(selected - Math.floor(maxRows / 2), commands.length - maxRows))
  const visible = commands.slice(start, start + maxRows)
  return (
    <Modal title="Command palette" width={w} height={maxRows + 5}>
      <box height={1} flexDirection="row">
        <text fg={theme.accent}>{"› "}</text>
        <input
          flexGrow={1}
          focused
          placeholder="type a command or service…"
          onInput={onQuery}
          backgroundColor={theme.panelAlt}
          focusedBackgroundColor={theme.panelAlt}
          textColor={theme.text}
          placeholderColor={theme.dim}
        />
      </box>
      <text fg={theme.border}>{"─".repeat(w - 4)}</text>
      {visible.length === 0 ? <text fg={theme.dim}>no matching commands</text> : null}
      {visible.map((c, i) => {
        const isSel = start + i === selected
        return (
          <box key={c.id} height={1} backgroundColor={isSel ? theme.selection : undefined}>
            <text>
              <span fg={isSel ? theme.accent : theme.dim}>{isSel ? "▸ " : "  "}</span>
              <span fg={isSel ? theme.text : theme.muted}>{fit(c.label, w - 8 - (c.hint?.length ?? 0))}</span>
              <span fg={theme.dim}>{c.hint ?? ""}</span>
            </text>
          </box>
        )
      })}
    </Modal>
  )
}

const HELP: Array<[string, string]> = [
  ["↑↓ / j k", "select service"],
  ["← → (graph)", "move across the dependency graph"],
  ["space", "start / stop selected (with dependencies)"],
  ["s · x · r", "start · stop · restart selected"],
  ["S · X · R", "start · stop · restart everything"],
  ["1 2 3", "dashboard · graph · logs"],
  ["tab / shift+tab", "move focus between panels"],
  ["z · esc", "zoom the focused panel · back"],
  ["+ - =", "resize the focused panel · reset"],
  ["enter / l", "logs of the selected service"],
  ["a", "logs: toggle selected ⇄ all services"],
  ["/", "filter logs (regex)  ·  esc clears"],
  ["f · pgup pgdn · wheel", "follow · scroll logs"],
  ["(logs focused)", "j k · ctrl+u/d · g G  scroll"],
  ["t · c", "toggle timestamps · clear logs"],
  ["o", "open service URL in the browser"],
  ["L", "open the service's git repo in lazygit"],
  [": / ctrl+p", "command palette"],
  ["q", "quit: stop all services or leave them running"],
]

export function HelpOverlay({ width }: { width: number }) {
  const w = Math.min(70, width - 4)
  return (
    <Modal title="Keys" width={w} height={HELP.length + 4}>
      {HELP.map(([k, v]) => (
        <text key={k}>
          <span fg={theme.accent}>{fit(k, 24)}</span>
          <span fg={theme.text}>{v}</span>
        </text>
      ))}
      <text fg={theme.dim}>press any key to close</text>
    </Modal>
  )
}

export function ConfirmOverlay({ message, width, busy }: { message: string; width: number; busy?: boolean }) {
  const w = Math.min(60, width - 4)
  return (
    <Modal title={busy ? "Stopping" : "Quit"} width={w} height={busy ? 6 : 7}>
      <text fg={theme.text}>{message}</text>
      <text>
        {busy ? (
          <span fg={theme.dim}>please wait…</span>
        ) : (
          <>
            <span fg={theme.green}>s</span>
            <span fg={theme.dim}> stop all & quit</span>
          </>
        )}
      </text>
      {busy ? null : (
        <>
          <text>
            <span fg={theme.accent}>d</span>
            <span fg={theme.dim}> leave running & quit (reopen orbit to resume)</span>
          </text>
          <text>
            <span fg={theme.red}>n / esc</span>
            <span fg={theme.dim}> cancel</span>
          </text>
        </>
      )}
    </Modal>
  )
}
