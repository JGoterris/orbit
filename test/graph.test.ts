import { describe, expect, test } from "bun:test"
import { findCycle, levels, topoOrder, transitiveDependents, transitiveDeps } from "../src/core/graph.ts"

const deps = { db: [], cache: [], api: ["db", "cache"], worker: ["db"], web: ["api"] }

describe("graph", () => {
  test("topological order puts dependencies first", () => {
    const order = topoOrder(deps)
    for (const [n, ds] of Object.entries(deps)) for (const d of ds) expect(order.indexOf(d)).toBeLessThan(order.indexOf(n))
  })
  test("levels", () => {
    expect(levels(deps)).toEqual({ db: 0, cache: 0, api: 1, worker: 1, web: 2 })
  })
  test("transitive", () => {
    expect([...transitiveDeps(deps, "web")].sort()).toEqual(["api", "cache", "db"])
    expect([...transitiveDependents(deps, "db")].sort()).toEqual(["api", "web", "worker"])
  })
  test("cycles", () => {
    expect(findCycle(deps)).toBeUndefined()
    expect(findCycle({ a: ["b"], b: ["c"], c: ["a"] })).toEqual(["a", "b", "c", "a"])
  })
})
