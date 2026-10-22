package ui

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/JGoterris/orbit/internal/git"
	"github.com/JGoterris/orbit/internal/ui/theme"
)

// listRow draws one row of a list panel on the given background.
type listRow func(bg string) *line

// listStart is the index of the first row a listPanel of height h shows: the selection stays centred.
func listStart(n, selected, h int) int {
	visible := max(1, h-2)
	return max(0, min(selected-visible/2, n-visible))
}

// listPanel is a bordered, scrolling list of one-line rows with a selection bar.
func listPanel(title string, rows []listRow, selected int, focused bool, empty, footer string, w, h int) []string {
	p := theme.Active()
	inner := w - 2
	visibleRows := max(1, h-2)
	start := listStart(len(rows), selected, h)
	var content []string
	if len(rows) == 0 {
		content = append(content, newLine(p.Panel).add(p.Dim, empty).String())
	}
	for i := start; i < len(rows) && i < start+visibleRows; i++ {
		sel := i == selected
		bg := p.Panel
		if sel && focused {
			bg = p.Selection
		}
		l := newLine(bg)
		if sel {
			l.add(p.Accent, "▌")
		} else {
			l.add(p.Panel, " ")
		}
		row := rows[i](bg)
		l.raw(row.String(), row.width())
		content = append(content, l.pad(inner).String())
	}
	return panel(panelOpts{title: title, bottom: footer, focused: focused}, w, h, content)
}

var agoRe = regexp.MustCompile(` (second|minute|hour|day|week|month|year)s?`)

func ago(s string) string {
	s = strings.Replace(s, " ago", "", 1)
	return agoRe.ReplaceAllStringFunc(s, func(m string) string {
		unit := strings.TrimSuffix(strings.TrimSpace(m), "s")
		if unit == "month" {
			return "mo"
		}
		return unit[:1]
	})
}

// distribute splits total rows among weights (flex-grow), every share at least min.
func distribute(total int, weights []int, minimum int) []int {
	sum := 0
	for _, w := range weights {
		sum += w
	}
	out := make([]int, len(weights))
	left := total
	for i, w := range weights {
		out[i] = max(minimum, total*w/sum)
		left -= out[i]
	}
	for i := 0; left > 0; i = (i + 1) % len(out) {
		out[i]++
		left--
	}
	for i := len(out) - 1; left < 0 && i >= 0; i-- {
		if out[i] > minimum {
			out[i]--
			left++
		}
		if i == 0 && left < 0 {
			i = len(out)
		}
	}
	return out
}

func colorOfStatus(c byte, p theme.Palette) string {
	switch c {
	case '?':
		return p.Muted
	case 'U', 'D':
		return p.Red
	}
	return p.Orange
}

