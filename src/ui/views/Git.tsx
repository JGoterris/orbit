import { useCallback, useEffect, useMemo, useReducer, useRef, useState } from "react"
import { commitDiff, fileDiff, parseDiff, rangeDiff, revFileDiff, revFiles, stashDiff, type DiffSide, type RevFile } from "../../core/git/diff.ts"
import * as ops from "../../core/git/ops.ts"
import type { GitRepo } from "../../core/git/repo.ts"
import type { RepoEntry } from "../../core/git/repos.ts"
import { hasStaged, hasUnstaged, type FileChange } from "../../core/git/status.ts"
import { DiffPane } from "../DiffPane.tsx"
import { buildRows, currentHunk, revealHunk } from "../diffRows.ts"
import { ListPanel, type ListRow } from "../ListPanel.tsx"
import { PromptOverlay, YesNoOverlay } from "../Overlays.tsx"
import { fit, theme } from "../theme.ts"
import type { Pane, ViewContext } from "./types.ts"

const LISTS = ["changes", "branches", "commits", "stash"] as const
type ListPane = (typeof LISTS)[number]
const isList = (p: Pane): p is ListPane => (LISTS as readonly string[]).includes(p)

interface Prompt {
  kind: "commit" | "amend" | "branch" | "stash"
  title: string
  hint: string
  value: string
  /** amend: the subject as it was, to tell whether it was edited */
  original?: string
}

/** Commits and stashes can be opened to browse the files they changed, one diff per file. */
interface RevView {
  pane: "commits" | "stash"
  rev: string
  /** short name for titles: the commit hash or the stash ref */
  label: string
  /** undefined while loading */
  files: RevFile[] | undefined
}

interface Confirm {
  title: string
  message: string
  run: () => void
}

