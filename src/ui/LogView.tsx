import type { BoxRenderable, MouseEvent } from "@opentui/core"
import { useEffect, useRef, useState } from "react"
import { clock, detectLevel, filterLines, foldTraces, isFolded, matcher, type LogLine } from "../core/logs.ts"
import { useSize } from "./hooks.ts"
import { fit, serviceColor, theme } from "./theme.ts"

interface Props {
  lines: readonly LogLine[]
  /** undefined = combined view with service prefixes */
  service?: string
  names: readonly string[]
  filter: string
  /** lines scrolled up from the bottom; 0 = following */
  scrollBack: number
  onScroll: (delta: number) => void
  title: string
  focused: boolean
  showTime: boolean
  /** wrap long lines onto continuation rows instead of cutting them with … */
  wrap: boolean
  /** copy mode: seq of the line under the cursor, and of the selection anchor (if any) */
  cursor?: number
  anchor?: number
  /** keep the view still even when following (copy mode) */
  freeze?: boolean
  /** search term (regex): matching lines are highlighted without hiding the others */
  search?: string
  /** seq of the match n/N is on */
  current?: number
  /** mouse: click in copy mode (anchor == cursor) or drag (anchor → cursor) */
  onSelect?: (anchor: number, cursor: number) => void
  /** collapse stack traces into their first line; `expanded` holds the heads (seq) the user opened */
  fold?: boolean
  expanded?: ReadonlySet<number>
  onToggleTrace?: (seq: number) => void
  onFocus?: () => void
}

const NONE: ReadonlySet<number> = new Set()