func (m *Model) renderGit(W, H int) []string {
	p := theme.Active()
	g := m.gs()
	if len(g.entries) == 0 {
		msg := newLine(p.Panel).add(p.Muted, "None of this project's services are in a git repository.").String()
		return panel(panelOpts{padX: 1}, W, H, []string{"", msg})
	}
	v, _ := m.gitView()
	index := min(g.index, len(g.entries)-1)

	leftW := min(48, max(34, W*32/100))
	inner := leftW - 4
	staged, unstaged := 0, 0
	for _, f := range v.files {
		if f.HasStaged() {
			staged++
		}
		if f.HasUnstaged() {
			unstaged++
		}
	}
	zoomed := ""
	if m.zoomed {
		zoomed = string(m.focus)
	}
	show := func(pn pane) bool { return zoomed == "" || zoomed == string(pn) }
	showLeft := zoomed == "" || isGitList(m.focus)
	showRight := zoomed == "" || m.focus == paneGitDiff || m.focus == paneGitLog

	// ---- repos table
	spread := git.BranchSpread(g.entries)
	common := ""
	if len(spread) > 0 {
		common = spread[0].Name
	}
	var spreadParts []string
	for _, s := range spread {
		spreadParts = append(spreadParts, fmt.Sprintf("%s ×%d", s.Name, s.Count))
	}
	tableInner := W - 4
	nameW, branchW := 6, 6
	snaps := make([]git.Snapshot, len(g.entries))
	for i, e := range g.entries {
		snaps[i] = e.Repo.Snapshot()
		nameW = max(nameW, min(18, len([]rune(e.Name))))
		b := "detached"
		if st := snaps[i].Status; st != nil && st.Branch != "" {
			b = st.Branch
		}
		branchW = max(branchW, min(22, len([]rune(b))))
	}
	const syncW, dirtyW = 13, 8
	rest := tableInner - 4 - nameW - 1 - branchW - 1 - syncW - dirtyW
	resultW := 0
	if rest > 70 {
		resultW = 26
	}
	servicesW := 0
	if rest-resultW > 56 {
		servicesW = 18
	}
	lastW := max(0, rest-resultW-servicesW)

	var repoRows []listRow
	for i, e := range g.entries {
		e, snap := e, snaps[i]
		repoRows = append(repoRows, func(bg string) *line {
			st := snap.Status
			l := newLine(bg)
			if g.marked[e.Repo.Root] {
				l.add(p.Accent, "✓ ")
			} else {
				l.add(p.Accent, "  ")
			}
			dot := p.Green
			switch {
			case st == nil:
				dot = p.Dim
			case len(st.Files) > 0:
				dot = p.Orange
			}
			l.add(dot, "● ").add(p.Text, theme.FitMid(e.Name, nameW))
			branchName := "…"
			sync := ""
			if st != nil {
				branchName = st.Branch
				if branchName == "" {
					branchName = "detached " + st.Oid
				}
				switch {
				case st.Upstream == "" && st.Branch != "":
					sync = "no upstream"
				case st.Upstream != "":
					var parts []string
					if st.Ahead > 0 {
						parts = append(parts, fmt.Sprintf("↑%d", st.Ahead))
					}
					if st.Behind > 0 {
						parts = append(parts, fmt.Sprintf("↓%d", st.Behind))
					}
					sync = strings.Join(parts, " ")
					if sync == "" {
						sync = "="
					}
				}
			}
			bc := p.Accent2
			if st != nil && st.Branch != "" && common != "" && st.Branch != common {
				bc = p.Yellow
			}
			l.add(bc, " "+theme.FitMid(branchName, branchW))
			sc := p.Muted
			if st != nil && st.Behind > 0 {
				sc = p.Yellow
			}
			l.add(sc, " "+theme.Fit(sync, syncW-1))
			var dirty []string
			if st != nil && len(st.Files) > 0 {
				dirty = append(dirty, fmt.Sprintf("✎%d", len(st.Files)))
			}
			if len(snap.Stashes) > 0 {
				dirty = append(dirty, fmt.Sprintf("⚑%d", len(snap.Stashes)))
			}
			l.add(p.Orange, theme.Fit(strings.Join(dirty, " "), dirtyW))
			if lastW > 8 {
				if len(snap.Commits) > 0 {
					c := snap.Commits[0]
					l.add(p.Yellow, c.Hash+" ").add(p.Text, theme.Fit(c.Subject, max(1, lastW-17))).add(p.Dim, theme.Fit(" "+ago(c.When), 9))
				} else {
					l.add(p.Text, strings.Repeat(" ", lastW))
				}
			}
			if servicesW > 0 {
				l.add(p.Muted, theme.Fit(strings.Join(e.Services, ", "), servicesW))
			}
			if resultW > 0 {
				out, ok := g.results[e.Repo.Root]
				text, color := "", p.Green
				if ok {
					mark := "✓"
					if !out.ok {
						mark, color = "✗", p.Red
					}
					text = mark + " " + out.text
				}
				l.add(color, theme.Fit(text, resultW))
			}
			return l
		})
	}
	targetNote := "none marked: all"
	if len(g.marked) > 0 {
		targetNote = fmt.Sprintf("%d marked", len(g.marked))
	}
	repoTitle := fmt.Sprintf("Repos · %d", len(g.entries))
	if len(spreadParts) > 0 {
		repoTitle += " · " + strings.Join(spreadParts, " · ")
	}
	repoH := min(len(g.entries), 8) + 2
	if zoomed == string(paneRepos) {
		repoH = H
	}
	var out []string
	if show(paneRepos) {
		m.hit(paneRepos, 0, 1, W, repoH, len(repoRows), listStart(len(repoRows), index, repoH))
		out = append(out, listPanel(repoTitle, repoRows, index, m.focus == paneRepos, "",
			targetNote+" · m multi-repo · L lazygit", W, repoH)...)
	}
	if zoomed == string(paneRepos) {
		return out
	}
	rest2 := H - len(out)
	top := 1 + len(out)

	// ---- left column
	var left []string
	if showLeft {
		lw := leftW
		if m.zoomed {
			lw = W
		}
		vis := []pane{}
		for _, pn := range gitLists {
			if show(pn) {
				vis = append(vis, pn)
			}
		}
		weights := map[pane]int{paneChanges: 3, paneBranches: 2, paneCommits: 3, paneStash: 1}
		ws := make([]int, len(vis))
		for i, pn := range vis {
			ws[i] = weights[pn]
		}
		heights := distribute(rest2, ws, 3)
		status := v.snap.Status
		y := top
		for i, pn := range vis {
			h := heights[i]
			sel, count := 0, 0
			switch pn {
			case paneChanges:
				count = len(v.tree)
			case paneBranches:
				count = len(v.snap.Branches)
			case paneCommits:
				count = len(v.snap.Commits)
			case paneStash:
				count = len(v.snap.Stashes)
			}
			sel = g.at(pn, count)
			m.hit(pn, 0, y, lw, h, count, listStart(count, sel, h))
			y += h
			switch pn {
			case paneChanges:
				branch := "…"
				sync := ""
				if status != nil {
					branch = status.Branch
					if branch == "" {
						branch = "detached " + status.Oid
					}
					switch {
					case status.Upstream != "":
						if status.Ahead > 0 {
							sync += fmt.Sprintf(" ↑%d", status.Ahead)
						}
						if status.Behind > 0 {
							sync += fmt.Sprintf(" ↓%d", status.Behind)
						}
					case status.Branch != "":
						sync = " (no upstream)"
					}
				}
				var rows []listRow
				for _, r := range v.tree {
					r := r
					rows = append(rows, func(bg string) *line {
						l := newLine(bg)
						pad := strings.Repeat("  ", r.Depth)
						if r.Dir {
							mark := "▾"
							if r.Collapsed {
								mark = "▸"
							}
							count := fmt.Sprintf(" %d", len(r.Files))
							l.add(p.Dim, "   ").add(p.Accent2, theme.Fit(fmt.Sprintf("%s%s %s/", pad, mark, r.Name), max(1, lw-4-4-len(count)))).add(p.Dim, count)
							return l
						}
						f := r.File
						if f.X == '.' {
							l.add(p.Dim, " ")
						} else {
							l.add(p.Green, string(f.X))
						}
						if f.Y == '.' {
							l.add(p.Text, " ")
						} else {
							l.add(colorOfStatus(f.Y, p), string(f.Y))
						}
						c := p.Text
						if f.Kind == git.Conflict {
							c = p.Red
						}
						l.add(c, " "+theme.Fit(pad+r.Name, lw-4-4))
						return l
					})
				}
				footer := ""
				if len(v.files) > 0 {
					footer = fmt.Sprintf("%d staged · %d unstaged", staged, unstaged)
				}
				left = append(left, listPanel("Changes · "+branch+sync, rows, g.at(paneChanges, len(v.tree)), m.focus == paneChanges, "working tree clean", footer, lw, h)...)
			case paneBranches:
				var rows []listRow
				for _, b := range v.snap.Branches {
					b := b
					rows = append(rows, func(bg string) *line {
						l := newLine(bg)
						if b.Current {
							l.add(p.Green, "* ").add(p.Text, theme.FitMid(b.Name, max(1, lw-4-2-20)))
						} else {
							l.add(p.Dim, "  ").add(p.Muted, theme.FitMid(b.Name, max(1, lw-4-2-20)))
						}
						l.add(p.Yellow, theme.Fit(b.Track, 9)).add(p.Dim, theme.Fit(" "+ago(b.When), 11))
						return l
					})
				}
				left = append(left, listPanel("Branches", rows, g.at(paneBranches, len(rows)), m.focus == paneBranches, "no branches", "", lw, h)...)
			case paneCommits:
				graphW := max(20, lw-4)
				metaW := 0
				if graphW >= 70 {
					metaW = 24
				}
				var rows []listRow
				for _, c := range v.snap.Commits {
					c := c
					rows = append(rows, func(bg string) *line {
						refs := ""
						if c.Refs != "" {
							refs = "(" + c.Refs + ") "
						}
						used := len([]rune(c.Graph)) + len(c.Hash) + 1 + len([]rune(refs))
						l := newLine(bg)
						l.add(p.Accent2, c.Graph).add(p.Yellow, c.Hash+" ").add(p.Green, refs).add(p.Text, theme.Fit(c.Subject, max(1, graphW-used-metaW)))
						if metaW > 0 {
							l.add(p.Dim, theme.Fit(" "+c.Author+" · "+ago(c.When), metaW))
						}
						return l
					})
				}
				left = append(left, listPanel("Graph", rows, g.at(paneCommits, len(rows)), m.focus == paneCommits, "no commits yet", "", lw, h)...)
			case paneStash:
				var rows []listRow
				for _, s := range v.snap.Stashes {
					s := s
					rows = append(rows, func(bg string) *line {
						return newLine(bg).add(p.Accent2, s.Ref+" ").add(p.Text, theme.Fit(s.Message, max(1, inner-len(s.Ref)-1)))
					})
				}
				left = append(left, listPanel("Stash", rows, g.at(paneStash, len(rows)), m.focus == paneStash, "no stashes", "", lw, h)...)
			}
		}
	}

	// ---- right column
	var right []string
	if showRight {
		rw := W - leftW
		if m.zoomed {
			rw = W
		}
		cmdH := 8
		if zoomed != "" {
			cmdH = rest2
		}
		diffH := rest2 - cmdH
		if zoomed == string(paneGitLog) {
			diffH = 0
		}
		if zoomed == string(paneGitDiff) {
			diffH, cmdH = rest2, 0
		}
		rx := 0
		if showLeft && zoomed == "" {
			rx = leftW
		}
		if diffH > 0 && show(paneGitDiff) {
			m.hit(paneGitDiff, rx, top, rw, diffH, 0, 0)
		}
		if cmdH > 0 && show(paneGitLog) {
			n := len(v.cmds)
			m.hit(paneGitLog, rx, top+max(0, diffH), rw, cmdH, n, listStart(n, v.logAt, cmdH))
		}
		if diffH > 0 && show(paneGitDiff) {
			right = append(right, m.renderGitDiff(v, rw, diffH)...)
		}
		if cmdH > 0 && show(paneGitLog) {
			right = append(right, m.renderGitCommands(v, rw, cmdH)...)
		}
	}

	switch {
	case showLeft && showRight && zoomed == "":
		out = append(out, hconcat(left, right)...)
	case showLeft:
		out = append(out, left...)
	default:
		out = append(out, right...)
	}
	for len(out) < H {
		out = append(out, blank(W, p.Bg))
	}
	return out[:H]
}

