import type { BoxRenderable, MouseEvent } from "@opentui/core"
import { detectLevel, type LogLine } from "../core/logs.ts"
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
}

function time(ts: number) {
  const d = new Date(ts)
  return `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}:${String(d.getSeconds()).padStart(2, "0")}`
}

export function filterLines(lines: readonly LogLine[], filter: string): readonly LogLine[] {
  if (!filter) return lines
  let re: RegExp
  try {
    re = new RegExp(filter, "i")
  } catch {
    const f = filter.toLowerCase()
    return lines.filter((l) => l.text.toLowerCase().includes(f) || l.service.includes(f))
  }
  return lines.filter((l) => re.test(l.text) || re.test(l.service))
}

export function LogView({ lines, service, names, filter, scrollBack, onScroll, title, focused, showTime }: Props) {
  const { ref, size, onSizeChange } = useSize<BoxRenderable>()
  const filtered = filterLines(lines, filter)
  const height = Math.max(1, size.height - 2)
  const width = Math.max(10, size.width - 4)
  const maxBack = Math.max(0, filtered.length - height)
  const back = Math.min(scrollBack, maxBack)
  const end = filtered.length - back
  const visible = filtered.slice(Math.max(0, end - height), end)
  const prefixW = service ? 0 : Math.min(14, Math.max(4, ...names.map((n) => n.length))) + 1

  const status = [
    filter ? `/${filter}  ${filtered.length} matches` : `${filtered.length} lines`,
    back ? `↑ ${back}  (f to follow)` : "following",
  ].join(" · ")

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
      onMouseScroll={(e: MouseEvent) => onScroll(e.scroll?.direction === "up" ? 3 : e.scroll?.direction === "down" ? -3 : 0)}
    >
      {visible.length === 0 ? (
        <text fg={theme.dim}>{filter ? "no lines match the filter" : "no output yet"}</text>
      ) : (
        visible.map((l) => {
          const level = l.stream === "system" ? undefined : detectLevel(l.text)
          const color =
            l.stream === "system"
              ? theme.accent2
              : level === "error"
                ? theme.red
                : level === "warn"
                  ? theme.yellow
                  : level === "debug"
                    ? theme.muted
                    : l.stream === "stderr"
                      ? theme.orange
                      : theme.text
          const timeW = showTime ? 9 : 0
          const textW = Math.max(1, width - timeW - prefixW)
          return (
            <text key={l.seq}>
              {showTime ? <span fg={theme.dim}>{time(l.ts)} </span> : null}
              {service ? null : <span fg={serviceColor(l.service, names)}>{fit(l.service, prefixW - 1)} </span>}
              <span fg={color}>{fit(l.stream === "system" ? `» ${l.text}` : l.text, textW)}</span>
            </text>
          )
        })
      )}
    </box>
  )
}
