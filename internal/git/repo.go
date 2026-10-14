package git

import (
	"reflect"
	"sync"
)

// Repo is a git repository orbit watches: the last known status, branches, commits and stashes.
type Repo struct {
	Root string

	mu       sync.Mutex
	status   *Status
	branches []BranchInfo
	commits  []CommitInfo
	stashes  []StashInfo
	pending  chan struct{} // closed when the refresh in flight is over
	again    string        // "", "status" or "all": one more run requested while one was in flight
	version  int
}

// NewRepo watches the repository at root.
func NewRepo(root string) *Repo { return &Repo{Root: root} }

// Find is the repo that holds dir, or nil.
func Find(dir string) *Repo {
	if root := FindRoot(dir); root != "" {
		return NewRepo(root)
	}
	return nil
}

// Snapshot is a consistent copy of what is known so far.
type Snapshot struct {
	Status   *Status
	Branches []BranchInfo
	Commits  []CommitInfo
	Stashes  []StashInfo
	Version  int
}

func (r *Repo) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := Snapshot{Branches: r.branches, Commits: r.commits, Stashes: r.stashes, Version: r.version}
	if r.status != nil {
		st := *r.status
		s.Status = &st
	}
	return s
}

// Refresh refreshes the cheap part (status: what "status" says) or everything ("all"). Concurrent calls are
// coalesced into one more run; it returns when the run it joined is over.
func (r *Repo) Refresh(what string) {
	r.mu.Lock()
	if r.pending != nil {
		if what == "all" || r.again == "all" {
			r.again = "all"
		} else {
			r.again = "status"
		}
		done := r.pending
		r.mu.Unlock()
		<-done
		return
	}
	done := make(chan struct{})
	r.pending = done
	r.mu.Unlock()

	r.load(what)

	r.mu.Lock()
	r.pending = nil
	next := r.again
	r.again = ""
	r.mu.Unlock()
	close(done)
	if next != "" {
		go r.Refresh(next)
	}
}

func (r *Repo) load(what string) {
	var (
		st       Status
		ok       bool
		branches []BranchInfo
		commits  []CommitInfo
		stashes  []StashInfo
		wg       sync.WaitGroup
	)
	wg.Add(1)
	go func() { defer wg.Done(); st, ok = ReadStatus(r.Root) }()
	if what == "all" {
		wg.Add(3)
		go func() { defer wg.Done(); branches = Branches(r.Root) }()
		go func() { defer wg.Done(); commits = Log(r.Root, 200) }()
		go func() { defer wg.Done(); stashes = Stashes(r.Root) }()
	}
	wg.Wait()

	r.mu.Lock()
	defer r.mu.Unlock()
	changed := false
	if ok && (r.status == nil || !reflect.DeepEqual(*r.status, st)) {
		s := st
		r.status = &s
		changed = true
	}
	if what == "all" {
		if !reflect.DeepEqual(r.branches, branches) {
			r.branches, changed = branches, true
		}
		if !reflect.DeepEqual(r.commits, commits) {
			r.commits, changed = commits, true
		}
		if !reflect.DeepEqual(r.stashes, stashes) {
			r.stashes, changed = stashes, true
		}
	}
	if changed {
		r.version++
	}
}

// Run runs a git operation and refreshes everything afterwards (also when it failed: it may have half-applied).
func (r *Repo) Run(op func() OpResult) OpResult {
	res := op()
	r.Refresh("all")
	return res
}
