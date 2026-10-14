// Package graph holds the algorithms over the dependency graph of services: name -> dependencies.
package graph

// DepMap is name -> dependencies. Names keeps the declaration order, which breaks every tie.
type DepMap struct {
	Names []string
	Deps  map[string][]string
}

func (d DepMap) of(n string) []string { return d.Deps[n] }

// FindCycle returns a cycle as a list of names (first == last), or nil.
func FindCycle(d DepMap) []string {
	state := map[string]int{} // 1 = visiting, 2 = done
	var stack []string
	var visit func(n string) []string
	visit = func(n string) []string {
		switch state[n] {
		case 2:
			return nil
		case 1:
			i := 0
			for j, s := range stack {
				if s == n {
					i = j
				}
			}
			return append(append([]string{}, stack[i:]...), n)
		}
		state[n] = 1
		stack = append(stack, n)
		for _, dep := range d.of(n) {
			if c := visit(dep); c != nil {
				return c
			}
		}
		stack = stack[:len(stack)-1]
		state[n] = 2
		return nil
	}
	for _, n := range d.Names {
		if c := visit(n); c != nil {
			return c
		}
	}
	return nil
}

// TopoOrder puts dependencies first. Stable: ties keep the order of names.
func TopoOrder(d DepMap, names []string) []string {
	if names == nil {
		names = d.Names
	}
	var out []string
	seen := map[string]bool{}
	var visit func(n string)
	visit = func(n string) {
		if seen[n] {
			return
		}
		seen[n] = true
		for _, dep := range d.of(n) {
			visit(dep)
		}
		out = append(out, n)
	}
	for _, n := range names {
		visit(n)
	}
	return out
}

// Levels: level 0 = no dependencies; otherwise 1 + max(level of deps).
func Levels(d DepMap) map[string]int {
	memo := map[string]int{}
	var level func(n string) int
	level = func(n string) int {
		if v, ok := memo[n]; ok {
			return v
		}
		best := -1
		for _, dep := range d.of(n) {
			if l := level(dep); l > best {
				best = l
			}
		}
		memo[n] = best + 1
		return best + 1
	}
	for _, n := range d.Names {
		level(n)
	}
	return memo
}

// TransitiveDeps are all the services `name` needs, directly or not.
func TransitiveDeps(d DepMap, name string) map[string]bool {
	out := map[string]bool{}
	var walk func(n string)
	walk = func(n string) {
		for _, dep := range d.of(n) {
			if !out[dep] {
				out[dep] = true
				walk(dep)
			}
		}
	}
	walk(name)
	return out
}

// DependentsMap is the reverse graph: name -> the services that depend on it.
func DependentsMap(d DepMap) map[string][]string {
	out := map[string][]string{}
	for _, n := range d.Names {
		out[n] = nil
	}
	for _, n := range d.Names {
		for _, dep := range d.of(n) {
			out[dep] = append(out[dep], n)
		}
	}
	return out
}

// TransitiveDependents are all the services that need `name`, directly or not.
func TransitiveDependents(d DepMap, name string) map[string]bool {
	rev := DependentsMap(d)
	out := map[string]bool{}
	var walk func(n string)
	walk = func(n string) {
		for _, dep := range rev[n] {
			if !out[dep] {
				out[dep] = true
				walk(dep)
			}
		}
	}
	walk(name)
	return out
}