func tabs(s string) string { return strings.ReplaceAll(s, "\t", "    ") }

func (m *Model) renderGitDiff(v gitView, w, h int) []string {
	p := theme.Active()
	g := m.gs()
	height := max(1, h-2)
	g.pageRows = height
	width := max(20, w-4)
	rows := g.rows
	total := len(rows.rows)
	var split []splitRow
	if g.split {
		split = buildSplit(rows)
		total = len(split)
	}
	maxScroll := max(0, total-height)
	g.scroll = max(0, min(g.scroll, maxScroll))
	top := g.scroll
	cur := currentHunk(rows, g.hunkIdx, min(top, max(0, len(rows.rows)-1)), height)

	addBg := theme.Mix(p.Panel, p.Green, 0.16)
	delBg := theme.Mix(p.Panel, p.Red, 0.16)
	const gutter = 4
	textW := max(1, width-2-gutter*2-1)
	num := func(n int) string {
		if n == 0 {
			return strings.Repeat(" ", gutter)
		}
		return padStart(fmt.Sprint(n), gutter)
	}

	var content []string
	titleTarget := ""
	if t := v.target(); t != nil {
		titleTarget = t.title
	}
	switch {
	case total == 0:
		empty := "nothing selected"
		switch {
		case v.target() != nil && v.source == paneBranches && v.branch != nil && v.branch.Current:
			empty = "this is the branch you are on"
		case v.target() != nil:
			empty = "no changes to show"
		case v.source == paneChanges && v.dir != nil:
			empty = fmt.Sprintf("%s/ · %d changed file%s (enter folds, space stages them all)", v.dir.Path, len(v.dir.Files), plural(len(v.dir.Files)))
		}
		content = append(content, newLine(p.Panel).add(p.Dim, empty).String())
	case g.split:
		half := max(5, (width-1)/2)
		side := func(text string, kind rowKind, no int, has bool) *line {
			l := newLine(p.Panel)
			if !has {
				return l.add(p.Dim, strings.Repeat(" ", half))
			}
			bg, fg, sign := "", p.Text, " "
			switch kind {
			case rowAdd:
				bg, fg, sign = addBg, p.Green, "+"
			case rowDel:
				bg, fg, sign = delBg, p.Red, "-"
			}
			l.add(p.Dim, num(no)+" ")
			l.addStyled(fg, bg, false, theme.Fit(sign+tabs(text), max(1, half-gutter-1)))
			return l
		}
		for _, r := range split[top:min(len(split), top+height)] {
			if r.full != "" {
				switch r.fullKind {
				case rowFile:
					content = append(content, newLine(p.PanelAlt).bold(p.Accent, theme.Fit(" "+r.full, width)).String())
				case rowHunk:
					content = append(content, newLine(p.Panel).add(p.Accent2, theme.Fit(" "+r.full, width)).String())
				default:
					content = append(content, newLine(p.Panel).add(p.Dim, "  "+r.full).String())
				}
				continue
			}
			l := newLine(p.Panel)
			a := side(r.left, r.leftKind, r.leftNo, r.hasLeft)
			b := side(r.right, r.rightKind, r.rightNo, r.hasRight)
			l.raw(a.String(), a.width()).add(p.Border, "│").raw(b.String(), b.width())
			content = append(content, l.String())
		}
	default:
		selLo, selHi := -1, -1
		if g.selOn {
			selLo, selHi = g.selCur, g.selCur
			if g.selAnchor >= 0 {
				selLo, selHi = min(g.selAnchor, g.selCur), max(g.selAnchor, g.selCur)
			}
		}
		for i, r := range rows.rows[top:min(len(rows.rows), top+height)] {
			// lines being selected for a copy
			selBg := ""
			if at := top + i; at == g.selCur && g.selOn {
				selBg = p.Cursor
			} else if at >= selLo && at <= selHi {
				selBg = p.Selection
			}
			mark := " "
			if r.hunk >= 0 && r.hunk == cur {
				mark = "▌"
			}
			l := newLine(p.Panel)
			switch r.kind {
			case rowFile:
				bg := p.PanelAlt
				if selBg != "" {
					bg = selBg
				}
				l.addStyled(p.Accent, bg, true, theme.Fit(" "+r.text, width))
			case rowNote:
				l.addStyled(p.Dim, selBg, false, theme.Fit("  "+r.text, width))
			case rowHunk:
				l.add(p.Accent, mark).addStyled(p.Accent2, selBg, false, theme.Fit(" "+r.text, width-1))
			default:
				bg, fg, sign := "", p.Text, " "
				switch r.kind {
				case rowAdd:
					bg, fg, sign = addBg, p.Green, "+"
				case rowDel:
					bg, fg, sign = delBg, p.Red, "-"
				case rowMarker:
					fg = p.Dim
				}
				text := sign + tabs(r.text)
				if r.kind == rowMarker {
					text = r.text
				}
				if selBg != "" {
					bg = selBg
				}
				l.add(p.Accent, mark).add(p.Dim, num(r.oldNo)+" "+num(r.newNo)+" ").addStyled(fg, bg, false, theme.Fit(text, textW))
			}
			content = append(content, l.String())
		}
	}

	var parts []string
	files := 0
	fileIdx := -1
	if v.source == paneChanges {
		g2 := m.gs()
		for i, r := range v.tree {
			if !r.Dir {
				files++
				if i == g2.at(paneChanges, len(v.tree)) {
					fileIdx = files
				}
			}
		}
	}
	if files > 1 && fileIdx > 0 {
		parts = append(parts, fmt.Sprintf("file %d/%d", fileIdx, files))
	}
	if len(rows.hunks) > 0 && !g.split {
		parts = append(parts, fmt.Sprintf("hunk %d/%d", cur+1, len(rows.hunks)))
	}
	if g.split {
		parts = append(parts, "split")
	}
	if g.selAnchor >= 0 {
		parts = append(parts, fmt.Sprintf("%d selected", max(g.selCur-g.selAnchor, g.selAnchor-g.selCur)+1))
	} else if g.selOn {
		parts = append(parts, "copy mode")
	}
	title := "Diff"
	if titleTarget != "" {
		title += " · " + titleTarget
	}
	return panel(panelOpts{title: title, bottom: strings.Join(parts, " · "), focused: m.focus == paneGitDiff, padX: 1}, w, h, content)
}

