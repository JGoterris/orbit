import { statSync } from "node:fs"
import { describeDiff, type ConfigDiff } from "../config/diff.ts"
import type { EnvEntry } from "../config/envFiles.ts"
import { expandPath, looksLikePath, sortProjects, type ProjectEntry, type ProjectStatus } from "../core/projects.ts"
import { fit, theme } from "./theme.ts"
import type { Palette } from "./themes.ts"

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

export function Modal({ title, width, height, children }: { title: string; width: number; height: number; children: React.ReactNode }) {
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

export interface ThemeEntry {
  name: string
  palette: Palette
  custom: boolean
}

const SWATCH_KEYS = ["accent", "accent2", "cyan", "green", "yellow", "orange", "red"] as const

export function ThemePicker({
  entries,
  selected,
  current,
  width,
  height,
}: {
  entries: ThemeEntry[]
  selected: number
  /** the theme that is saved right now */
  current: string
  width: number
  height: number
}) {
  const w = Math.min(60, width - 4)
  const maxRows = Math.max(1, Math.min(14, height - 6))
  const start = Math.max(0, Math.min(selected - Math.floor(maxRows / 2), entries.length - maxRows))
  const visible = entries.slice(start, start + maxRows)
  const nameW = Math.max(1, w - 4 - 2 - SWATCH_KEYS.length - 9)
  return (
    <Modal title="Theme" width={w} height={visible.length + 4}>
      {visible.map((e, i) => {
        const isSel = start + i === selected
        return (
          <box key={e.name} height={1} backgroundColor={isSel ? theme.selection : undefined}>
            <text>
              <span fg={isSel ? theme.accent : theme.dim}>{isSel ? "▸ " : "  "}</span>
              <span fg={isSel ? theme.text : theme.muted}>{fit(e.name + (e.custom ? " (custom)" : ""), nameW)}</span>
              <span fg={theme.green}>{e.name === current ? "✓ " : "  "}</span>
              {SWATCH_KEYS.map((k) => (
                <span key={k} fg={e.palette[k]}>
                  ■
                </span>
              ))}
            </text>
          </box>
        )
      })}
      <text fg={theme.dim}>↑↓ preview · enter save · esc cancel</text>
    </Modal>
  )
}

const HELP: Array<[string, string]> = [
  ["↑↓ / j k", "select service"],
  ["← → (graph)", "move across the dependency graph"],
  ["space", "start / stop selected (with dependencies)"],
  ["s · x · r", "start · stop · restart selected"],
  ["S · X · R", "start · stop · restart everything"],
  ["W", "pause / resume file watching of the selected service"],
  ["1 2 3 4", "dashboard · graph · logs · git"],
  ["tab / shift+tab", "move focus between panels"],
  ["z · esc", "zoom the focused panel · back"],
  ["+ - =", "resize the focused panel · reset"],
  ["enter / l", "logs of the selected service"],
  ["a", "logs: toggle selected ⇄ all services"],
  ["/", "filter logs (regex)  ·  esc clears"],
  ["f · pgup pgdn · wheel", "follow · scroll logs"],
  ["(logs focused)", "j k · ctrl+u/d · g G  scroll"],
  ["t · w · c", "toggle timestamps · line wrap · clear logs"],
  ["b", "fold / unfold all stack traces (enter/space in copy mode: just the one under the cursor)"],
  ["/ then tab", "filter bar ⇄ search bar (highlight, keep all lines)"],
  ["n · N", "next (newer) · previous (older) search match"],
  ["v (logs focused)", "copy mode: j k ctrl+u/d move, v select, enter/space on a trace opens it, y copy"],
  ["Y · E", "copy all visible logs · export them to a file"],
  ["(git changes)", "tree: space stage file/folder · a all · c commit · enter fold folder / open diff"],
  ["(git) f p u · L", "fetch · pull · push the selected repo · open it in lazygit"],
  ["(git repos) space a", "mark a repo for multi-repo actions · mark all / none"],
  ["(git) m then f p u b s", "multi-repo on the marked repos (all if none): fetch · pull · push · new branch · switch branch"],
  ["(git diff) [ ] { }", "previous/next hunk · previous/next file (read only)"],
  ["(git commands)", "j k move · y copy the command · c clear"],
  ["P · U", "open another project · apply orbit.yaml edits (restarts only what changed)"],
  ["T", "change color theme (live preview, enter saves)"],
  ["h", "dashboard: cpu / memory chart range 2m ⇄ 15m ⇄ 1h (memory turns orange/red near its limit, ↗ = leak)"],
  ["e", "environment variables of the selected service"],
  ["i", "interactive console: psql, rails console, container shell (ctrl+] hides it)"],
  ["o", "open service URL in the browser"],
  ["L", "open the service's git repo in lazygit"],
  [": / ctrl+p", "command palette"],
  ["q", "quit: stop all services or leave them running"],
]

const HELP_KEY_W = 24

export function HelpOverlay({ width, height }: { width: number; height: number }) {
  const w = Math.min(104, width - 4)
  const h = Math.min(HELP.length + 3, Math.max(4, height - 2))
  // inner width = modal - border (2) - padding (2); a longer description is cut with … instead of wrapping
  const descW = Math.max(1, w - 4 - HELP_KEY_W)
  return (
    <Modal title="Keys" width={w} height={h}>
      {HELP.slice(0, h - 3).map(([k, v]) => (
        <text key={k}>
          <span fg={theme.accent}>{fit(k, HELP_KEY_W)}</span>
          <span fg={theme.text}>{fit(v, descW)}</span>
        </text>
      ))}
      <text fg={theme.dim}>{h - 3 < HELP.length ? "terminal too short: enlarge it to see all keys" : "press any key to close"}</text>
    </Modal>
  )
}

export function ConfirmOverlay({
  message,
  width,
  busy,
  action = "quit",
}: {
  message: string
  width: number
  busy?: boolean
  /** what happens once the services are dealt with */
  action?: "quit" | "switch"
}) {
  const w = Math.min(60, width - 4)
  const verb = action === "quit" ? "quit" : "switch project"
  return (
    <Modal title={busy ? "Stopping" : action === "quit" ? "Quit" : "Switch project"} width={w} height={busy ? 6 : 7}>
      <text fg={theme.text}>{message}</text>
      <text>
        {busy ? (
          <span fg={theme.dim}>please wait…</span>
        ) : (
          <>
            <span fg={theme.green}>s</span>
            <span fg={theme.dim}>{` stop all & ${verb}`}</span>
          </>
        )}
      </text>
      {busy ? null : (
        <>
          <text>
            <span fg={theme.accent}>d</span>
            <span fg={theme.dim}>{` leave running & ${verb} (reopen that project to resume them)`}</span>
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

export function ReloadOverlay({ diff, width, height }: { diff: ConfigDiff; width: number; height: number }) {
  const w = Math.min(72, width - 4)
  const lines = describeDiff(diff)
  const shown = Math.max(1, Math.min(lines.length, height - 10))
  const colorOf = (l: string) => (l.startsWith("+") ? theme.green : l.startsWith("-") ? theme.red : l.startsWith("!") ? theme.yellow : theme.accent)
  const restarts = diff.changed.filter((c) => c.restart).length
  return (
    <Modal title="orbit.yaml changed" width={w} height={shown + 7}>
      {lines.slice(0, shown).map((l) => (
        <text key={l} fg={colorOf(l)}>{fit(l, w - 4)}</text>
      ))}
      {lines.length > shown ? <text fg={theme.dim}>{`… and ${lines.length - shown} more`}</text> : null}
      <text fg={theme.dim}>{`${restarts} restart${restarts === 1 ? "" : "s"} (↻) · only running services are restarted`}</text>
      <text>
        <span fg={theme.green}>y / enter</span>
        <span fg={theme.dim}> apply</span>
      </text>
      <text>
        <span fg={theme.red}>n / esc</span>
        <span fg={theme.dim}> not now (U applies later)</span>
      </text>
    </Modal>
  )
}

export interface ProjectRow {
  key: string
  label: string
  hint: string
  /** directory this row opens */
  path: string
  /** registry entry behind the row (absent for a typed path) */
  entry?: ProjectEntry
  /** the typed path is not a folder, or a remembered project's folder is gone */
  missing?: boolean
}

const isDir = (p: string) => {
  try {
    return statSync(p).isDirectory()
  } catch {
    return false
  }
}

/**
 * Rows of the project picker: a typed path first ("open this folder"), then the registry
 * (pinned, then recent) fuzzy-filtered by what was typed.
 */
export function projectRows(
  entries: ProjectEntry[],
  statuses: ReadonlyMap<string, ProjectStatus>,
  query: string,
  currentRoot: string,
): ProjectRow[] {
  const q = query.trim()
  const rows: ProjectRow[] = []
  const typedPath = looksLikePath(q)
  if (typedPath) {
    const path = expandPath(q)
    rows.push({ key: `open:${path}`, label: `Open folder ${path}`, hint: isDir(path) ? "" : "not a folder", path, missing: !isDir(path) })
  }
  const scored = sortProjects(entries)
    .map((e, i) => ({ e, i, s: typedPath ? fuzzyScore(expandPath(q), e.path) : fuzzyScore(q, `${e.name} ${e.path}`) }))
    .filter((x): x is { e: ProjectEntry; i: number; s: number } => x.s !== undefined)
    .sort((a, b) => a.s - b.s || a.i - b.i)
  for (const { e } of scored) {
    const st = statuses.get(e.path)
    const flags = [
      e.path === currentRoot ? "current" : "",
      st?.openIn ? `open in pid ${st.openIn}` : "",
      st?.running ? `● ${st.running} up` : "",
      st && !st.exists ? "missing" : "",
    ].filter(Boolean)
    rows.push({
      key: e.path,
      label: `${e.pinned ? "★ " : ""}${e.name}  ${e.path}`,
      hint: flags.join(" · "),
      path: e.path,
      entry: e,
      missing: st ? !st.exists : false,
    })
  }
  return rows
}

export function ProjectPicker({
  rows,
  selected,
  value,
  inputKey,
  onQuery,
  width,
  height,
}: {
  rows: ProjectRow[]
  selected: number
  value: string
  /** changes when `value` is set from outside (tab completion) so the input remounts with it */
  inputKey: number
  onQuery: (q: string) => void
  width: number
  height: number
}) {
  const w = Math.min(90, width - 4)
  const maxRows = Math.max(1, Math.min(14, height - 8))
  const start = Math.max(0, Math.min(selected - Math.floor(maxRows / 2), rows.length - maxRows))
  const visible = rows.slice(start, start + maxRows)
  return (
    <Modal title="Projects" width={w} height={visible.length + 6}>
      <box height={1} flexDirection="row">
        <text fg={theme.accent}>{"› "}</text>
        <input
          key={inputKey}
          flexGrow={1}
          focused
          value={value}
          placeholder="search projects, or type a path (/, ~, ./) and press tab to complete"
          onInput={onQuery}
          backgroundColor={theme.panelAlt}
          focusedBackgroundColor={theme.panelAlt}
          textColor={theme.text}
          placeholderColor={theme.dim}
        />
      </box>
      <text fg={theme.border}>{"─".repeat(w - 4)}</text>
      {visible.length === 0 ? <text fg={theme.dim}>no projects yet: type a folder path to open one</text> : null}
      {visible.map((r, i) => {
        const isSel = start + i === selected
        return (
          <box key={r.key} height={1} backgroundColor={isSel ? theme.selection : undefined}>
            <text>
              <span fg={isSel ? theme.accent : theme.dim}>{isSel ? "▸ " : "  "}</span>
              <span fg={r.missing ? theme.red : isSel ? theme.text : theme.muted}>{fit(r.label, w - 8 - r.hint.length)}</span>
              <span fg={r.missing ? theme.red : theme.dim}>{r.hint}</span>
            </text>
          </box>
        )
      })}
      <box flexGrow={1} />
      <text fg={theme.dim}>enter open · tab complete path · ctrl+f pin · ctrl+x forget · esc close</text>
    </Modal>
  )
}

const SECRET_RE = /KEY|TOKEN|SECRET|PASSWORD|PASS|PWD|CREDENTIAL|PRIVATE/i
export const isSecretKey = (key: string) => SECRET_RE.test(key)

/** Rows of the env overlay that fit at a given terminal height (modal chrome and footer excluded). */
export const envPageSize = (height: number) => Math.max(3, Math.min(height - 4, 30) - 5)

export function EnvOverlay({
  service,
  entries,
  missing,
  scroll,
  reveal,
  width,
  height,
}: {
  service: string
  entries: EnvEntry[]
  missing: string[]
  scroll: number
  reveal: boolean
  width: number
  height: number
}) {
  const w = Math.min(100, width - 4)
  const h = Math.min(height - 4, 30)
  const page = envPageSize(height)
  const start = Math.min(scroll, Math.max(0, entries.length - page))
  const shown = entries.slice(start, start + page)
  const keyW = Math.min(32, Math.max(8, ...entries.map((e) => e.key.length)))
  const srcW = Math.min(24, Math.max(6, ...entries.map((e) => e.source.length)))
  const valW = Math.max(8, w - 4 - keyW - srcW - 2)
  return (
    <Modal title={`Env · ${service} (${entries.length})`} width={w} height={h}>
      {shown.length ? (
        shown.map((e) => {
          const hidden = !reveal && isSecretKey(e.key)
          return (
            <text key={e.key}>
              <span fg={theme.accent}>{fit(e.key, keyW + 1)}</span>
              <span fg={hidden ? theme.dim : theme.text}>{fit(hidden ? "••••••" : e.value, valW + 1)}</span>
              <span fg={theme.dim}>{fit(e.source, srcW)}</span>
            </text>
          )
        })
      ) : (
        <text fg={theme.dim}>no variables defined for this service</text>
      )}
      <box flexGrow={1} />
      {missing.length ? <text fg={theme.yellow}>{fit(`missing env_file: ${missing.join(", ")}`, w - 4)}</text> : null}
      <text fg={theme.dim}>
        {`j/k scroll · v ${reveal ? "hide" : "reveal"} secrets · esc close${entries.length > page ? `  (${start + 1}-${start + shown.length}/${entries.length})` : ""}`}
      </text>
    </Modal>
  )
}

/** One-line text prompt (commit message, branch name…). Enter and esc are handled by the owner's key handler. */
export function PromptOverlay({
  title,
  hint,
  value,
  onInput,
  width,
}: {
  title: string
  hint: string
  value: string
  onInput: (v: string) => void
  width: number
}) {
  const w = Math.min(80, width - 4)
  return (
    <Modal title={title} width={w} height={5}>
      <box height={1} flexDirection="row">
        <text fg={theme.accent}>{"› "}</text>
        <input
          flexGrow={1}
          focused
          value={value}
          onInput={onInput}
          backgroundColor={theme.panelAlt}
          focusedBackgroundColor={theme.panelAlt}
          textColor={theme.text}
          placeholderColor={theme.dim}
        />
      </box>
      <text fg={theme.border}>{"─".repeat(w - 4)}</text>
      <text fg={theme.dim}>{hint}</text>
    </Modal>
  )
}

export function YesNoOverlay({ title, message, width }: { title: string; message: string; width: number }) {
  const w = Math.min(70, width - 4)
  return (
    <Modal title={title} width={w} height={6}>
      <text fg={theme.text}>{fit(message, w - 4)}</text>
      <text>
        <span fg={theme.red}>y / enter</span>
        <span fg={theme.dim}> confirm</span>
      </text>
      <text>
        <span fg={theme.accent}>n / esc</span>
        <span fg={theme.dim}> cancel</span>
      </text>
    </Modal>
  )
}