export function LogView({ lines, service, names, filter, scrollBack, onScroll, title, focused, showTime, wrap, cursor, anchor, freeze, search, current, onSelect, fold = false, expanded = NONE, onToggleTrace, onFocus }: Props) {
  const { ref, size, onSizeChange } = useSize<BoxRenderable>()
  const matching = filterLines(lines, filter)
  const filtered = fold ? foldTraces(matching, expanded) : matching
  const height = Math.max(1, size.height - 2)
  const width = Math.max(10, size.width - 4)
  const prefixW = service ? 0 : Math.min(14, Math.max(4, ...names.map((n) => n.length))) + 1
  const timeW = showTime ? 9 : 0
  const textW = Math.max(1, width - timeW - prefixW)
  const isHead = (l: LogLine) => fold && !!l.trace && l.trace.frames >= 2
  const bodyOf = (l: LogLine) => {
    if (l.stream === "system") return `» ${l.text}`
    if (!isHead(l)) return l.text
    if (!isFolded(l, expanded)) return `▾ ${l.text}`
    const t = l.trace!
    return `▸ ${l.text} ⋯ ${t.summary ? `${t.summary} · ` : ""}+${t.frames} lines`
  }
  const rowsOf = (l: LogLine) => (wrap ? Math.max(1, Math.ceil(bodyOf(l).length / textW)) : 1)

  // with wrap a line takes several rows: the top of the scroll range is where `height` rows fit from line 0
  let topRows = 0
  let topLines = 0
  while (topLines < filtered.length && topRows < height) topRows += rowsOf(filtered[topLines++]!)
  const maxBack = Math.max(0, filtered.length - topLines)
  const back = Math.min(scrollBack, maxBack)
  // keep the parent's offset within range so "jump to top" doesn't leave a huge dead scroll
  useEffect(() => {
    if (scrollBack > maxBack && size.height > 0) onScroll(maxBack - scrollBack)
  }, [scrollBack, maxBack, size.height, onScroll])

  // while scrolled up, new lines must not move what is being read (or copied): compensate the offset
  const seen = useRef({ key: service, seq: lines.at(-1)?.seq ?? 0 })
  const [unread, setUnread] = useState(0)
  const newest = lines.at(-1)?.seq ?? 0
  useEffect(() => {
    const prev = seen.current
    seen.current = { key: service, seq: newest }
    if (prev.key !== service || newest === prev.seq) return
    let added = 0
    for (let i = filtered.length - 1; i >= 0 && filtered[i]!.seq > prev.seq; i--) added++
    if ((scrollBack > 0 || freeze) && added > 0) {
      onScroll(added)
      setUnread((u) => u + added)
    }
  }, [newest, service]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (scrollBack === 0) setUnread(0)
  }, [scrollBack])

  const end = filtered.length - back
  let first = end
  let rows = 0
  while (first > 0 && rows < height) rows += rowsOf(filtered[--first]!)
  const visible = filtered.slice(first, end)

  // copy mode: the cursor must stay on screen when it moves
  const cursorIdx = cursor === undefined ? -1 : filtered.findIndex((l) => l.seq === cursor)
  const anchorIdx = anchor === undefined ? cursorIdx : filtered.findIndex((l) => l.seq === anchor)
  const [selLo, selHi] = cursorIdx < 0 ? [-1, -1] : [Math.min(cursorIdx, anchorIdx), Math.max(cursorIdx, anchorIdx)]
  const revealIdx = current !== undefined ? filtered.findIndex((l) => l.seq === current) : cursorIdx
  const revealKey = current ?? cursor
  useEffect(() => {
    if (revealIdx < 0 || size.height === 0) return
    if (revealIdx < first) onScroll(first - revealIdx)
    else if (revealIdx >= end) onScroll(-(revealIdx - end + 1))
  }, [revealKey]) // eslint-disable-line react-hooks/exhaustive-deps

  const isMatch = search ? matcher(search) : undefined

  const status = [
    cursor !== undefined ? (anchor !== undefined ? `${selHi - selLo + 1} selected` : "copy mode") : "",
    fold && filtered.some((l) => isFolded(l, expanded)) ? `${filtered.filter((l) => isFolded(l, expanded)).length} traces folded` : "",
    filter ? `/${filter}  ${filtered.length} matches` : `${filtered.length} lines`,
    back ? `↑ ${back}${unread ? ` · +${unread} new` : ""}  (f to follow)` : "following",
  ]
    .filter(Boolean)
    .join(" · ")

  // a line taller than the panel is clipped from its start, like a terminal would
  const shown = visible
    .flatMap((l, k) => {
      const body = bodyOf(l)
      const idx = first + k
      if (!wrap || body.length <= textW) return [{ l, idx, i: 0, text: fit(body, textW) }]
      const chunks: { l: LogLine; idx: number; i: number; text: string }[] = []
      for (let at = 0, i = 0; at < body.length; at += textW, i++) chunks.push({ l, idx, i, text: body.slice(at, at + textW).padEnd(textW) })
      return chunks
    })
    .slice(-height)

  const pressed = useRef<number | undefined>(undefined)
  const lineAt = (y: number) => shown[y - (ref.current?.y ?? 0) - 1]?.l

  return (
    <box
      ref={ref}
      onSizeChange={onSizeChange}
      flexGrow={1}
      flexDirection="column"
      border
      borderStyle="rounded"
      borderColor={focused ? theme.borderFocus : theme.border}
      backgroundColor={theme.panel}
      paddingLeft={1}
      paddingRight={1}
      title={` ${title} `}
      titleColor={theme.text}
      bottomTitle={` ${status} `}
      bottomTitleAlignment="right"
      onMouseDown={(e: MouseEvent) => {
        onFocus?.()
        const l = lineAt(e.y)
        pressed.current = l?.seq
        if (l && cursor !== undefined) onSelect?.(l.seq, l.seq)
        else if (l && isHead(l)) onToggleTrace?.(l.seq)
      }}
      onMouseDrag={(e: MouseEvent) => {
        const l = lineAt(e.y)
        if (l && pressed.current !== undefined) onSelect?.(pressed.current, l.seq)
      }}
      onMouseScroll={(e: MouseEvent) => onScroll(e.scroll?.direction === "up" ? 3 : e.scroll?.direction === "down" ? -3 : 0)}
    >
      {visible.length === 0 ? (
        <text fg={theme.dim}>{filter ? "no lines match the filter" : "no output yet"}</text>
      ) : (
        shown
          .map(({ l, idx, i, text }) => {
            const level = l.stream === "system" ? undefined : detectLevel(l.text)
            const color =
              l.stream === "system"
                ? theme.accent2
                : fold && l.traceOf !== undefined
                  ? theme.muted
                  : l.trace
                    ? theme.red
                    : level === "error"
                  ? theme.red
                  : level === "warn"
                    ? theme.yellow
                    : level === "debug"
                      ? theme.muted
                      : l.stream === "stderr"
                        ? theme.orange
                        : theme.text
            const bg =
              idx >= selLo && idx <= selHi
                ? idx === cursorIdx
                  ? theme.cursor
                  : theme.selection
                : l.seq === current
                  ? theme.cursor
                  : isMatch?.(l)
                    ? theme.match
                    : undefined
            return (
              <text key={`${l.seq}:${i}`} selectable={false}>
                {i > 0 ? (
                  <span bg={bg}>{" ".repeat(timeW + prefixW)}</span>
                ) : (
                  <>
                    {showTime ? <span fg={theme.dim} bg={bg}>{clock(l.ts)} </span> : null}
                    {service ? null : <span fg={serviceColor(l.service, names)} bg={bg}>{fit(l.service, prefixW - 1)} </span>}
                  </>
                )}
                <span fg={color} bg={bg}>{text}</span>
              </text>
            )
          })
      )}
    </box>
  )
}