func (m *Model) renderGitCommands(v gitView, w, h int) []string {
	p := theme.Active()
	g := m.gs()
	nameW := 4
	names := map[string]string{}
	for _, e := range g.entries {
		names[e.Repo.Root] = e.Name
		nameW = max(nameW, min(18, len([]rune(e.Name))))
	}
	var rows []listRow
	for _, c := range v.cmds {
		c := c
		rows = append(rows, func(bg string) *line {
			l := newLine(bg)
			l.add(p.Dim, c.At.Format("15:04:05")+" ")
			if c.OK {
				l.add(p.Green, "✓ ")
			} else {
				l.add(p.Red, "✗ ")
			}
			l.add(p.Accent2, theme.FitMid(names[c.Root], nameW)).add(p.Text, " git "+git.FormatArgs(c.Args)).add(p.Dim, fmt.Sprintf("  %dms", c.Ms))
			if !c.OK && c.Message != "" {
				l.add(p.Red, "  "+c.Message)
			}
			return l
		})
	}
	footer := ""
	if v.cmdEntry != nil {
		footer = v.cmdEntry.Message
	}
	return listPanel("Commands", rows, v.logAt, m.focus == paneGitLog,
		"no git command run yet (stage, commit, fetch, pull, push, branches…)", footer, w, h)
}

