package graph

import (
	"reflect"
	"slices"
	"sort"
	"testing"
)

var deps = DepMap{
	Names: []string{"db", "cache", "api", "worker", "web"},
	Deps:  map[string][]string{"db": nil, "cache": nil, "api": {"db", "cache"}, "worker": {"db"}, "web": {"api"}},
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestTopoOrderPutsDependenciesFirst(t *testing.T) {
	order := TopoOrder(deps, nil)
	for n, ds := range deps.Deps {
		for _, d := range ds {
			if slices.Index(order, d) > slices.Index(order, n) {
				t.Errorf("%s must come before %s in %v", d, n, order)
			}
		}
	}
}

func TestLevels(t *testing.T) {
	want := map[string]int{"db": 0, "cache": 0, "api": 1, "worker": 1, "web": 2}
	if got := Levels(deps); !reflect.DeepEqual(got, want) {
		t.Fatalf("levels = %v, want %v", got, want)
	}
}

func TestTransitive(t *testing.T) {
	if got := keys(TransitiveDeps(deps, "web")); !reflect.DeepEqual(got, []string{"api", "cache", "db"}) {
		t.Errorf("deps of web = %v", got)
	}
	if got := keys(TransitiveDependents(deps, "db")); !reflect.DeepEqual(got, []string{"api", "web", "worker"}) {
		t.Errorf("dependents of db = %v", got)
	}
}

func TestCycles(t *testing.T) {
	if c := FindCycle(deps); c != nil {
		t.Fatalf("unexpected cycle %v", c)
	}
	cyclic := DepMap{Names: []string{"a", "b", "c"}, Deps: map[string][]string{"a": {"b"}, "b": {"c"}, "c": {"a"}}}
	if got := FindCycle(cyclic); !reflect.DeepEqual(got, []string{"a", "b", "c", "a"}) {
		t.Fatalf("cycle = %v", got)
	}
}
