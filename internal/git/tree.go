package git

import (
	"sort"
	"strings"
)

// TreeRow is one line of the Changes tree: a folder (with everything changed under it) or a changed file.
type TreeRow struct {
	// Dir is true for a folder row
	Dir bool
	// Path is the full path of the folder (what folds it: `src/ui/views` when single-child folders are merged);
	// for a file row, the path of the file
	Path string
	// Name is what the row shows: the folder's own name, or the merged chain `ui/views`; the file's base name
	Name  string
	Depth int
	// Files are every change below a folder, folded or not
	Files     []FileChange
	Collapsed bool
	// File is the change of a file row
	File FileChange
}

type node struct {
	dirs  map[string]*node
	files []FileChange
}

func newNode() *node { return &node{dirs: map[string]*node{}} }

func baseName(p string) string { return p[strings.LastIndex(p, "/")+1:] }

func below(n *node) []FileChange {
	out := append([]FileChange(nil), n.files...)
	keys := sortedKeys(n.dirs)
	for _, k := range keys {
		out = append(out, below(n.dirs[k])...)
	}
	return out
}

func sortedKeys(m map[string]*node) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// BuildTree lays the changed files out as a tree: folders first, then files, each sorted by name. A folder whose
// only child is another folder is merged with it (`src/ui/views`), as editors do. Folded folders (by path) hide
// their contents.
func BuildTree(files []FileChange, collapsed map[string]bool) []TreeRow {
	root := newNode()
	for _, f := range files {
		n := root
		parts := strings.Split(f.Path, "/")
		for _, part := range parts[:len(parts)-1] {
			child := n.dirs[part]
			if child == nil {
				child = newNode()
				n.dirs[part] = child
			}
			n = child
		}
		n.files = append(n.files, f)
	}
	var rows []TreeRow
	var walk func(n *node, prefix string, depth int)
	walk = func(n *node, prefix string, depth int) {
		for _, name := range sortedKeys(n.dirs) {
			label, end := name, n.dirs[name]
			for len(end.files) == 0 && len(end.dirs) == 1 {
				k := sortedKeys(end.dirs)[0]
				label += "/" + k
				end = end.dirs[k]
			}
			path := prefix + label
			isCollapsed := collapsed[path]
			rows = append(rows, TreeRow{Dir: true, Path: path, Name: label, Depth: depth, Files: below(end), Collapsed: isCollapsed})
			if !isCollapsed {
				walk(end, path+"/", depth+1)
			}
		}
		files := append([]FileChange(nil), n.files...)
		sort.SliceStable(files, func(i, j int) bool { return baseName(files[i].Path) < baseName(files[j].Path) })
		for _, f := range files {
			rows = append(rows, TreeRow{Path: f.Path, Name: baseName(f.Path), Depth: depth, File: f})
		}
	}
	walk(root, "", 0)
	return rows
}
