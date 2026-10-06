import type { FileChange } from "./status.ts"

/** One line of the Changes tree: a folder (with everything changed under it) or a changed file. */
export type TreeRow =
  | {
      kind: "dir"
      /** full path of the folder, which is also what folds it (`src/ui/views` when single-child folders are merged) */
      path: string
      /** what the row shows: the folder's own name, or the merged chain `ui/views` */
      name: string
      depth: number
      /** every change below it, folded or not */
      files: FileChange[]
      collapsed: boolean
    }
  | { kind: "file"; file: FileChange; name: string; depth: number }

interface Node {
  dirs: Map<string, Node>
  files: FileChange[]
}

const node = (): Node => ({ dirs: new Map(), files: [] })
const base = (path: string) => path.slice(path.lastIndexOf("/") + 1)
const byName = <T,>(key: (t: T) => string) => (a: T, b: T) => key(a).localeCompare(key(b))

/**
 * The changed files as a tree: folders first, then files, each sorted by name. A folder whose only child
 * is another folder is merged with it (`src/ui/views`), as editors do. Folded folders (by path) hide their contents.
 */
export function buildTree(files: FileChange[], collapsed: ReadonlySet<string> = new Set()): TreeRow[] {
  const root = node()
  for (const f of files) {
    let n = root
    for (const part of f.path.split("/").slice(0, -1)) {
      let child = n.dirs.get(part)
      if (!child) n.dirs.set(part, (child = node()))
      n = child
    }
    n.files.push(f)
  }
  const below = (n: Node): FileChange[] => [...n.files, ...[...n.dirs.values()].flatMap(below)]
  const rows: TreeRow[] = []
  const walk = (n: Node, prefix: string, depth: number) => {
    for (const [name, first] of [...n.dirs].sort(byName(([k]) => k))) {
      let label = name
      let end = first
      while (end.files.length === 0 && end.dirs.size === 1) {
        const [[k, child]] = [...end.dirs] as [[string, Node]]
        label += `/${k}`
        end = child
      }
      const path = prefix + label
      const isCollapsed = collapsed.has(path)
      rows.push({ kind: "dir", path, name: label, depth, files: below(end), collapsed: isCollapsed })
      if (!isCollapsed) walk(end, `${path}/`, depth + 1)
    }
    for (const file of [...n.files].sort(byName((f) => base(f.path)))) rows.push({ kind: "file", file, name: base(file.path), depth })
  }
  walk(root, "", 0)
  return rows
}
