import type { Renderable } from "@opentui/core"
import { useCallback, useEffect, useRef, useState } from "react"
import type { Supervisor } from "../core/supervisor.ts"

/** Re-renders when the supervisor or its logs change, throttled to ~20 fps. */
export function useSupervisorVersion(sup: Supervisor): number {
  const [version, setVersion] = useState(0)
  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined
    const bump = () => {
      if (timer) return
      timer = setTimeout(() => {
        timer = undefined
        setVersion((v) => v + 1)
      }, 50)
    }
    sup.on("change", bump)
    const offLogs = sup.logs.onLine(bump)
    return () => {
      sup.off("change", bump)
      offLogs()
      clearTimeout(timer)
    }
  }, [sup])
  return version
}

/** A counter that increments every `ms`, used for spinners and uptime clocks. */
export function useTick(ms: number): number {
  const [tick, setTick] = useState(0)
  useEffect(() => {
    const t = setInterval(() => setTick((v) => v + 1), ms)
    return () => clearInterval(t)
  }, [ms])
  return tick
}

/** Measures a renderable after layout. */
export function useSize<T extends Renderable>(): {
  ref: React.RefObject<T | null>
  size: { width: number; height: number }
  onSizeChange: () => void
} {
  const ref = useRef<T | null>(null)
  const [size, setSize] = useState({ width: 0, height: 0 })
  const onSizeChange = useCallback(() => {
    const r = ref.current
    if (!r) return
    setSize((prev) => (prev.width === r.width && prev.height === r.height ? prev : { width: r.width, height: r.height }))
  }, [])
  useEffect(() => {
    // first measurement happens after the initial layout pass
    const t = setTimeout(onSizeChange, 0)
    return () => clearTimeout(t)
  }, [onSizeChange])
  return { ref, size, onSizeChange }
}
