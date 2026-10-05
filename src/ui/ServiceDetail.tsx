import { relative } from "node:path"
import type { Supervisor } from "../core/supervisor.ts"
import { describeHealth } from "../core/health.ts"
import { formatBytes, formatDuration } from "../core/metrics.ts"
import { areaChart, fit, sparkline, styleFor, theme, typeBadge } from "./theme.ts"

interface Props {
  sup: Supervisor
  name: string
  width: number
  focused?: boolean
  /** zoomed: fill the available height instead of the fixed 9 rows */
  expanded?: boolean
  height?: number
  /** inner rows available; anything beyond the 7 base lines is filled with config, charts and recent events */
  rows?: number
  onFocus?: () => void
}

/** the lines every service shows: status, what, fields, needs, spacer, cpu/mem, error */
const BASE_ROWS = 7

interface Item {
  label: string
  value: string
}

/** Greedy-packs `label value` items into lines of at most `width` columns. */
function packItems(items: Item[], width: number): Item[][] {
  const lines: Item[][] = [[]]
  let used = 0
  for (const it of items) {
    const value = fit(it.value, Math.max(4, Math.min(it.value.length, width - it.label.length - 4))).trimEnd()
    const w = it.label.length + value.length + 4
    if (used + w > width && lines[lines.length - 1]!.length) {
      lines.push([])
      used = 0
    }
    lines[lines.length - 1]!.push({ label: it.label, value })
    used += w
  }
  return lines
}

const stats = (vs: readonly number[], fmt: (n: number) => string) =>
  vs.length ? `now ${fmt(vs[vs.length - 1]!)} · avg ${fmt(vs.reduce((a, b) => a + b, 0) / vs.length)} · max ${fmt(Math.max(...vs))}` : "no samples"

