package git

import (
	"path/filepath"
	"slices"
	"sort"
)

// Entry is a repository that some part of the project lives in.
type Entry struct {
	Repo *Repo
	// Name is a short label: the folder name (with its parent when two repos share a name)
	Name string
	// Services are the services whose working directory is inside this repo
	Services []string
}

// Discover finds the repos of a project. A project is a set of services and each may live in a different repo (or
// several in the same one), so they come from the services' working directories; the project folder's own repo, if
// it has one, goes first. Each repo appears once. root is the project folder, cwds maps service name -> cwd in
// declaration order (names).
func Discover(root string, names []string, cwd func(name string) string) []Entry {
	var order []string
	byRoot := map[string]*Entry{}
	add := func(r, service string) {
		if r == "" {
			return
		}
		e := byRoot[r]
		if e == nil {
			e = &Entry{Repo: NewRepo(r), Name: filepath.Base(r)}
			byRoot[r] = e
			order = append(order, r)
		}
		if service != "" && !slices.Contains(e.Services, service) {
			e.Services = append(e.Services, service)
		}
	}
	add(FindRoot(root), "")
	for _, n := range names {
		add(FindRoot(cwd(n)), n)
	}
	entries := make([]Entry, 0, len(order))
	for _, r := range order {
		entries = append(entries, *byRoot[r])
	}
	return nameRepos(entries)
}

// nameRepos: folder names are labels; two repos called the same get their parent folder to tell them apart.
func nameRepos(entries []Entry) []Entry {
	count := map[string]int{}
	for _, e := range entries {
		count[e.Name]++
	}
	for i := range entries {
		if count[entries[i].Name] > 1 {
			entries[i].Name = filepath.Base(filepath.Dir(entries[i].Repo.Root)) + "/" + entries[i].Name
		}
	}
	return entries
}

// EntriesOf wraps repos handed in directly (tests, embedding) as entries.
func EntriesOf(repos []*Repo) []Entry {
	entries := make([]Entry, len(repos))
	for i, r := range repos {
		entries[i] = Entry{Repo: r, Name: filepath.Base(r.Root)}
	}
	return nameRepos(entries)
}

// RepoOfService is the index of the repo holding service, or -1.
func RepoOfService(entries []Entry, service string) int {
	for i, e := range entries {
		if slices.Contains(e.Services, service) {
			return i
		}
	}
	return -1
}

// RepoResult is the outcome of an operation on one repo.
type RepoResult struct {
	Entry Entry
	Res   OpResult
}

// RunOnRepos runs op on every repo at once, then refreshes each of them (also the ones that failed: they may
// have half-applied).
func RunOnRepos(entries []Entry, op func(Entry) OpResult) []RepoResult {
	out := make([]RepoResult, len(entries))
	done := make(chan struct{}, len(entries))
	for i, e := range entries {
		go func() {
			out[i] = RepoResult{Entry: e, Res: e.Repo.Run(func() OpResult { return op(e) })}
			done <- struct{}{}
		}()
	}
	for range entries {
		<-done
	}
	return out
}

// Summarize makes one outcome for the lot: ok when every repo was, else "pulled 2/3 · web: Not possible to
// fast-forward".
func Summarize(verb string, results []RepoResult) OpResult {
	var bad []RepoResult
	for _, r := range results {
		if !r.Res.OK {
			bad = append(bad, r)
		}
	}
	if len(bad) == 0 {
		if len(results) == 1 {
			return OpResult{OK: true, Message: verb}
		}
		return OpResult{OK: true, Message: verb + " " + itoa(len(results)) + " repos"}
	}
	first := bad[0]
	return OpResult{Message: verb + " " + itoa(len(results)-len(bad)) + "/" + itoa(len(results)) + " · " + first.Entry.Name + ": " + first.Res.Message}
}

// BranchCount is how many repos are on a branch.
type BranchCount struct {
	Name  string
	Count int
}

// BranchSpread lists the branches the repos are on, most common first (detached HEADs and repos not read yet are
// left out).
func BranchSpread(entries []Entry) []BranchCount {
	count := map[string]int{}
	for _, e := range entries {
		if st := e.Repo.Snapshot().Status; st != nil && st.Branch != "" {
			count[st.Branch]++
		}
	}
	out := make([]BranchCount, 0, len(count))
	for n, c := range count {
		out = append(out, BranchCount{n, c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	return out
}
