import { useRenderer } from "@opentui/react"
import { join } from "node:path"
import { useEffect, useMemo, useReducer, useRef, useState } from "react"
import { commitDiff, fileDiff, parseDiff, rangeDiff, stashDiff, type DiffSide } from "../../core/git/diff.ts"
import { cmdLog, formatArgs } from "../../core/git/cmdlog.ts"
import { branchSpread, runOnRepos, summarizeResults } from "../../core/git/multi.ts"
import * as ops from "../../core/git/ops.ts"
import { buildTree } from "../../core/git/tree.ts"
import type { RepoEntry } from "../../core/git/repos.ts"
import { hasStaged, hasUnstaged, type FileChange } from "../../core/git/status.ts"
import { clipboard } from "../clipboard.ts"
import { DiffPane } from "../DiffPane.tsx"
import { buildRows, currentHunk, revealHunk } from "../diffRows.ts"
import { ListPanel, type ListRow } from "../ListPanel.tsx"
import { PromptOverlay, YesNoOverlay } from "../Overlays.tsx"
import { fit, theme } from "../theme.ts"
import type { Pane, ViewContext } from "./types.ts"

const LISTS = ["changes", "branches", "commits", "stash"] as const
type ListPane = (typeof LISTS)[number]
const isList = (p: Pane): p is ListPane => (LISTS as readonly string[]).includes(p)

/** What a repo's last action ended in, kept in the Repos table until the next one. */
interface Outcome {
  ok: boolean
  text: string
}

interface Prompt {
  kind: "commit" | "branch" | "multiBranch" | "multiSwitch"
  title: string
  hint: string
  value: string
}

interface Confirm {
  title: string
  message: string
  run: () => void
}

export function GitView({ ctx }: { ctx: ViewContext }) {
  // leaving the view must not leave its key handler (or a capture) behind in the shell
  useEffect(
    () => () => {
      ctx.keys.current = undefined
      ctx.capture.current = false
      ctx.setHints(undefined)
    },
    [], // eslint-disable-line react-hooks/exhaustive-deps
  )
  if (!ctx.repos.length) {
    return (
      <box flexGrow={1} border borderStyle="rounded" borderColor={theme.border} backgroundColor={theme.panel} padding={1}>
        <text fg={theme.muted}>None of this project's services are in a git repository.</text>
      </box>
    )
  }
  return <GitPanels ctx={ctx} />
}