function Header({ label, width }: { label: string; width: number }) {
  return (
    <text flexShrink={0}>
      <span fg={theme.border}>{"── "}</span>
      <span fg={theme.muted}>{label}</span>
      <span fg={theme.border}>{` ${"─".repeat(Math.max(0, width - label.length - 4))}`}</span>
    </text>
  )
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

export function ServiceDetail({ sup, name, width, focused, expanded, height, rows, onFocus }: Props) {
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

  // ---- extra blocks, in priority order, as far as the height allows
  const extra = Math.max(0, (rows ?? 0) - BASE_ROWS)
  const items: Item[] = []
  if (svc.health) items.push({ label: "check", value: `every ${formatDuration(svc.health.interval)} · timeout ${formatDuration(svc.health.timeout)}` })
  items.push({ label: "start_timeout", value: formatDuration(svc.startTimeout) }, { label: "stop_timeout", value: formatDuration(svc.stopTimeout) })
  items.push({ label: "autostart", value: svc.autostart ? "yes" : "no" })
  if (svc.oneshot) items.push({ label: "oneshot", value: "yes" })
  for (const [label, hooks] of [["pre_start", svc.hooks?.preStart], ["post_start", svc.hooks?.postStart], ["post_stop", svc.hooks?.postStop]] as const) {
    if (hooks?.length) items.push({ label, value: hooks.map((h) => h.cmd).join(" · ") })
  }
  if (svc.url) items.push({ label: "url", value: svc.url })
  if (svc.ports.length) items.push({ label: "ports", value: svc.ports.join(", ") })
  if (svc.volumes.length) items.push({ label: "volumes", value: svc.volumes.join(", ") })
  if (svc.dockerArgs.length) items.push({ label: "args", value: svc.dockerArgs.join(" ") })
  if (svc.composeProject) items.push({ label: "project", value: svc.composeProject })
  if (st.exitCode !== undefined && st.exitCode !== null) items.push({ label: "exit", value: String(st.exitCode) })
  // each block gets a one-row header so they stay distinguishable (charts are empty while stopped)
  const configLines = extra >= 2 ? packItems(items, inner).slice(0, extra - 1) : []
  const rem = extra - (configLines.length ? configLines.length + 1 : 0)
  const chartExtra = rem >= 3 ? Math.min(rem - 1, Math.max(2, Math.min(7, Math.ceil(rem / 2)))) : 0
  const eventRows = rem - (chartExtra ? chartExtra + 1 : 0) >= 2 ? rem - (chartExtra ? chartExtra + 1 : 0) : 0
  const events = eventRows ? sup.logs.lines(name).filter((l) => l.stream !== "stdout").slice(-(eventRows - 1)) : []

  const chartW = Math.max(10, Math.floor((inner - 12) / 2))
  const cpuChart = chartExtra ? areaChart(st.cpu, chartW, chartExtra, Math.max(100, ...st.cpu)) : []
  const memChart = chartExtra ? areaChart(st.mem, chartW, chartExtra) : []

  return (
    <box
      flexDirection="column"
      border
      borderStyle="rounded"
      borderColor={focused ? theme.borderFocus : theme.border}
      backgroundColor={theme.panel}
      paddingLeft={1}
      paddingRight={1}
      height={expanded ? undefined : (height ?? 9)}
      flexGrow={expanded ? 1 : 0}
      overflow="hidden"
      title={` ${name} `}
      titleColor={theme.text}
      onMouseDown={onFocus}
    >
      <text flexShrink={0}>
        <span fg={style.color}>{style.icon} </span>
        <strong fg={style.color}>{style.label}</strong>
        <span fg={theme.muted}>{uptime ? `  ${uptime}` : ""}</span>
        <span fg={theme.dim}>{"   "}</span>
        <span fg={badge.color}>{svc.type}</span>
        <span fg={theme.dim}>{svc.description ? `  ${svc.description}` : ""}</span>
      </text>
      <text flexShrink={0}>
        <span fg={theme.accent2}>{fit(what, Math.max(10, inner - cwd.length - 6))}</span>
        <span fg={theme.dim}>{"  in "}</span>
        <span fg={theme.muted}>{cwd}</span>
      </text>
      <text flexShrink={0}>
        {svc.port ? <Field label="port" value={`:${svc.port}`} color={theme.cyan} /> : null}
        <Field label="health" value={describeHealth(svc.health) + (st.health ? ` (${st.health})` : "")} />
        {st.pid ? <Field label="pid" value={String(st.pid)} /> : null}
        {st.containerId ? <Field label="container" value={st.containerId.slice(0, 12)} /> : null}
        <Field label="restart" value={svc.restart} />
        {svc.watch ? (
          <Field
            label="watch"
            value={
              st.watch === "paused"
                ? "paused"
                : `${svc.watch.paths.join(", ")} · ${svc.watch.debounce / 1000}s/${svc.watch.cooldown / 1000}s${st.watch === "pending" ? " · changes queued" : ""}`
            }
            color={st.watch === "pending" ? theme.yellow : theme.muted}
          />
        ) : null}
        {svc.envFiles.length ? (
          <Field
            label="env_file"
            value={svc.envFiles.map((f) => relative(sup.config.root, f.path) || f.path).join(", ")}
            color={theme.muted}
          />
        ) : null}
        {st.restarts ? <Field label="restarts" value={String(st.restarts)} color={theme.orange} /> : null}
      </text>
      {configLines.length ? <Header label="config" width={inner} /> : null}
      {configLines.map((line, i) => (
        <text key={`cfg${i}`} flexShrink={0}>
          {line.map((it) => (
            <Field key={it.label} label={it.label} value={it.value} color={theme.muted} />
          ))}
        </text>
      ))}
      <text flexShrink={0}>
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
      <text flexShrink={0}> </text>
      {chartExtra ? (
        <>
          <Header label="usage" width={inner} />
          {cpuChart.map((row, i) => (
            <text key={`ch${i}`} flexShrink={0}>
              <span fg={theme.dim}>{i === 0 ? "cpu " : "    "}</span>
              <span fg={theme.green}>{row}</span>
              <span fg={theme.dim}>{i === 0 ? "    mem " : "        "}</span>
              <span fg={theme.accent}>{memChart[i]}</span>
            </text>
          ))}
          <text flexShrink={0}>
            <span fg={theme.muted}>{`    ${fit(stats(st.cpu, (n) => `${n.toFixed(1)}%`), chartW + 4)}`}</span>
            <span fg={theme.muted}>{`    ${fit(stats(st.mem, formatBytes), chartW)}`}</span>
          </text>
        </>
      ) : (
      <text flexShrink={0}>
        <span fg={theme.dim}>cpu </span>
        <span fg={theme.green}>{sparkline(st.cpu, sparkW, Math.max(100, ...st.cpu))}</span>
        <span fg={theme.text}>{` ${cpuNow.toFixed(1).padStart(5)}%`}</span>
        <span fg={theme.dim}>{"     mem "}</span>
        <span fg={theme.accent}>{sparkline(st.mem, sparkW)}</span>
        <span fg={theme.text}>{` ${formatBytes(memNow).padStart(6)}`}</span>
      </text>
      )}
      {eventRows ? (
        <>
          <Header label="recent" width={inner} />
          {events.length ? (
            events.map((l) => (
              <text key={l.seq} flexShrink={0}>
                <span fg={theme.dim}>{`${new Date(l.ts).toTimeString().slice(0, 8)} `}</span>
                <span fg={l.stream === "stderr" ? theme.red : theme.muted}>{fit(l.text, Math.max(10, inner - 9))}</span>
              </text>
            ))
          ) : (
            <text flexShrink={0}>
              <span fg={theme.dim}>no recent events</span>
            </text>
          )}
        </>
      ) : null}
      <text flexShrink={0}>
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
