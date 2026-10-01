import { relative } from "node:path"
import type { Supervisor } from "../core/supervisor.ts"
import { describeHealth } from "../core/health.ts"
import { formatBytes, formatDuration } from "../core/metrics.ts"
import { fit, sparkline, styleFor, theme, typeBadge } from "./theme.ts"

interface Props {
  sup: Supervisor
  name: string
  width: number
  focused?: boolean
  /** zoomed: fill the available height instead of the fixed 9 rows */
  expanded?: boolean
  onFocus?: () => void
}

function Field({ label, value, color = theme.text }: { label: string; value: string; color?: string }) {
  return (
    <>
      <span fg={theme.dim}>{label} </span>
      <span fg={color}>{value}</span>
      <span fg={theme.dim}>{"   "}</span>
    </>
  )
}

export function ServiceDetail({ sup, name, width, focused, expanded, onFocus }: Props) {
  const svc = sup.service(name)
  const st = sup.state(name)
  const style = styleFor(st.status, svc.oneshot)
  const badge = typeBadge[svc.type]
  const inner = Math.max(20, width - 4)
  const up = st.startedAt && ["starting", "running", "healthy", "unhealthy"].includes(st.status)
  const uptime = up ? formatDuration(Date.now() - st.startedAt!) : st.stoppedAt ? `${formatDuration(Date.now() - st.stoppedAt)} ago` : ""

  const what =
    svc.type === "process"
      ? `$ ${svc.cmd}`
      : svc.type === "docker"
        ? `image ${svc.image}${svc.cmd ? ` · ${svc.cmd}` : ""}`
        : `compose ${relative(sup.config.root, svc.composeFile!) || svc.composeFile} · ${svc.composeService}`
  const cwd = relative(sup.config.root, svc.cwd) || "."
  const deps = svc.dependsOn
  const users = sup.dependents[name] ?? []

  const sparkW = Math.max(10, Math.floor((inner - 36) / 2))
  const cpuNow = st.cpu.length ? st.cpu[st.cpu.length - 1]! : 0
  const memNow = st.mem.length ? st.mem[st.mem.length - 1]! : 0

  return (
    <box
      flexDirection="column"
      border
      borderStyle="rounded"
      borderColor={focused ? theme.borderFocus : theme.border}
      backgroundColor={theme.panel}
      paddingLeft={1}
      paddingRight={1}
      height={expanded ? undefined : 9}
      flexGrow={expanded ? 1 : 0}
      title={` ${name} `}
      titleColor={theme.text}
      onMouseDown={onFocus}
    >
      <text>
        <span fg={style.color}>{style.icon} </span>
        <strong fg={style.color}>{style.label}</strong>
        <span fg={theme.muted}>{uptime ? `  ${uptime}` : ""}</span>
        <span fg={theme.dim}>{"   "}</span>
        <span fg={badge.color}>{svc.type}</span>
        <span fg={theme.dim}>{svc.description ? `  ${svc.description}` : ""}</span>
      </text>
      <text>
        <span fg={theme.accent2}>{fit(what, Math.max(10, inner - cwd.length - 6))}</span>
        <span fg={theme.dim}>{"  in "}</span>
        <span fg={theme.muted}>{cwd}</span>
      </text>
      <text>
        {svc.port ? <Field label="port" value={`:${svc.port}`} color={theme.cyan} /> : null}
        <Field label="health" value={describeHealth(svc.health) + (st.health ? ` (${st.health})` : "")} />
        {st.pid ? <Field label="pid" value={String(st.pid)} /> : null}
        {st.containerId ? <Field label="container" value={st.containerId.slice(0, 12)} /> : null}
        <Field label="restart" value={svc.restart} />
        {svc.envFiles.length ? (
          <Field
            label="env_file"
            value={svc.envFiles.map((f) => relative(sup.config.root, f.path) || f.path).join(", ")}
            color={theme.muted}
          />
        ) : null}
        {st.restarts ? <Field label="restarts" value={String(st.restarts)} color={theme.orange} /> : null}
      </text>
      <text>
        <span fg={theme.dim}>needs </span>
        {deps.length ? (
          deps.map((d, i) => (
            <span key={d} fg={styleFor(sup.state(d).status, sup.service(d).oneshot).color}>
              {styleFor(sup.state(d).status, sup.service(d).oneshot).icon} {d}
              {i < deps.length - 1 ? "  " : ""}
            </span>
          ))
        ) : (
          <span fg={theme.dim}>—</span>
        )}
        <span fg={theme.dim}>{"     used by "}</span>
        {users.length ? (
          users.map((d, i) => (
            <span key={d} fg={styleFor(sup.state(d).status, sup.service(d).oneshot).color}>
              {styleFor(sup.state(d).status, sup.service(d).oneshot).icon} {d}
              {i < users.length - 1 ? "  " : ""}
            </span>
          ))
        ) : (
          <span fg={theme.dim}>—</span>
        )}
      </text>
      <text> </text>
      <text>
        <span fg={theme.dim}>cpu </span>
        <span fg={theme.green}>{sparkline(st.cpu, sparkW, Math.max(100, ...st.cpu))}</span>
        <span fg={theme.text}>{` ${cpuNow.toFixed(1).padStart(5)}%`}</span>
        <span fg={theme.dim}>{"     mem "}</span>
        <span fg={theme.accent}>{sparkline(st.mem, sparkW)}</span>
        <span fg={theme.text}>{` ${formatBytes(memNow).padStart(6)}`}</span>
      </text>
      <text>
        {st.error ? (
          <span fg={theme.red}>{fit(`✖ ${st.error}`, inner)}</span>
        ) : st.waitingOn?.length ? (
          <span fg={theme.yellow}>{`waiting for ${st.waitingOn.filter((d) => !sup.isReady(d)).join(", ") || "dependencies"}…`}</span>
        ) : (
          <span fg={theme.dim}> </span>
        )}
      </text>
    </box>
  )
}
