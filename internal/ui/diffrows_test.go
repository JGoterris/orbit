package ui

import (
	"testing"

	"github.com/JGoterris/orbit/internal/git"
)

const sample = "diff --git a/a.go b/a.go\nindex 1..2\n--- a/a.go\n+++ b/a.go\n" +
	"@@ -1,3 +1,3 @@\n keep\n-old\n+new\n tail\n" +
	"@@ -10,2 +10,3 @@\n ctx\n+added\n+more\n" +
	"diff --git a/b.bin b/b.bin\nBinary files a/b.bin and b/b.bin differ\n"

func TestBuildRowsNumbersLinesAndLocatesHunks(t *testing.T) {
	r := buildRows(git.ParseDiff(sample))
	if len(r.hunks) != 2 {
		t.Fatalf("hunks = %d", len(r.hunks))
	}
	if r.rows[0].kind != rowFile || r.rows[0].text != "a.go" {
		t.Fatalf("first row = %+v", r.rows[0])
	}
	// keep(1,1) old(2,-) new(-,2) tail(3,3)
	var nums [][2]int
	for _, row := range r.rows[2:6] {
		nums = append(nums, [2]int{row.oldNo, row.newNo})
	}
	want := [][2]int{{1, 1}, {2, 0}, {0, 2}, {3, 3}}
	for i := range want {
		if nums[i] != want[i] {
			t.Fatalf("numbers = %v, want %v", nums, want)
		}
	}
	last := r.rows[len(r.rows)-1]
	if last.kind != rowNote || last.text != "binary file" {
		t.Fatalf("last row = %+v", last)
	}
}

func TestHunkNavigationHelpers(t *testing.T) {
	r := buildRows(git.ParseDiff(sample))
	if hunkAt(r.hunks, 0) != 0 || hunkAt(r.hunks, r.hunks[1].row) != 1 {
		t.Fatal("hunkAt")
	}
	page := 4
	if got := currentHunk(r, 1, 0, page); got != 0 {
		// the chosen hunk is not on screen (rows 0..3): the one at the top is used
		t.Fatalf("currentHunk = %d", got)
	}
	if got := currentHunk(r, 1, r.hunks[1].row, page); got != 1 {
		t.Fatalf("currentHunk on screen = %d", got)
	}
	if got := revealHunk(r, 1, 0, page); got != r.hunks[1].row {
		t.Fatalf("revealHunk = %d", got)
	}
	if got := revealHunk(r, 0, 0, 100); got != 0 {
		t.Fatalf("an already visible hunk keeps the scroll, got %d", got)
	}
}

func TestSplitPairsRemovedAndAddedLines(t *testing.T) {
	r := buildRows(git.ParseDiff(sample))
	rows := buildSplit(r)
	var pair *splitRow
	for i := range rows {
		if rows[i].hasLeft && rows[i].hasRight && rows[i].leftKind == rowDel {
			pair = &rows[i]
		}
	}
	if pair == nil || pair.left != "old" || pair.right != "new" {
		t.Fatalf("pair = %+v", pair)
	}
	// two additions with nothing removed: right side only
	var onlyRight int
	for _, s := range rows {
		if !s.hasLeft && s.hasRight {
			onlyRight++
		}
	}
	if onlyRight != 2 {
		t.Fatalf("right-only rows = %d", onlyRight)
	}
}