func (m *Model) gitHints() [][2]string {
	g := m.gs()
	common := [][2]string{{"?", "help"}, {"q", "quit"}}
	net := [2]string{"f/p/u", "fetch/pull/push"}
	lazy := [2]string{"L", "lazygit"}
	if g.multi {
		on := fmt.Sprintf("all %d", len(g.entries))
		if len(g.marked) > 0 {
			on = fmt.Sprintf("%d marked", len(g.marked))
		}
		return [][2]string{{"f", "fetch"}, {"p", "pull"}, {"u", "push"}, {"b", "new branch"}, {"s", "switch branch"}, {"esc", "cancel · on " + on}}
	}
	join := func(parts ...[][2]string) [][2]string {
		var out [][2]string
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	switch m.focus {
	case paneRepos:
		return join([][2]string{{"j/k", "pick repo"}, {"space", "mark"}, {"a", "mark all"}, {"m", "multi-repo"}, {"enter", "changes"}, lazy, net}, common)
	case paneChanges:
		return join([][2]string{{"space", "stage"}, {"a", "all"}, {"c", "commit"}, {"y", "copy path"}, {"enter", "diff/fold"}, net, lazy, {"m", "multi"}}, common)
	case paneBranches:
		return join([][2]string{{"enter", "switch"}, {"n", "new"}, {"y", "copy name"}, net, lazy, {"m", "multi"}}, common)
	case paneCommits:
		return join([][2]string{{"enter", "diff"}, {"y", "copy hash"}, net, lazy, {"m", "multi"}}, common)
	case paneStash:
		return join([][2]string{{"enter", "diff"}, {"y", "copy ref"}, net, lazy, {"m", "multi"}}, common)
	case paneGitLog:
		return join([][2]string{{"j/k", "move"}, {"G", "latest"}, {"y", "copy command"}, {"c", "clear"}, lazy, {"m", "multi"}}, common)
	}
	if g.selOn {
		if g.selAnchor >= 0 {
			return join([][2]string{{"j/k", "extend"}, {"y", "copy lines"}, {"v", "unmark"}, {"esc", "exit"}}, common)
		}
		return join([][2]string{{"j/k", "move"}, {"v", "start selecting"}, {"y", "copy line"}, {"esc", "exit"}}, common)
	}
	hints := [][2]string{{"esc", "back"}, {"j/k", "scroll"}, {"[ ]", "hunk"}, {"v", "copy mode"}}
	if v, ok := m.gitView(); ok && v.source == paneChanges {
		n := 0
		for _, r := range v.tree {
			if !r.Dir {
				n++
			}
		}
		if n > 1 {
			hints = append(hints, [2]string{"{ }", "file"})
		}
	}
	hints = append(hints, [2]string{"s", "split"})
	if v, ok := m.gitView(); ok && v.source == paneChanges {
		hints = append(hints, [2]string{"y", "copy path"})
	}
	return join(hints, [][2]string{lazy}, common)
}

// renderGitPrompt draws the commit / branch prompt or a yes-no question over the Git view.
func (m *Model) renderGitModal(W, H int) ([]string, int, int, int, bool) {
	p := theme.Active()
	g := m.gs()
	switch {
	case g.prompt != nil:
		w := min(80, W-4)
		pr := g.prompt
		content := []string{
			newLine(p.PanelAlt).add(p.Accent, "› ").raw(pr.in.view(w-6, p.Text, p.Dim, p.PanelAlt, true), w-6).String(),
			newLine(p.PanelAlt).add(p.Border, strings.Repeat("─", w-4)).String(),
			newLine(p.PanelAlt).add(p.Dim, pr.hint).String(),
		}
		x, y := center(W, H, w, 5)
		return modalBox(pr.title, w, 5, content), w, x, y, true
	case g.confirm != nil:
		w := min(70, W-4)
		c := g.confirm
		content := []string{
			newLine(p.PanelAlt).add(p.Text, theme.Fit(c.message, w-4)).String(),
			newLine(p.PanelAlt).add(p.Red, "y / enter").add(p.Dim, " confirm").String(),
			newLine(p.PanelAlt).add(p.Accent, "n / esc").add(p.Dim, " cancel").String(),
		}
		x, y := center(W, H, w, 6)
		return modalBox(c.title, w, 6, content), w, x, y, true
	}
	return nil, 0, 0, 0, false
}
