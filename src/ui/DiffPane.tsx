import type { BoxRenderable, MouseEvent } from "@opentui/core"
import { useEffect, type MutableRefObject } from "react"
import type { DiffRows } from "./diffRows.ts"
import { useSize } from "./hooks.ts"
import { fit, mix, theme } from "./theme.ts"

interface Props {
  rows: DiffRows
  /** index of the first visible row */
  scroll: number
  /** the hunk to mark as the one operations act on (-1 for none) */
  current: number
  /** relative scroll request; the pane clamps whatever the owner holds to what fits */
  onScroll: (delta: number) => void
  /** the pane writes how many rows fit here, for page-wise scrolling */
  pageRef: MutableRefObject<number>
  title: string
  status?: string
  focused: boolean
  onFocus?: () => void
  /** shown instead of the diff when there are no rows */
  empty: string
  /** side-by-side, read only, rendered by OpenTUI's own diff component from the raw patch */
  split?: { patch: string }
}

const tabs = (s: string) => s.replace(/\t/g, "    ")

export function DiffPane({ rows, scroll, current, onScroll, pageRef, title, status, focused, onFocus, empty, split }: Props) {
  const { ref, size, onSizeChange } = useSize<BoxRenderable>()
  const height = Math.max(1, size.height - 2)
  const width = Math.max(20, size.width - 4)
  pageRef.current = height
  const maxScroll = Math.max(0, rows.rows.length - height)
  useEffect(() => {
    if (scroll > maxScroll && size.height > 0) onScroll(maxScroll - scroll)
  }, [scroll, maxScroll, size.height, onScroll])

  const top = Math.min(scroll, maxScroll)
  const visible = rows.rows.slice(top, top + height)
  const gutter = 4
  const textW = Math.max(1, width - 2 - gutter * 2 - 1)
  const addBg = mix(theme.panel, theme.green, 0.16)
  const delBg = mix(theme.panel, theme.red, 0.16)

  return (
    <box
      ref={ref}
      onSizeChange={onSizeChange}
      flexGrow={1}
      flexBasis={0}
      flexDirection="column"
      border
      borderStyle="rounded"
      borderColor={focused ? theme.borderFocus : theme.border}
      backgroundColor={theme.panel}
      paddingLeft={1}
      paddingRight={1}
      title={` ${title} `}
      titleColor={theme.text}
      bottomTitle={status ? ` ${status} ` : undefined}
      bottomTitleAlignment="right"
      onMouseDown={onFocus}
      onMouseScroll={(e: MouseEvent) => onScroll(e.scroll?.direction === "up" ? -3 : e.scroll?.direction === "down" ? 3 : 0)}
    >
      {rows.rows.length === 0 ? (
        <text fg={theme.dim}>{empty}</text>
      ) : split ? (
        <scrollbox flexGrow={1}>
          <diff
            diff={split.patch}
            view="split"
            showLineNumbers
            fg={theme.text}
            addedBg={addBg}
            removedBg={delBg}
            contextBg={theme.panel}
            lineNumberFg={theme.dim}
            lineNumberBg={theme.panel}
            addedSignColor={theme.green}
            removedSignColor={theme.red}
          />
        </scrollbox>
      ) : (
        visible.map((r, i) => {
          const mark = r.hunk >= 0 && r.hunk === current ? "▌" : " "
          const key = `${top + i}`
          if (r.kind === "file")
            return (
              <text key={key} selectable={false}>
                <strong fg={theme.accent} bg={theme.panelAlt}>{fit(` ${r.text}`, width)}</strong>
              </text>
            )
          if (r.kind === "note")
            return (
              <text key={key} fg={theme.dim} selectable={false}>
                {`  ${r.text}`}
              </text>
            )
          if (r.kind === "hunk")
            return (
              <text key={key} selectable={false}>
                <span fg={theme.accent}>{mark}</span>
                <span fg={theme.accent2}>{fit(` ${r.text}`, width - 1)}</span>
              </text>
            )
          const bg = r.kind === "add" ? addBg : r.kind === "del" ? delBg : undefined
          const fg = r.kind === "add" ? theme.green : r.kind === "del" ? theme.red : r.kind === "marker" ? theme.dim : theme.text
          const sign = r.kind === "add" ? "+" : r.kind === "del" ? "-" : " "
          const num = (n?: number) => (n === undefined ? "" : String(n)).padStart(gutter)
          return (
            <text key={key} selectable={false}>
              <span fg={theme.accent}>{mark}</span>
              <span fg={theme.dim}>{`${num(r.oldNo)} ${num(r.newNo)} `}</span>
              <span fg={fg} bg={bg}>{fit(r.kind === "marker" ? r.text : `${sign}${tabs(r.text)}`, textW)}</span>
            </text>
          )
        })
      )}
    </box>
  )
}
