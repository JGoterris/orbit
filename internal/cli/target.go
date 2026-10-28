package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/ipc"
	"github.com/JGoterris/orbit/internal/projects"
)

// Target is the project a command acts on, as `-p` names it.
type Target struct {
	Root string
	// Name is known when the project is running or remembered
	Name string
	// Running is the config of the orbit running it, if any
	Running *config.OrbitConfig
}

// ResolveProject finds what `-p <query>` names: an orbit that is running (by name, path or name fragment), then a
// remembered project, then any existing folder.
func ResolveProject(query string) (Target, error) {
	q := strings.TrimSpace(query)
	var live []ipc.RunningOrbit
	for _, o := range ipc.Running() {
		if o.Config != nil {
			live = append(live, o)
		}
	}
	path := projects.ExpandPath(q, "")
	for _, match := range []func(c *config.OrbitConfig) bool{
		func(c *config.OrbitConfig) bool { return c.Name == q },
		func(c *config.OrbitConfig) bool { return c.Root == path },
		func(c *config.OrbitConfig) bool { return strings.Contains(strings.ToLower(c.Name), strings.ToLower(q)) },
	} {
		var hits []*config.OrbitConfig
		for _, o := range live {
			if match(o.Config) {
				hits = append(hits, o.Config)
			}
		}
		switch len(hits) {
		case 0:
		case 1:
			return Target{Root: hits[0].Root, Name: hits[0].Name, Running: hits[0]}, nil
		default:
			paths := make([]string, len(hits))
			for i, c := range hits {
				paths[i] = c.Root
			}
			return Target{}, fmt.Errorf("several running projects match %q: %s (use the path)", q, strings.Join(paths, ", "))
		}
	}
	entry, err := projects.Find(q, projects.Read())
	if err == nil {
		return Target{Root: entry.Path, Name: entry.Name}, nil
	}
	if st, serr := os.Stat(path); serr == nil && st.IsDir() {
		return Target{Root: path}, nil
	}
	return Target{}, fmt.Errorf("no running or remembered project %q (see `orbit projects`)", q)
}

// RunningAt is the config of an orbit running the project at dir or at a folder above it.
func RunningAt(dir string) *config.OrbitConfig {
	dir, _ = filepath.Abs(dir)
	var best *config.OrbitConfig
	for _, o := range ipc.Running() {
		c := o.Config
		if c == nil || (dir != c.Root && !strings.HasPrefix(dir, c.Root+string(filepath.Separator))) {
			continue
		}
		if best == nil || len(c.Root) > len(best.Root) {
			best = c
		}
	}
	return best
}

// PrintRunning lists the orbits that are running, as a hint after a command found no project in the current folder.
func PrintRunning(w *os.File) {
	live := ipc.Running()
	if len(live) == 0 {
		fmt.Fprintln(w, dim("no orbit is running"))
		return
	}
	fmt.Fprintln(w, "running orbits:")
	for _, o := range live {
		kind := "open"
		if o.Daemon {
			kind = "daemon"
		}
		if o.Config == nil {
			fmt.Fprintf(w, "  %s  %s\n", dim(o.StateDir), dim(fmt.Sprintf("pid %d, does not answer", o.Pid)))
			continue
		}
		fmt.Fprintf(w, "  %s  %s  %s\n", o.Config.Name, dim(o.Config.Root), dim(fmt.Sprintf("%s, pid %d", kind, o.Pid)))
	}
	fmt.Fprintln(w, dim("act on one with `-p <name>` (e.g. `orbit down -p <name>`), or `orbit down --all`"))
}

// RunDownAll stops every orbit that is running and the leftovers of the remembered projects (`orbit down --all`).
func RunDownAll() int {
	code := 0
	done := map[string]bool{}
	hung := map[int]bool{}
	for _, o := range ipc.Running() {
		if o.Config == nil {
			hung[o.Pid] = true
			errorln(fmt.Sprintf("%s: orbit is open (pid %d) but does not answer: quit it first", o.StateDir, o.Pid))
			code = 1
			continue
		}
		done[o.Config.Root] = true
		fmt.Println(bold(o.Config.Name) + " " + dim(o.Config.Root))
		if RunDown(o.Config) != 0 {
			code = 1
		}
	}
	for _, p := range projects.Read() {
		st := projects.StatusOf(p)
		if done[p.Path] || st.Running == 0 {
			continue
		}
		// an orbit holds it but was not listed as answering (it hangs): asking it to stop would hang as well
		if st.OpenIn != 0 {
			if !hung[st.OpenIn] {
				errorln(fmt.Sprintf("%s: orbit is open (pid %d) but does not answer: quit it first", p.Name, st.OpenIn))
			}
			code = 1
			continue
		}
		fmt.Println(bold(p.Name) + " " + dim(p.Path))
		done[p.Path] = true
		if RunDown(loadForDown(p)) != 0 {
			code = 1
		}
	}
	if len(done) == 0 && code == 0 {
		fmt.Println(dim("nothing was running"))
	}
	return code
}

// loadForDown is the configuration to stop a remembered project with: its orbit.yaml, or only its root and name,
// which is enough to stop what the saved state says is left.
func loadForDown(p projects.Entry) *config.OrbitConfig {
	if cfg, err := config.LoadConfig(config.LoadOptions{Dir: p.Path}); err == nil {
		return cfg
	}
	return &config.OrbitConfig{Root: p.Path, Name: p.Name, Services: map[string]*config.ServiceConfig{}, Groups: map[string][]string{}}
}