function GitPanels({ ctx }: { ctx: ViewContext }) {
  const { focus, setFocus, notify, repos } = ctx
  const renderer = useRenderer()
  const index = Math.min(ctx.repoIndex, repos.length - 1)
  const entry = repos[index]!
  const repo = entry.repo
  const root = repo.root
  const [, rerender] = useReducer((n: number) => n + 1, 0)
  const [diffVersion, bumpDiff] = useReducer((n: number) => n + 1, 0)
  const [sel, setSel] = useState<Record<ListPane, number>>({ changes: 0, branches: 0, commits: 0, stash: 0 })
  /** selected row of the Commands panel; undefined follows the newest */
  const [logSel, setLogSel] = useState<number | undefined>()
  /** folded folders of the Changes tree, by path */
  const [folded, setFolded] = useState<ReadonlySet<string>>(new Set())
  const [marked, setMarked] = useState<Set<string>>(new Set())
  const [results, setResults] = useState<Record<string, Outcome>>({})
  const [multi, setMulti] = useState(false)
  const [sideChoice, setSideChoice] = useState<{ key: string; side: DiffSide } | undefined>()
  const [split, setSplit] = useState(false)
  const [scroll, setScroll] = useState(0)
  const [hunkIdx, setHunkIdx] = useState(0)
  const [prompt, setPrompt] = useState<Prompt | undefined>()
  const [confirm, setConfirm] = useState<Confirm | undefined>()
  const [diff, setDiff] = useState({ key: "", patch: "" })
  const pageRef = useRef(20)
  const busy = useRef(false)
  const lastList = useRef<ListPane>("changes")

  // another repo: its lists start from the top
  useEffect(() => {
    setSel({ changes: 0, branches: 0, commits: 0, stash: 0 })
    setSideChoice(undefined)
  }, [root])

  // live data: every repo is read in full (the table shows last commit and stashes), the selected one more often
  useEffect(() => {
    const all = () => repos.forEach((e) => void e.repo.refresh("all"))
    for (const e of repos) e.repo.on("change", rerender)
    cmdLog.on("change", rerender)
    all()
    const slow = setInterval(all, 10_000)
    return () => {
      for (const e of repos) e.repo.off("change", rerender)
      cmdLog.off("change", rerender)
      clearInterval(slow)
    }
  }, [repos])
  useEffect(() => {
    const timer = setInterval(() => {
      void repo.refresh("all")
      bumpDiff()
    }, 3000)
    return () => clearInterval(timer)
  }, [repo])

  const status = repo.status
  const files = status?.files ?? []
  const tree = useMemo(() => buildTree(files, folded), [files, folded])
  const lists = { changes: tree, branches: repo.branches, commits: repo.commits, stash: repo.stashes }
  const at = (pane: ListPane) => Math.max(0, Math.min(sel[pane], lists[pane].length - 1))
  if (isList(focus)) lastList.current = focus
  const source = lastList.current

  const treeRow = tree[at("changes")]
  const file: FileChange | undefined = treeRow?.kind === "file" ? treeRow.file : undefined
  const dir = treeRow?.kind === "dir" ? treeRow : undefined
  const side: DiffSide = !file || file.kind === "untracked" ? "unstaged" : sideChoice ? sideChoice.side : hasUnstaged(file) ? "unstaged" : "staged"
  const branch = repo.branches[at("branches")]
  const commit = repo.commits[at("commits")]
  const stash = repo.stashes[at("stash")]

  const target = useMemo((): { key: string; title: string; load: () => Promise<string> } | undefined => {
    if (source === "changes" && file) return { key: `c:${file.path}:${side}`, title: `${file.path} · ${side}`, load: () => fileDiff(root, file, side) }
    if (source === "branches" && branch) {
      if (branch.current) return { key: `b:${branch.name}`, title: `${branch.name} (current)`, load: async () => "" }
      return { key: `b:${branch.name}`, title: `HEAD...${branch.name}`, load: () => rangeDiff(root, `HEAD...${branch.name}`) }
    }
    if (source === "commits" && commit) return { key: `m:${commit.hash}`, title: `${commit.hash} ${commit.subject}`, load: () => commitDiff(root, commit.hash) }
    if (source === "stash" && stash) return { key: `s:${stash.ref}`, title: `${stash.ref} ${stash.message}`, load: () => stashDiff(root, stash.hash) }
  }, [source, file?.path, file?.x, file?.y, side, branch?.name, branch?.current, commit?.hash, stash?.ref, stash?.hash, root]) // eslint-disable-line react-hooks/exhaustive-deps

  // load the diff of whatever is selected; a slow answer for a previous selection is dropped
  const token = useRef(0)
  useEffect(() => {
    const mine = ++token.current
    if (!target) return setDiff({ key: "", patch: "" })
    void target.load().then((patch) => {
      if (mine === token.current) setDiff((d) => (d.key === target.key && d.patch === patch ? d : { key: target.key, patch }))
    })
  }, [target, diffVersion])
  const shownKey = useRef("")
  useEffect(() => {
    if (shownKey.current !== diff.key) {
      setScroll(0)
      setHunkIdx(0)
    }
    shownKey.current = diff.key
  }, [diff.key])

  const parsed = useMemo(() => parseDiff(diff.patch), [diff.patch])
  const rows = useMemo(() => buildRows(parsed), [parsed])
  const page = pageRef.current
  const top = Math.min(scroll, Math.max(0, rows.rows.length - page))
  const curHunk = currentHunk(rows, hunkIdx, top, page)

  const roots = new Set(repos.map((e) => e.repo.root))
  const cmdEntries = cmdLog.entries.filter((e) => roots.has(e.root))
  const logAt = logSel === undefined ? cmdEntries.length - 1 : Math.max(0, Math.min(logSel, cmdEntries.length - 1))
  const cmdEntry = cmdEntries[logAt]

  // ---------------------------------------------------------------- actions

  /** The repos a multi-repo action touches: the marked ones, or all of them when none is marked. */
  const targets = (): RepoEntry[] => (marked.size ? repos.filter((e) => marked.has(e.repo.root)) : repos)

  const remember = (rs: { entry: RepoEntry; res: ops.OpResult }[], ok: string) =>
    setResults((prev) => {
      const next = { ...prev }
      for (const { entry: e, res } of rs) next[e.repo.root] = { ok: res.ok, text: res.ok ? ok : res.message }
      return next
    })

  /** Runs `op` on the given repos at once, one batch at a time; the table shows each outcome, a toast the summary. */
  const runBatch = async (verb: string, doneText: string, on: RepoEntry[], op: (e: RepoEntry) => Promise<ops.OpResult>) => {
    if (busy.current) return notify("another git operation is still running", theme.yellow)
    busy.current = true
    try {
      const rs = await runOnRepos(on, op)
      remember(rs, doneText)
      const sum = summarizeResults(verb, rs)
      notify(sum.ok ? sum.message : `${sum.message}`, sum.ok ? theme.green : theme.red)
    } finally {
      busy.current = false
      bumpDiff()
    }
  }

  /** A single-repo git operation on the selected repo (staging, commit, new branch…), reported with git's own message. */
  const act = async (label: string, run: () => Promise<ops.OpResult>, ok?: string) => {
    if (busy.current) return notify("another git operation is still running", theme.yellow)
    busy.current = true
    try {
      const res = await repo.run(run)
      res.ok ? notify(ok ?? label, theme.green) : notify(`${label} failed: ${res.message}`, theme.red)
    } finally {
      busy.current = false
      bumpDiff()
    }
  }

  const pushOp = (e: RepoEntry): Promise<ops.OpResult> => {
    const st = e.repo.status
    if (!st?.branch) return Promise.resolve({ ok: false, message: "detached HEAD: no branch to push" })
    return ops.push(e.repo.root, st.branch, !!st.upstream)
  }

  const NET = {
    f: { verb: "fetched", op: (e: RepoEntry) => ops.fetch(e.repo.root) },
    p: { verb: "pulled", op: (e: RepoEntry) => ops.pull(e.repo.root) },
    u: { verb: "pushed", op: pushOp },
  } as const

  const names = (on: RepoEntry[]) => on.map((e) => e.name).join(", ")

  /** f/p/u on one repo (the selected one) or, in multi-repo mode, on every target (pull and push ask first). */
  const network = (k: keyof typeof NET, many: boolean) => {
    const { verb, op } = NET[k]
    const on = many ? targets() : [entry]
    const go = () => void runBatch(verb, verb, on, op)
    if (!many || k === "f") return go()
    setConfirm({ title: `${k === "p" ? "Pull" : "Push"} ${on.length} repos`, message: names(on), run: go })
  }

  /** `ask`: the multi-repo flow always confirms (even with one repo marked), the plain `n` in Branches does not. */
  const newBranch = (name: string, on: RepoEntry[], ask: boolean) => {
    const go = () => void runBatch("new branch", `on ${name}`, on, (e) => ops.createBranch(e.repo.root, name))
    if (!ask) return go()
    setConfirm({ title: `New branch ${name} in ${on.length} repos`, message: names(on), run: go })
  }

  const switchBranch = (name: string, on: RepoEntry[]) => {
    const go = () => void runBatch("switched", `on ${name}`, on, (e) => ops.checkout(e.repo.root, name))
    setConfirm({ title: `Switch ${on.length} repos to ${name}`, message: names(on), run: go })
  }

  const move = (pane: ListPane, delta: number | "first" | "last") => {
    const n = lists[pane].length
    if (!n) return
    const next = delta === "first" ? 0 : delta === "last" ? n - 1 : Math.max(0, Math.min(n - 1, at(pane) + delta))
    setSel((s) => ({ ...s, [pane]: next }))
  }

  const toggleMark = (r: RepoEntry) =>
    setMarked((m) => {
      const next = new Set(m)
      next.has(r.repo.root) ? next.delete(r.repo.root) : next.add(r.repo.root)
      return next
    })

  const copyPath = (path: string, absolute: boolean) => copyText(absolute ? join(root, path) : path)
  const copyText = (text: string) => {
    void clipboard.copy(renderer, text).then((r) => (r.ok ? notify(`copied ${text} via ${r.via}`, theme.green) : notify(r.error ?? "copy failed", theme.red)))
  }

  const toggleFile = (f: FileChange) => {
    const stageIt = hasUnstaged(f)
    void act(stageIt ? `stage ${f.path}` : `unstage ${f.path}`, () => (stageIt ? ops.stage(root, [f.path]) : ops.unstage(root, [f.path], status?.hasCommits ?? true)), `${stageIt ? "staged" : "unstaged"} ${f.path}`)
  }

  /** space on a folder: stage everything under it, or unstage it when it is all staged */
  const toggleDir = (d: NonNullable<typeof dir>) => {
    const stageIt = d.files.some(hasUnstaged)
    const paths = d.files.map((f) => f.path)
    void act(`${stageIt ? "stage" : "unstage"} ${d.path}/`, () => (stageIt ? ops.stage(root, paths) : ops.unstage(root, paths, status?.hasCommits ?? true)), `${stageIt ? "staged" : "unstaged"} ${d.path}/`)
  }

  const foldDir = (path: string) =>
    setFolded((f) => {
      const next = new Set(f)
      next.has(path) ? next.delete(path) : next.add(path)
      return next
    })

  /** `{` `}`: the previous / next changed file, skipping folders */
  const stepFile = (delta: 1 | -1) => {
    for (let i = at("changes") + delta; i >= 0 && i < tree.length; i += delta) {
      if (tree[i]!.kind === "file") return setSel((s) => ({ ...s, changes: i }))
    }
  }

  const toggleAll = () => {
    const anyUnstaged = files.some(hasUnstaged)
    void act(anyUnstaged ? "stage all" : "unstage all", () => (anyUnstaged ? ops.stageAll(root) : ops.unstageAll(root, status?.hasCommits ?? true)), anyUnstaged ? "staged everything" : "unstaged everything")
  }

  const openCommit = () => {
    if (!files.some(hasStaged)) return notify("nothing is staged (space stages the selected file, a stages all)", theme.yellow)
    setPrompt({ kind: "commit", title: `Commit · ${entry.name}`, hint: "enter commit · esc cancel  (amend and the rest: lazygit, L)", value: "" })
  }

  const submit = (p: Prompt) => {
    const value = p.value.trim()
    setPrompt(undefined)
    if (!value) return p.kind === "commit" ? notify("empty commit message", theme.yellow) : undefined
    if (p.kind === "commit") return void act("commit", () => ops.commit(root, value), `committed: ${value}`)
    if (p.kind === "branch") return newBranch(value, [entry], false)
    if (p.kind === "multiBranch") return newBranch(value, targets(), true)
    return switchBranch(value, targets())
  }

  const openMultiPrompt = (kind: "multiBranch" | "multiSwitch") => {
    const on = targets()
    setPrompt({
      kind,
      title: kind === "multiBranch" ? `New branch in ${on.length} repo${on.length > 1 ? "s" : ""}` : `Switch ${on.length} repo${on.length > 1 ? "s" : ""} to branch`,
      hint: "enter continue · esc cancel",
      value: "",
    })
  }

  // ---------------------------------------------------------------- keys

  ctx.capture.current = !!(prompt || confirm)
  ctx.keys.current = (key) => {
    const ch = key.sequence
    const enter = key.name === "return"
    if (confirm) {
      if (ch === "y" || ch === "Y" || enter) {
        setConfirm(undefined)
        confirm.run()
      } else if (ch === "n" || key.name === "escape") setConfirm(undefined)
      return true
    }
    if (prompt) {
      if (key.name === "escape") setPrompt(undefined)
      else if (enter) submit(prompt)
      return true
    }
    // multi-repo mode: the next key says what to do on every target
    if (multi) {
      setMulti(false)
      if (ch === "f" || ch === "p" || ch === "u") network(ch, true)
      else if (ch === "b") openMultiPrompt("multiBranch")
      else if (ch === "s") openMultiPrompt("multiSwitch")
      else if (key.name !== "escape" && ch !== "m") notify("multi-repo: f fetch · p pull · u push · b new branch · s switch branch", theme.yellow)
      return true
    }
    const down = key.name === "down" || ch === "j"
    const up = key.name === "up" || ch === "k"

    // keys that work in every pane
    if (ch === "m") return setMulti(true), true
    if (ch === "f" || ch === "p" || ch === "u") return network(ch, false), true
    if (ch === "L") return ctx.lazygit(root), true

    if (focus === "gitdiff") {
      if (key.name === "escape") return setFocus(lastList.current), true
      if (down) return setScroll((v) => v + 1), true
      if (up) return setScroll((v) => Math.max(0, v - 1)), true
      if (key.ctrl && key.name === "d") return setScroll((v) => v + Math.floor(page / 2)), true
      if (key.ctrl && key.name === "u") return setScroll((v) => Math.max(0, v - Math.floor(page / 2))), true
      if (key.name === "pagedown") return setScroll((v) => v + page), true
      if (key.name === "pageup") return setScroll((v) => Math.max(0, v - page)), true
      if (ch === "g" || key.name === "home") return setScroll(0), true
      if (ch === "G" || key.name === "end") return setScroll(Number.MAX_SAFE_INTEGER), true
      if (ch === "]" || ch === "[") {
        if (!rows.hunks.length) return true
        const next = Math.max(0, Math.min(rows.hunks.length - 1, curHunk + (ch === "]" ? 1 : -1)))
        setHunkIdx(next)
        setScroll(revealHunk(rows, next, top, page))
        return true
      }
      if (ch === "s") return setSplit((v) => !v), true
      if (ch === "}" && source === "changes") return stepFile(1), true
      if (ch === "{" && source === "changes") return stepFile(-1), true
      if ((ch === "y" || ch === "Y") && source === "changes" && file) return copyPath(file.path, ch === "Y"), true
      return false
    }
    if (focus === "repos") {
      const n = repos.length
      if (down) return ctx.setRepoIndex(Math.min(n - 1, index + 1)), true
      if (up) return ctx.setRepoIndex(Math.max(0, index - 1)), true
      if (ch === "g" || key.name === "home") return ctx.setRepoIndex(0), true
      if (ch === "G" || key.name === "end") return ctx.setRepoIndex(n - 1), true
      if (ch === " ") return toggleMark(entry), true
      if (ch === "a") return setMarked((m) => (m.size === n ? new Set() : new Set(repos.map((e) => e.repo.root)))), true
      if (enter) return setFocus("changes"), true
      return false
    }
    if (focus === "gitlog") {
      const n = cmdEntries.length
      if (down) return setLogSel(logAt + 1 >= n - 1 ? undefined : logAt + 1), true
      if (up) return setLogSel(Math.max(0, logAt - 1)), true
      if (ch === "g" || key.name === "home") return setLogSel(0), true
      if (ch === "G" || key.name === "end") return setLogSel(undefined), true
      if (ch === "c") return cmdLog.clear(), setLogSel(undefined), true
      if ((ch === "y" || ch === "Y") && cmdEntry) return copyText(`git ${formatArgs(cmdEntry.args)}`), true
      return false
    }
    if (!isList(focus)) return false

    if (down) return move(focus, 1), true
    if (up) return move(focus, -1), true
    if (ch === "g" || key.name === "home") return move(focus, "first"), true
    if (ch === "G" || key.name === "end") return move(focus, "last"), true

    if (focus === "changes") {
      if (ch === " ") return file ? toggleFile(file) : dir ? toggleDir(dir) : undefined, true
      if (ch === "a") return toggleAll(), true
      if (ch === "c") return openCommit(), true
      if ((ch === "y" || ch === "Y") && (file || dir)) return copyPath(file?.path ?? dir!.path, ch === "Y"), true
      if (ch === "v" && file && file.kind === "tracked" && hasStaged(file) && hasUnstaged(file)) return setSideChoice({ key: "", side: side === "staged" ? "unstaged" : "staged" }), true
      if (enter) return dir ? foldDir(dir.path) : file ? setFocus("gitdiff") : undefined, true
    }
    if (focus === "branches") {
      if (enter && branch) return branch.current ? notify(`already on ${branch.name}`, theme.yellow) : void act(`switch to ${branch.name}`, () => ops.checkout(root, branch.name), `on ${branch.name}`), true
      if (ch === "n") return setPrompt({ kind: "branch", title: `New branch · ${entry.name}`, hint: "enter create and switch · esc cancel", value: "" }), true
    }
    if ((focus === "commits" || focus === "stash") && enter) return setFocus("gitdiff"), true
    return false
  }

  // ---------------------------------------------------------------- render

  const leftW = Math.min(48, Math.max(34, Math.floor(ctx.width * 0.32)))
  const inner = leftW - 4
  const staged = files.filter(hasStaged).length
  const unstaged = files.filter(hasUnstaged).length
  const colorOf = (c: string) => (c === "?" ? theme.muted : c === "U" || c === "D" ? theme.red : theme.orange)

  const spread = branchSpread(repos)
  const common = spread[0]?.name
  const tableInner = ctx.width - 4
  const nameW = Math.min(18, Math.max(6, ...repos.map((e) => e.name.length)))
  const branchW = Math.min(22, Math.max(6, ...repos.map((e) => (e.repo.status?.branch ?? "detached").length)))
  const SYNC_W = 13
  const DIRTY_W = 8
  const rest = tableInner - 4 - nameW - 1 - branchW - 1 - SYNC_W - DIRTY_W
  const resultW = rest > 70 ? 26 : 0
  const servicesW = rest - resultW > 56 ? 18 : 0
  const lastW = Math.max(0, rest - resultW - servicesW)
  const ago = (s: string) => s.replace(" ago", "").replace(/ (second|minute|hour|day|week|month|year)s?/, (_m, u: string) => (u === "month" ? "mo" : u[0]))

  const repoRows: ListRow[] = repos.map((e) => {
    const st = e.repo.status
    const last = e.repo.commits[0]
    const out = results[e.repo.root]
    const sync = !st ? "" : !st.upstream ? (st.branch ? "no upstream" : "") : [st.ahead ? `↑${st.ahead}` : "", st.behind ? `↓${st.behind}` : ""].filter(Boolean).join(" ") || "="
    const dirty = !st ? "" : [st.files.length ? `✎${st.files.length}` : "", e.repo.stashes.length ? `⚑${e.repo.stashes.length}` : ""].filter(Boolean).join(" ")
    const branchName = st ? (st.branch ?? `detached ${st.oid ?? ""}`) : "…"
    const odd = !!st?.branch && !!common && st.branch !== common
    return {
      key: e.repo.root,
      node: (
        <text>
          <span fg={theme.accent}>{marked.has(e.repo.root) ? "✓ " : "  "}</span>
          <span fg={!st ? theme.dim : st.files.length ? theme.orange : theme.green}>{"● "}</span>
          <span fg={theme.text}>{fit(e.name, nameW)}</span>
          <span fg={odd ? theme.yellow : theme.accent2}>{` ${fit(branchName, branchW)}`}</span>
          <span fg={st?.behind ? theme.yellow : theme.muted}>{` ${fit(sync, SYNC_W - 1)}`}</span>
          <span fg={theme.orange}>{fit(dirty, DIRTY_W)}</span>
          {lastW > 8 ? (
            <>
              <span fg={theme.yellow}>{last ? `${last.hash} ` : ""}</span>
              <span fg={theme.text}>{fit(last ? `${last.subject}` : "", Math.max(1, lastW - 17))}</span>
              <span fg={theme.dim}>{fit(last ? ` ${ago(last.when)}` : "", 9)}</span>
            </>
          ) : null}
          {servicesW ? <span fg={theme.muted}>{fit(e.services.join(", "), servicesW)}</span> : null}
          {resultW ? <span fg={out?.ok ? theme.green : theme.red}>{fit(out ? `${out.ok ? "✓" : "✗"} ${out.text}` : "", resultW)}</span> : null}
        </text>
      ),
    }
  })

  const changeRows: ListRow[] = tree.map((r) => {
    const pad = "  ".repeat(r.depth)
    if (r.kind === "dir") {
      const label = `${r.collapsed ? "▸" : "▾"} ${r.name}/`
      const count = ` ${r.files.length}`
      return {
        key: `d:${r.path}`,
        node: (
          <text>
            <span fg={theme.dim}>{"   "}</span>
            <span fg={theme.accent2}>{fit(`${pad}${label}`, Math.max(1, inner - 4 - count.length))}</span>
            <span fg={theme.dim}>{count}</span>
          </text>
        ),
      }
    }
    const f = r.file
    return {
      key: `${f.path}:${f.x}${f.y}`,
      node: (
        <text>
          <span fg={f.x === "." ? theme.dim : theme.green}>{f.x === "." ? " " : f.x}</span>
          <span fg={colorOf(f.y)}>{f.y === "." ? " " : f.y}</span>
          <span fg={f.kind === "conflict" ? theme.red : theme.text}>{` ${fit(`${pad}${r.name}`, inner - 4)}`}</span>
        </text>
      ),
    }
  })
  const branchRows: ListRow[] = repo.branches.map((b) => ({
    key: b.name,
    node: (
      <text>
        <span fg={b.current ? theme.green : theme.dim}>{b.current ? "* " : "  "}</span>
        <span fg={b.current ? theme.text : theme.muted}>{fit(b.name, inner - 2 - 20)}</span>
        <span fg={theme.yellow}>{fit(b.track, 9)}</span>
        <span fg={theme.dim}>{fit(` ${ago(b.when)}`, 11)}</span>
      </text>
    ),
  }))
  const graphW = Math.max(20, (ctx.zoomed ? ctx.width : leftW) - 4)
  /** author and age only when the panel is wide enough (zoomed, or a wide left column) */
  const metaW = graphW >= 70 ? 24 : 0
  const commitRows: ListRow[] = repo.commits.map((c, i) => {
    const refs = c.refs ? `(${c.refs}) ` : ""
    const used = c.graph.length + c.hash.length + 1 + refs.length
    return {
      key: `${i}:${c.hash}`,
      node: (
        <text>
          <span fg={theme.accent2}>{c.graph}</span>
          <span fg={theme.yellow}>{c.hash} </span>
          <span fg={theme.green}>{refs}</span>
          <span fg={theme.text}>{fit(c.subject, Math.max(1, graphW - used - metaW))}</span>
          {metaW ? <span fg={theme.dim}>{fit(` ${c.author} · ${ago(c.when)}`, metaW)}</span> : null}
        </text>
      ),
    }
  })
  const stashRows: ListRow[] = repo.stashes.map((s) => ({
    key: s.ref,
    node: (
      <text>
        <span fg={theme.accent2}>{s.ref} </span>
        <span fg={theme.text}>{fit(s.message, Math.max(1, inner - s.ref.length - 1))}</span>
      </text>
    ),
  }))

  const clock = (d: Date) => d.toTimeString().slice(0, 8)
  const repoName = (root: string) => repos.find((e) => e.repo.root === root)?.name ?? ""
  const cmdNameW = Math.min(18, Math.max(4, ...repos.map((e) => e.name.length)))
  const cmdRows: ListRow[] = cmdEntries.map((c, i) => ({
    key: `${i}:${c.at.getTime()}`,
    node: (
      <text>
        <span fg={theme.dim}>{`${clock(c.at)} `}</span>
        <span fg={c.ok ? theme.green : theme.red}>{c.ok ? "✓ " : "✗ "}</span>
        <span fg={theme.accent2}>{fit(repoName(c.root), cmdNameW)}</span>
        <span fg={theme.text}>{` git ${formatArgs(c.args)}`}</span>
        <span fg={theme.dim}>{`  ${c.ms}ms`}</span>
        {!c.ok && c.message ? <span fg={theme.red}>{`  ${c.message}`}</span> : null}
      </text>
    ),
  }))

  const sync = status?.upstream ? `${status.ahead ? ` ↑${status.ahead}` : ""}${status.behind ? ` ↓${status.behind}` : ""}` : status?.branch ? " (no upstream)" : ""
  const panel = (pane: ListPane) => ({ focused: focus === pane, onFocus: () => setFocus(pane), selected: at(pane), onSelect: (i: number) => setSel((s) => ({ ...s, [pane]: i })) })
  const fileRows = tree.flatMap((r, i) => (r.kind === "file" ? [i] : []))
  const fileCount = source === "changes" ? fileRows.length : 0
  const diffStatus = [fileCount > 1 ? `file ${fileRows.indexOf(at("changes")) + 1}/${fileCount}` : "", rows.hunks.length ? `hunk ${curHunk + 1}/${rows.hunks.length}` : "", split ? "split" : ""].filter(Boolean).join(" · ")

  const hints = multi
    ? [["f", "fetch"], ["p", "pull"], ["u", "push"], ["b", "new branch"], ["s", "switch branch"], ["esc", `cancel · on ${marked.size ? `${marked.size} marked` : `all ${repos.length}`}`]]
    : gitHints(focus, { source, canStep: fileCount > 1, marked: marked.size })
  const hintsKey = JSON.stringify(hints)
  useEffect(() => ctx.setHints(hints), [hintsKey]) // eslint-disable-line react-hooks/exhaustive-deps

  const zoomedPane = ctx.zoomed ? focus : undefined
  const show = (pane: Pane) => !zoomedPane || zoomedPane === pane
  const showLeft = !zoomedPane || zoomedPane === "changes" || zoomedPane === "branches" || zoomedPane === "commits" || zoomedPane === "stash"
  const showRight = !zoomedPane || zoomedPane === "gitdiff" || zoomedPane === "gitlog"
  const spreadTitle = spread.map((s) => `${s.name} ×${s.count}`).join(" · ")
  const targetNote = marked.size ? `${marked.size} marked` : "none marked: all"

  return (
    <box flexGrow={1} flexDirection="column">
      {show("repos") && (
        <ListPanel
          title={`Repos · ${repos.length}${spreadTitle ? ` · ${spreadTitle}` : ""}`}
          rows={repoRows}
          empty=""
          footer={`${targetNote} · m multi-repo · L lazygit`}
          height={zoomedPane ? undefined : Math.min(repos.length, 8) + 2}
          focused={focus === "repos"}
          onFocus={() => setFocus("repos")}
          selected={index}
          onSelect={ctx.setRepoIndex}
        />
      )}
      {zoomedPane === "repos" ? null : (
        <box flexGrow={1} flexDirection="row">
          {showLeft ? (
            <box width={ctx.zoomed ? ctx.width : leftW} flexDirection="column">
              {show("changes") && (
                <ListPanel title={`Changes · ${status?.branch ?? (status?.oid ? `detached ${status.oid}` : "…")}${sync}`} rows={changeRows} empty="working tree clean" footer={files.length ? `${staged} staged · ${unstaged} unstaged` : undefined} grow={3} {...panel("changes")} />
              )}
              {show("branches") && <ListPanel title="Branches" rows={branchRows} empty="no branches" grow={2} {...panel("branches")} />}
              {show("commits") && <ListPanel title="Graph" rows={commitRows} empty="no commits yet" grow={3} {...panel("commits")} />}
              {show("stash") && <ListPanel title="Stash" rows={stashRows} empty="no stashes" grow={1} {...panel("stash")} />}
            </box>
          ) : null}
          {showRight ? (
            <box flexGrow={1} flexDirection="column">
              {show("gitdiff") && (
                <DiffPane
                  rows={rows}
                  scroll={scroll}
                  current={curHunk}
                  onScroll={(d) => setScroll((v) => Math.max(0, v + d))}
                  pageRef={pageRef}
                  title={`Diff · ${target?.title ?? ""}`}
                  status={diffStatus}
                  focused={focus === "gitdiff"}
                  onFocus={() => setFocus("gitdiff")}
                  empty={target ? (source === "branches" && branch?.current ? "this is the branch you are on" : "no changes to show") : source === "changes" && dir ? `${dir.path}/ · ${dir.files.length} changed file${dir.files.length > 1 ? "s" : ""} (enter folds, space stages them all)` : "nothing selected"}
                  split={split ? { patch: diff.patch } : undefined}
                  grow={3}
                />
              )}
              {show("gitlog") && (
                <ListPanel
                  title="Commands"
                  rows={cmdRows}
                  empty="no git command run yet (stage, commit, fetch, pull, push, branches…)"
                  footer={cmdEntry?.message || undefined}
                  height={zoomedPane ? undefined : 8}
                  focused={focus === "gitlog"}
                  onFocus={() => setFocus("gitlog")}
                  selected={logAt}
                  onSelect={(i) => setLogSel(i >= cmdEntries.length - 1 ? undefined : i)}
                />
              )}
            </box>
          ) : null}
        </box>
      )}
      {prompt ? <PromptOverlay title={prompt.title} hint={prompt.hint} value={prompt.value} onInput={(v) => setPrompt((p) => (p ? { ...p, value: v } : p))} width={ctx.width} /> : null}
      {confirm ? <YesNoOverlay title={confirm.title} message={confirm.message} width={ctx.width} /> : null}
    </box>
  )
}

