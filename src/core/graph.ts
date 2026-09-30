import { ConfigError, type OrbitConfig } from "../config/schema.ts"

/** Minimal shape needed for graph algorithms: name -> dependencies */
export type DepMap = Record<string, readonly string[]>

export function depMapOf(config: Pick<OrbitConfig, "services">): DepMap {
  return Object.fromEntries(Object.values(config.services).map((s) => [s.name, s.dependsOn]))
}

/** Returns a cycle as a list of names (first === last) or undefined. */
export function findCycle(deps: DepMap): string[] | undefined {
  const state = new Map<string, 1 | 2>() // 1 = visiting, 2 = done
  const stack: string[] = []
  const visit = (n: string): string[] | undefined => {
    if (state.get(n) === 2) return
    if (state.get(n) === 1) return [...stack.slice(stack.indexOf(n)), n]
    state.set(n, 1)
    stack.push(n)
    for (const d of deps[n] ?? []) {
      const cycle = visit(d)
      if (cycle) return cycle
    }
    stack.pop()
    state.set(n, 2)
  }
  for (const n of Object.keys(deps)) {
    const cycle = visit(n)
    if (cycle) return cycle
  }
}

export function validateGraph(config: Pick<OrbitConfig, "services">): void {
  for (const svc of Object.values(config.services)) {
    for (const dep of svc.dependsOn) {
      if (!config.services[dep]) {
        throw new ConfigError(`depends on unknown service "${dep}"`, `services.${svc.name}.depends_on`)
      }
      if (dep === svc.name) throw new ConfigError("a service cannot depend on itself", `services.${svc.name}.depends_on`)
    }
  }
  const cycle = findCycle(depMapOf(config))
  if (cycle) throw new ConfigError(`dependency cycle: ${cycle.join(" → ")}`)
}

/** Dependencies first. Stable: ties keep declaration order. */
export function topoOrder(deps: DepMap, names: readonly string[] = Object.keys(deps)): string[] {
  const out: string[] = []
  const seen = new Set<string>()
  const visit = (n: string) => {
    if (seen.has(n)) return
    seen.add(n)
    for (const d of deps[n] ?? []) visit(d)
    out.push(n)
  }
  names.forEach(visit)
  return out
}

/** level 0 = no dependencies; otherwise 1 + max(level of deps) */
export function levels(deps: DepMap): Record<string, number> {
  const memo: Record<string, number> = {}
  const level = (n: string): number => {
    if (memo[n] !== undefined) return memo[n]!
    const ds = deps[n] ?? []
    return (memo[n] = ds.length === 0 ? 0 : 1 + Math.max(...ds.map(level)))
  }
  Object.keys(deps).forEach(level)
  return memo
}

export function transitiveDeps(deps: DepMap, name: string): Set<string> {
  const out = new Set<string>()
  const walk = (n: string) => {
    for (const d of deps[n] ?? []) {
      if (!out.has(d)) {
        out.add(d)
        walk(d)
      }
    }
  }
  walk(name)
  return out
}

export function dependentsMap(deps: DepMap): Record<string, string[]> {
  const out: Record<string, string[]> = Object.fromEntries(Object.keys(deps).map((n) => [n, []]))
  for (const [n, ds] of Object.entries(deps)) for (const d of ds) out[d]?.push(n)
  return out
}

export function transitiveDependents(deps: DepMap, name: string): Set<string> {
  const rev = dependentsMap(deps)
  const out = new Set<string>()
  const walk = (n: string) => {
    for (const d of rev[n] ?? []) {
      if (!out.has(d)) {
        out.add(d)
        walk(d)
      }
    }
  }
  walk(name)
  return out
}
