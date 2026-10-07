import type { Renderable } from "@opentui/core"
import { useCallback, useEffect, useRef, useState } from "react"
import { RANGE_MS, type Range, type ResourceBucket } from "../core/resources.ts"
import type { SupervisorLike } from "../core/supervisor.ts"

/** Re-renders when the supervisor or its logs change, throttled to ~20 fps. */
export function useSupervisorVersion(sup: SupervisorLike): number {
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

/** The cpu / memory buckets of a service for the 15m / 1h charts, refreshed every 10 s (empty for 2m: the live samples are enough). */
export function useResourceHistory(sup: SupervisorLike, name: string, range: Range): ResourceBucket[] {
  const [buckets, setBuckets] = useState<ResourceBucket[]>([])
  useEffect(() => {
    setBuckets([])
    if (range === "2m") return
    let alive = true
    const load = () =>
      sup
        .history(name, RANGE_MS[range])
        .then((b) => alive && setBuckets(b))
        .catch(() => {})
    void load()
    const t = setInterval(load, 10_000)
    return () => {
      alive = false
      clearInterval(t)
    }
  }, [sup, name, range])
  return buckets
}