/** What changes the footer hints besides the focused panel. */
export interface HintMode {
  /** the diff follows this list */
  source?: "changes" | "branches" | "commits" | "stash"
  /** the diff follows a list of several files, so `{ }` can step through them */
  canStep?: boolean
  /** repos marked for multi-repo actions */
  marked?: number
}

/** Footer hints for the focused panel (kept short: they have to fit in 100 columns or so). */
export function gitHints(focus: Pane, mode: HintMode = {}): string[][] {
  const common = [["?", "help"], ["q", "quit"]]
  const net = ["f/p/u", "fetch/pull/push"]
  const lazy = ["L", "lazygit"]
  if (focus === "repos") return [["j/k", "pick repo"], ["space", "mark"], ["a", "mark all"], ["m", "multi-repo"], ["enter", "changes"], lazy, net, ...common]
  if (focus === "changes") return [["space", "stage"], ["a", "all"], ["c", "commit"], ["y", "copy path"], ["enter", "diff/fold"], net, lazy, ["m", "multi"], ...common]
  if (focus === "branches") return [["enter", "switch"], ["n", "new"], net, lazy, ["m", "multi"], ...common]
  if (focus === "commits" || focus === "stash") return [["enter", "diff"], net, lazy, ["m", "multi"], ...common]
  if (focus === "gitlog") return [["j/k", "move"], ["G", "latest"], ["y", "copy command"], ["c", "clear"], lazy, ["m", "multi"], ...common]
  const files = mode.canStep ? [["{ }", "file"]] : []
  return [["esc", "back"], ["j/k", "scroll"], ["[ ]", "hunk"], ...files, ["s", "split"], ...(mode.source === "changes" ? [["y", "copy path"]] : []), lazy, ...common]
}
