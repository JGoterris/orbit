import type { BoxRenderable, MouseEvent } from "@opentui/core"
import type { ReactNode } from "react"
import { useSize } from "./hooks.ts"
import { theme } from "./theme.ts"

export interface ListRow {
  key: string
  /** the row's content; the panel adds the selection bar */
  node: ReactNode
}

interface Props {
  title: string
  rows: ListRow[]
  selected: number
  focused: boolean
  onSelect: (index: number) => void
  onFocus?: () => void
  empty: string
  /** shown at the bottom border */
  footer?: string
  grow?: number
  /** fixed height in rows (borders included) instead of sharing the column */
  height?: number
}

/** A bordered, scrolling list of one-line rows with a selection bar. */
export function ListPanel({ title, rows, selected, focused, onSelect, onFocus, empty, footer, grow = 1, height: fixed }: Props) {
  const { ref, size, onSizeChange } = useSize<BoxRenderable>()
  const height = Math.max(1, size.height - 2)
  const start = Math.max(0, Math.min(selected - Math.floor(height / 2), rows.length - height))
  const visible = rows.slice(start, start + height)
  return (
    <box
      ref={ref}
      onSizeChange={onSizeChange}
      flexGrow={fixed ? 0 : grow}
      flexBasis={fixed ?? 0}
      height={fixed}
      flexDirection="column"
      border
      borderStyle="rounded"
      borderColor={focused ? theme.borderFocus : theme.border}
      backgroundColor={theme.panel}
      title={` ${title} `}
      titleColor={theme.text}
      bottomTitle={footer ? ` ${footer} ` : undefined}
      bottomTitleAlignment="right"
      onMouseDown={onFocus}
      onMouseScroll={(e: MouseEvent) => onSelect(Math.max(0, Math.min(rows.length - 1, selected + (e.scroll?.direction === "down" ? 1 : e.scroll?.direction === "up" ? -1 : 0))))}
    >
      {rows.length === 0 ? <text fg={theme.dim}>{empty}</text> : null}
      {visible.map((r, i) => {
        const idx = start + i
        const isSel = idx === selected
        return (
          <box key={r.key} height={1} flexDirection="row" backgroundColor={isSel && focused ? theme.selection : undefined} onMouseDown={() => onSelect(idx)}>
            <text>
              <span fg={isSel ? theme.accent : theme.panel}>{isSel ? "▌" : " "}</span>
            </text>
            {r.node}
          </box>
        )
      })}
    </box>
  )
}
