import type { SupervisorLike } from "../core/supervisor.ts"
import { formatBytes, formatDuration } from "../core/metrics.ts"
import { fit, statusIcon, styleFor, theme, typeBadge } from "./theme.ts"

interface Props {
  sup: SupervisorLike
  names: string[]
  selected: string
  onSelect: (name: string) => void
  tick: number
  width: number
  focused: boolean
  /** icon + name + port only */
  compact?: boolean
  onFocus?: () => void
}

export function ServiceList({ sup, names, selected, onSelect, tick, width, focused, compact, onFocus }: Props) {
  const up = names.filter((n) => sup.isUp(n)).length
  const nameW = Math.max(6, width - (compact ? 14 : 35))
  return (
    <box
      width={width}
      flexDirection="column"
      border
      borderStyle="rounded"
      borderColor={focused ? theme.borderFocus : theme.border}
      backgroundColor={theme.panel}
      title={` Services ${up}/${names.length} `}
      titleColor={theme.text}
      onMouseDown={onFocus}
    >
      {names.map((name) => {
        const st = sup.state(name)
        const svc = sup.service(name)
        const style = styleFor(st.status, svc.oneshot)
        const isSel = name === selected
        const upNow = st.startedAt && (st.status === "healthy" || st.status === "running" || st.status === "unhealthy" || st.status === "starting")
        const cpu = st.cpu.length && upNow ? `${Math.round(st.cpu[st.cpu.length - 1]!)}%` : ""
        const mem = st.mem.length && upNow ? formatBytes(st.mem[st.mem.length - 1]!) : ""
        const uptime = upNow ? formatDuration(Date.now() - st.startedAt!) : st.status === "stopped" ? "" : style.label
        const badge = typeBadge[svc.type]
        return (
          <box
            key={name}
            height={1}
            flexDirection="row"
            backgroundColor={isSel ? theme.selection : undefined}
            onMouseDown={() => onSelect(name)}
          >
            <text>
              <span fg={isSel ? theme.accent : theme.panel}>{isSel ? "▌" : " "}</span>
              <span fg={style.color}>{statusIcon(st.status, tick, svc.oneshot)} </span>
              {isSel ? <strong fg={theme.text}>{fit(name, nameW - 2)}</strong> : <span fg={theme.text}>{fit(name, nameW - 2)}</span>}
              <span fg={st.watch === "pending" ? theme.yellow : st.watch === "active" ? theme.accent : theme.dim}>{st.watch ? " ↻" : "  "}</span>
              {compact ? null : <span fg={badge.color}> {badge.label}</span>}
              <span fg={theme.muted}>{fit(svc.port ? ` :${svc.port}` : "", 7)}</span>
              {compact ? null : (
                <>
                  <span fg={theme.muted}>{cpu.padStart(4)}</span>
                  <span fg={theme.dim}>{mem.padStart(6)}</span>
                  <span fg={st.status === "crashed" || st.status === "failed" ? theme.red : theme.dim}>
                    {" " + fit(uptime, 7)}
                  </span>
                </>
              )}
            </text>
          </box>
        )
      })}
    </box>
  )
}