export function GitView({ ctx }: { ctx: ViewContext }) {
  // leaving the view must not leave its key handler (or a capture) behind in the shell. This lives here, not
  // in GitPanels, which remounts when another repo is picked and would wipe the new handler as it unmounts.
  useEffect(
    () => () => {
      ctx.keys.current = undefined
      ctx.capture.current = false
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
  const index = Math.min(ctx.repoIndex, ctx.repos.length - 1)
  // keyed by repo: picking another one starts with fresh selections, diff and prompts
  return <GitPanels key={ctx.repos[index]!.repo.root} ctx={ctx} entry={ctx.repos[index]!} index={index} />
}

function GitPanels({ ctx, entry, index }: { ctx: ViewContext; entry: RepoEntry; index: number }) {
  const repo: GitRepo = entry.repo
  const { focus, setFocus, notify } = ctx
  const root = repo.root
  const [, rerender] = useReducer((n: number) => n + 1, 0)
  const [diffVersion, bumpDiff] = useReducer((n: number) => n + 1, 0)
  const [sel, setSel] = useState<Record<ListPane, number>>({ changes: 0, branches: 0, commits: 0, stash: 0 })
  const [revView, setRevView] = useState<RevView | undefined>()
  const [revSel, setRevSel] = useState(0)
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

  // live data: repaint on change, and keep everything fresh while the view is open
  useEffect(() => {
    repo.on("change", rerender)
    void repo.refresh("all")
    const timer = setInterval(() => {
      void repo.refresh("all")
      bumpDiff()
    }, 3000)
    return () => {
      repo.off("change", rerender)
      clearInterval(timer)
    }
  }, [repo])

  const status = repo.status
  const files = status?.files ?? []
  const lists = { changes: files, branches: repo.branches, commits: repo.commits, stash: repo.stashes }
  const at = (pane: ListPane) => Math.max(0, Math.min(sel[pane], lists[pane].length - 1))
  if (isList(focus)) lastList.current = focus
  const source = lastList.current

  const file: FileChange | undefined = files[at("changes")]
  const fileKey = file ? `${file.path}:${file.x}${file.y}` : ""
  const side: DiffSide = !file || file.kind === "untracked" ? "unstaged" : sideChoice?.key === fileKey ? sideChoice.side : hasUnstaged(file) ? "unstaged" : "staged"
  const branch = repo.branches[at("branches")]
  const commit = repo.commits[at("commits")]
  const stash = repo.stashes[at("stash")]
  const revList = revView?.files ?? []
  const revAt = Math.max(0, Math.min(revSel, revList.length - 1))
  const revFile = revView && source === revView.pane ? revList[revAt] : undefined

  const target = useMemo((): { key: string; title: string; load: () => Promise<string> } | undefined => {
    if (source === "changes" && file) return { key: `c:${file.path}:${side}`, title: `${file.path} · ${side}`, load: () => fileDiff(root, file, side) }
    if (source === "branches" && branch) {
      if (branch.current) return { key: `b:${branch.name}`, title: `${branch.name} (current)`, load: async () => "" }
      return { key: `b:${branch.name}`, title: `HEAD...${branch.name}`, load: () => rangeDiff(root, `HEAD...${branch.name}`) }
    }
    if (revView && revFile && source === revView.pane)
      return { key: `r:${revView.rev}:${revFile.path}`, title: `${revView.label} · ${revFile.path}`, load: () => revFileDiff(root, revView.rev, revFile) }
    if (source === "commits" && commit) return { key: `m:${commit.hash}`, title: `${commit.hash} ${commit.subject}`, load: () => commitDiff(root, commit.hash) }
    if (source === "stash" && stash) return { key: `s:${stash.ref}`, title: `${stash.ref} ${stash.message}`, load: () => stashDiff(root, stash.hash) }
  }, [source, file?.path, file?.x, file?.y, side, branch?.name, branch?.current, commit?.hash, stash?.ref, stash?.hash, revView?.rev, revFile?.path, revFile?.orig, root]) // eslint-disable-line react-hooks/exhaustive-deps

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

  const files_ = useMemo(() => parseDiff(diff.patch), [diff.patch])
  const rows = useMemo(() => buildRows(files_), [files_])
  const page = pageRef.current
  const top = Math.min(scroll, Math.max(0, rows.rows.length - page))
  const curHunk = currentHunk(rows, hunkIdx, top, page)

  // ---------------------------------------------------------------- actions

  const report = (label: string, res: ops.OpResult, ok?: string) =>
    res.ok ? notify(ok ?? label, theme.green) : notify(`${label} failed: ${res.message}`, theme.red)

  /** Runs a git operation, one at a time, and reports its outcome. */
  const act = async (label: string, run: () => Promise<ops.OpResult>, ok?: string) => {
    if (busy.current) return notify("another git operation is still running", theme.yellow)
    busy.current = true
    try {
      report(label, await repo.run(run), ok)
    } finally {
      busy.current = false
      bumpDiff()
    }
  }

  /** `enter` on a commit or stash: browse the files it changed (the diff follows the selected file). */
  const openFiles = (pane: "commits" | "stash", rev: string, label: string) => {
    setRevView({ pane, rev, label, files: undefined })
    setRevSel(0)
    void revFiles(root, rev).then((files) => setRevView((v) => (v && v.rev === rev ? { ...v, files } : v)))
  }

  const moveRev = (delta: number | "first" | "last") => {
    const n = revList.length
    if (!n) return
    setRevSel(delta === "first" ? 0 : delta === "last" ? n - 1 : Math.max(0, Math.min(n - 1, revAt + delta)))
  }

  const move = (pane: ListPane, delta: number | "first" | "last") => {
    const n = lists[pane].length
    if (!n) return
    const next = delta === "first" ? 0 : delta === "last" ? n - 1 : Math.max(0, Math.min(n - 1, at(pane) + delta))
    setSel((s) => ({ ...s, [pane]: next }))
  }

  const toggleFile = (f: FileChange) => {
    const stageIt = hasUnstaged(f)
    void act(stageIt ? `stage ${f.path}` : `unstage ${f.path}`, () => (stageIt ? ops.stage(root, [f.path]) : ops.unstage(root, [f.path], status?.hasCommits ?? true)), `${stageIt ? "staged" : "unstaged"} ${f.path}`)
  }

  const toggleAll = () => {
    const anyUnstaged = files.some(hasUnstaged)
    void act(anyUnstaged ? "stage all" : "unstage all", () => (anyUnstaged ? ops.stageAll(root) : ops.unstageAll(root, status?.hasCommits ?? true)), anyUnstaged ? "staged everything" : "unstaged everything")
  }

  const hunkOp = (mode: "stage" | "unstage" | "discard") => {
    const ref = rows.hunks[curHunk]
    if (source !== "changes" || !file || file.kind === "untracked" || !ref) return notify("no hunk to act on here (hunks work on tracked files in Changes)", theme.yellow)
    if (mode === "stage" && side === "staged") return notify("this hunk is already staged (space unstages it)", theme.yellow)
    const df = files_[ref.file]!
    const run = () => act(`${mode} hunk`, () => ops.applyHunk(root, df, ref.index, mode), `${mode === "stage" ? "staged" : mode === "unstage" ? "unstaged" : "discarded"} hunk in ${file.path}`)
    if (mode === "discard") setConfirm({ title: "Discard hunk", message: `Throw away this hunk of ${file.path}? It cannot be undone.`, run: () => void run() })
    else void run()
  }

  const openCommit = () => {
    if (!files.some(hasStaged)) return notify("nothing is staged (space stages the selected file, a stages all)", theme.yellow)
    setPrompt({ kind: "commit", title: "Commit", hint: "enter commit · esc cancel  (amend: A)", value: "" })
  }

  const openAmend = async () => {
    if (!status?.hasCommits) return notify("no commit to amend yet", theme.yellow)
    const subject = (await ops.lastMessage(root)).split("\n")[0] ?? ""
    setPrompt({ kind: "amend", title: "Amend last commit", hint: "enter amend (staged changes included; unchanged subject keeps the message) · esc cancel", value: subject, original: subject })
  }

  const submit = (p: Prompt) => {
    const value = p.value.trim()
    setPrompt(undefined)
    if (p.kind === "commit") {
      if (!value) return notify("empty commit message", theme.yellow)
      return void act("commit", () => ops.commit(root, value), `committed: ${value}`)
    }
    if (p.kind === "amend") {
      if (!value) return notify("empty commit message", theme.yellow)
      return void act("amend", () => ops.amend(root, value === p.original ? undefined : value), "amended the last commit")
    }
    if (p.kind === "branch") {
      if (!value) return
      return void act("new branch", () => ops.createBranch(root, value), `on new branch ${value}`)
    }
    return void act("stash", () => ops.stashPush(root, value || undefined), "stashed the changes")
  }

  const askDiscard = (f: FileChange) =>
    setConfirm({
      title: "Discard changes",
      message: f.kind === "untracked" ? `Delete the untracked ${f.path}?` : `Throw away the changes in ${f.path}?`,
      run: () => void act(`discard ${f.path}`, () => ops.discard(root, f.path, f.kind === "untracked"), `discarded ${f.path}`),
    })

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
    const down = key.name === "down" || ch === "j"
    const up = key.name === "up" || ch === "k"

    // keys that work in every pane
    if (ch === "f") return void act("fetch", () => ops.fetch(root), "fetched"), true
    if (ch === "F" && ctx.repos.length > 1) {
      const all = ctx.repos
      return void act("fetch all", async () => {
        const results = await Promise.all(all.map(async (e) => ({ name: e.name, res: await ops.fetch(e.repo.root) })))
        const bad = results.filter((r) => !r.res.ok)
        void Promise.all(all.map((e) => e.repo.refresh("all")))
        return bad.length ? { ok: false, message: `${bad.map((b) => b.name).join(", ")}: ${bad[0]!.res.message}` } : { ok: true, message: "" }
      }, `fetched ${all.length} repos`), true
    }
    if (ch === "p") return void act("pull", () => ops.pull(root), "pulled"), true
    if (ch === "u") {
      if (!status?.branch) return notify("detached HEAD: there is no branch to push", theme.yellow), true
      return void act("push", () => ops.push(root, status.branch!, !!status.upstream), "pushed"), true
    }

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
      if (ch === " ") return hunkOp(side === "staged" ? "unstage" : "stage"), true
      if (ch === "d") return hunkOp("discard"), true
      if (ch === "s") return setSplit((v) => !v), true
    }
    if (focus === "gitdiff" || focus === "changes") {
      if (ch === "v" && file && file.kind === "tracked" && hasStaged(file) && hasUnstaged(file))
        return setSideChoice({ key: fileKey, side: side === "staged" ? "unstaged" : "staged" }), true
    }
    if (focus === "repos") {
      const n = ctx.repos.length
      if (down) return ctx.setRepoIndex(Math.min(n - 1, index + 1)), true
      if (up) return ctx.setRepoIndex(Math.max(0, index - 1)), true
      if (ch === "g" || key.name === "home") return ctx.setRepoIndex(0), true
      if (ch === "G" || key.name === "end") return ctx.setRepoIndex(n - 1), true
      if (enter) return setFocus("changes"), true
      return false
    }
    if (!isList(focus)) return false

    // a commit or stash opened to browse its files: the panel is a file list until esc
    if (revView && focus === revView.pane) {
      if (key.name === "escape") return setRevView(undefined), true
      if (down) return moveRev(1), true
      if (up) return moveRev(-1), true
      if (ch === "g" || key.name === "home") return moveRev("first"), true
      if (ch === "G" || key.name === "end") return moveRev("last"), true
      if (enter) return revFile ? setFocus("gitdiff") : undefined, true
      return false
    }

    if (down) return move(focus, 1), true
    if (up) return move(focus, -1), true
    if (ch === "g" || key.name === "home") return move(focus, "first"), true
    if (ch === "G" || key.name === "end") return move(focus, "last"), true

    if (focus === "changes") {
      if (ch === " " && file) return toggleFile(file), true
      if (ch === "a") return toggleAll(), true
      if (ch === "d" && file) return askDiscard(file), true
      if (ch === "c") return openCommit(), true
      if (ch === "A") return void openAmend(), true
      if (ch === "s") return setPrompt({ kind: "stash", title: "Stash changes", hint: "enter stash (message optional, untracked files included) · esc cancel", value: "" }), true
      if (enter) return setFocus("gitdiff"), true
    }
    if (focus === "branches") {
      if (enter && branch) return branch.current ? notify(`already on ${branch.name}`, theme.yellow) : void act(`switch to ${branch.name}`, () => ops.checkout(root, branch.name), `on ${branch.name}`), true
      if (ch === "n") return setPrompt({ kind: "branch", title: "New branch", hint: "enter create and switch · esc cancel", value: "" }), true
      if (ch === "d" && branch) {
        if (branch.current) return notify("cannot delete the branch you are on", theme.yellow), true
        return setConfirm({ title: "Delete branch", message: `Delete branch ${branch.name}? (unmerged work is refused)`, run: () => void act(`delete ${branch.name}`, () => ops.deleteBranch(root, branch.name), `deleted ${branch.name}`) }), true
      }
    }
    if (focus === "commits" && enter && commit) return openFiles("commits", commit.hash, commit.hash), true
    if (focus === "stash" && stash) {
      if (enter) return openFiles("stash", stash.hash, stash.ref), true
      if (ch === " ") return void act("apply stash", () => ops.stashApply(root, stash.ref), `applied ${stash.ref}`), true
      if (ch === "o") return void act("pop stash", () => ops.stashPop(root, stash.ref), `popped ${stash.ref}`), true
      if (ch === "d") return setConfirm({ title: "Drop stash", message: `Drop ${stash.ref} (${stash.message})?`, run: () => void act("drop stash", () => ops.stashDrop(root, stash.ref), `dropped ${stash.ref}`) }), true
    }
    return false
  }

  // ---------------------------------------------------------------- render

  const leftW = Math.min(48, Math.max(34, Math.floor(ctx.width * 0.32)))
  const inner = leftW - 4
  const staged = files.filter(hasStaged).length
  const unstaged = files.filter(hasUnstaged).length
  const colorOf = (c: string) => (c === "?" ? theme.muted : c === "U" || c === "D" ? theme.red : theme.orange)

  const changeRows: ListRow[] = files.map((f) => ({
    key: `${f.path}:${f.x}${f.y}`,
    node: (
      <text>
        <span fg={f.x === "." ? theme.dim : theme.green}>{f.x === "." ? " " : f.x}</span>
        <span fg={colorOf(f.y)}>{f.y === "." ? " " : f.y}</span>
        <span fg={f.kind === "conflict" ? theme.red : theme.text}>{` ${fit(f.path, inner - 4)}`}</span>
      </text>
    ),
  }))
  const branchRows: ListRow[] = repo.branches.map((b) => ({
    key: b.name,
    node: (
      <text>
        <span fg={b.current ? theme.green : theme.dim}>{b.current ? "* " : "  "}</span>
        <span fg={b.current ? theme.text : theme.muted}>{fit(b.name, inner - 2 - 12)}</span>
        <span fg={theme.yellow}>{fit(b.track, 12)}</span>
      </text>
    ),
  }))
  const commitRows: ListRow[] = repo.commits.map((c, i) => ({
    key: `${i}:${c.hash}`,
    node: (
      <text>
        <span fg={theme.accent2}>{c.graph}</span>
        <span fg={theme.yellow}>{c.hash} </span>
        <span fg={theme.text}>{fit(c.subject, Math.max(1, inner - c.graph.length - 9))}</span>
      </text>
    ),
  }))
  const stashRows: ListRow[] = repo.stashes.map((s) => ({
    key: s.ref,
    node: (
      <text>
        <span fg={theme.accent2}>{s.ref} </span>
        <span fg={theme.text}>{fit(s.message, Math.max(1, inner - s.ref.length - 1))}</span>
      </text>
    ),
  }))

  const repoRows: ListRow[] = ctx.repos.map((e) => {
    const st = e.repo.status
    const tail = st ? [st.ahead ? `↑${st.ahead}` : "", st.behind ? `↓${st.behind}` : "", st.files.length ? `✎${st.files.length}` : ""].filter(Boolean).join(" ") : ""
    const nameW = Math.min(14, Math.max(6, Math.floor(inner / 2.5)))
    const branchW = Math.max(1, inner - 2 - nameW - 1 - (tail ? tail.length + 1 : 0))
    return {
      key: e.repo.root,
      node: (
        <text>
          <span fg={!st ? theme.dim : st.files.length ? theme.orange : theme.green}>{"● "}</span>
          <span fg={theme.text}>{fit(e.name, nameW)}</span>
          <span fg={theme.accent2}>{` ${fit(st ? (st.branch ?? `detached ${st.oid ?? ""}`) : "…", branchW)}`}</span>
          <span fg={theme.orange}>{tail ? ` ${tail}` : ""}</span>
        </text>
      ),
    }
  })
  const statusColor = (c: string) => (c === "A" ? theme.green : c === "D" ? theme.red : c === "R" || c === "C" ? theme.accent2 : c === "M" ? theme.orange : theme.muted)
  const fileRows: ListRow[] = revList.map((f) => ({
    key: `${f.status}:${f.orig ?? ""}:${f.path}`,
    node: (
      <text>
        <span fg={statusColor(f.status)}>{`${f.status} `}</span>
        <span fg={theme.text}>{fit(f.orig ? `${f.orig} → ${f.path}` : f.path, inner - 4)}</span>
      </text>
    ),
  }))
  const filesPanel = (pane: "commits" | "stash") =>
    revView?.pane === pane
      ? {
          title: `Files · ${revView.label}`,
          rows: fileRows,
          empty: revView.files ? "no files changed" : "loading…",
          footer: "enter diff · esc back",
          focused: focus === pane,
          onFocus: () => setFocus(pane),
          selected: revAt,
          onSelect: setRevSel,
        }
      : undefined
  const sync = status?.upstream ? `${status.ahead ? ` ↑${status.ahead}` : ""}${status.behind ? ` ↓${status.behind}` : ""}` : status?.branch ? " (no upstream)" : ""
  const panel = (pane: ListPane) => ({ focused: focus === pane, onFocus: () => setFocus(pane), selected: at(pane), onSelect: (i: number) => setSel((s) => ({ ...s, [pane]: i })) })
  const hunkNo = curHunk + 1
  const diffStatus = [rows.hunks.length ? `hunk ${hunkNo}/${rows.hunks.length}` : "", split ? "split" : ""].filter(Boolean).join(" · ")

  const showLists = !ctx.zoomed || isList(focus) || focus === "repos"
  const showDiff = !ctx.zoomed || focus === "gitdiff"
  const zoomedList = ctx.zoomed && (isList(focus) || focus === "repos") ? focus : undefined

  return (
    <box flexGrow={1} flexDirection="row">
      {showLists ? (
        <box width={ctx.zoomed ? ctx.width : leftW} flexDirection="column">
          {ctx.repos.length > 1 && (!zoomedList || zoomedList === "repos") && (
            <ListPanel
              title={`Repos · ${ctx.repos.length}`}
              rows={repoRows}
              empty=""
              footer={entry.services.length ? entry.services.join(", ") : undefined}
              height={zoomedList ? undefined : Math.min(ctx.repos.length, 6) + 2}
              focused={focus === "repos"}
              onFocus={() => setFocus("repos")}
              selected={index}
              onSelect={ctx.setRepoIndex}
            />
          )}
          {(!zoomedList || zoomedList === "changes") && (
            <ListPanel title={`Changes · ${status?.branch ?? (status?.oid ? `detached ${status.oid}` : "…")}${sync}`} rows={changeRows} empty="working tree clean" footer={files.length ? `${staged} staged · ${unstaged} unstaged` : undefined} grow={3} {...panel("changes")} />
          )}
          {(!zoomedList || zoomedList === "branches") && <ListPanel title="Branches" rows={branchRows} empty="no branches" grow={2} {...panel("branches")} />}
          {(!zoomedList || zoomedList === "commits") && (
            <ListPanel grow={3} {...(filesPanel("commits") ?? { title: "Commits", rows: commitRows, empty: "no commits yet", ...panel("commits") })} />
          )}
          {(!zoomedList || zoomedList === "stash") && (
            <ListPanel grow={1} {...(filesPanel("stash") ?? { title: "Stash", rows: stashRows, empty: "no stashes", ...panel("stash") })} />
          )}
        </box>
      ) : null}
      {showDiff ? (
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
          empty={target ? (source === "branches" && branch?.current ? "this is the branch you are on" : "no changes to show") : "nothing selected"}
          split={split ? { patch: diff.patch } : undefined}
        />
      ) : null}
      {prompt ? <PromptOverlay title={prompt.title} hint={prompt.hint} value={prompt.value} onInput={(v) => setPrompt((p) => (p ? { ...p, value: v } : p))} width={ctx.width} /> : null}
      {confirm ? <YesNoOverlay title={confirm.title} message={confirm.message} width={ctx.width} /> : null}
    </box>
  )
}

/** Footer hints for the focused panel. */
export function gitHints(focus: Pane): string[][] {
  const common = [["f/p/u", "fetch/pull/push"], ["tab", "panel"], [":", "commands"], ["q", "quit"]]
  if (focus === "repos") return [["j/k", "pick repo"], ["enter", "changes"], ["F", "fetch all"], ...common]
  if (focus === "changes") return [["space", "stage"], ["a", "all"], ["c", "commit"], ["A", "amend"], ["d", "discard"], ["s", "stash"], ["enter", "diff"], ...common]
  if (focus === "branches") return [["enter", "switch"], ["n", "new"], ["d", "delete"], ...common]
  if (focus === "commits") return [["enter", "files"], ["esc", "back"], ...common]
  if (focus === "stash") return [["enter", "files"], ["space", "apply"], ["o", "pop"], ["d", "drop"], ...common]
  return [["esc", "back"], ["j/k", "scroll"], ["[ ]", "hunk"], ["space", "stage hunk"], ["d", "discard hunk"], ["v", "staged/unstaged"], ["s", "split"], ...common]
}
