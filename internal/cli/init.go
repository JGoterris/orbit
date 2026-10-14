package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/JGoterris/orbit/internal/config"
	"gopkg.in/yaml.v3"
)

var skipDirs = map[string]bool{
	"node_modules": true, ".git": true, "dist": true, "build": true, "target": true, ".next": true,
	".venv": true, "venv": true, "vendor": true, "coverage": true,
}

// detected is a service orbit.yaml could run, found by looking at the project's files.
type detected struct {
	name string
	cwd  string
	cmd  string
	port int
}

var explicitPortRe = regexp.MustCompile(`(?:--port[= ]|-p |PORT=)(\d{2,5})`)

func detectPackageJSON(dir string) *detected {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return nil
	}
	var pkg struct {
		Scripts         map[string]string `json:"scripts"`
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return nil
	}
	script := ""
	switch {
	case pkg.Scripts["dev"] != "":
		script = "dev"
	case pkg.Scripts["start"] != "":
		script = "start"
	default:
		return nil
	}
	has := func(name string) bool { return fileExists(filepath.Join(dir, name)) }
	pm := "npm run"
	switch {
	case has("bun.lock") || has("bun.lockb"):
		pm = "bun run"
	case has("pnpm-lock.yaml"):
		pm = "pnpm"
	case has("yarn.lock"):
		pm = "yarn"
	}
	dep := func(name string) bool {
		_, a := pkg.Dependencies[name]
		_, b := pkg.DevDependencies[name]
		return a || b
	}
	port := 0
	if m := explicitPortRe.FindStringSubmatch(pkg.Scripts[script]); m != nil {
		port, _ = strconv.Atoi(m[1])
	} else {
		switch {
		case dep("vite"):
			port = 5173
		case dep("next"), dep("nuxt"):
			port = 3000
		case dep("astro"):
			port = 4321
		case dep("@angular/core"):
			port = 4200
		}
	}
	return &detected{name: filepath.Base(dir), cwd: dir, cmd: pm + " " + script, port: port}
}

func detectOther(dir string) *detected {
	name := filepath.Base(dir)
	switch {
	case fileExists(filepath.Join(dir, "Cargo.toml")):
		return &detected{name: name, cwd: dir, cmd: "cargo run"}
	case fileExists(filepath.Join(dir, "go.mod")):
		return &detected{name: name, cwd: dir, cmd: "go run ."}
	case fileExists(filepath.Join(dir, "manage.py")):
		return &detected{name: name, cwd: dir, cmd: "python manage.py runserver", port: 8000}
	case fileExists(filepath.Join(dir, "Procfile")):
		data, _ := os.ReadFile(filepath.Join(dir, "Procfile"))
		if m := regexp.MustCompile(`(?m)^web:\s*(.+)$`).FindSubmatch(data); m != nil {
			return &detected{name: name, cwd: dir, cmd: string(m[1])}
		}
	}
	return nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func hasChildProjects(dir string) bool {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if skipDirs[e.Name()] || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if !isDir(p) {
			continue
		}
		if fileExists(filepath.Join(p, "package.json")) {
			return true
		}
		subs, _ := os.ReadDir(p)
		for _, s := range subs {
			if fileExists(filepath.Join(p, s.Name(), "package.json")) {
				return true
			}
		}
	}
	return false
}

func scan(root, dir string, depth int, out *[]detected) {
	found := detectPackageJSON(dir)
	if found == nil {
		found = detectOther(dir)
	}
	// a root package.json in a monorepo usually just runs the workspaces
	if found != nil && !(dir == root && fileExists(filepath.Join(dir, "package.json")) && depth == 0 && hasChildProjects(dir)) {
		*out = append(*out, *found)
	}
	if depth >= 2 {
		return
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") || skipDirs[e.Name()] {
			continue
		}
		if p := filepath.Join(dir, e.Name()); isDir(p) {
			scan(root, p, depth+1, out)
		}
	}
}

// yamlMap builds a mapping node that keeps the order of the pairs given.
func yamlMap(pairs ...any) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode}
	for i := 0; i+1 < len(pairs); i += 2 {
		n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: pairs[i].(string)}, toNode(pairs[i+1]))
	}
	return n
}

func toNode(v any) *yaml.Node {
	switch x := v.(type) {
	case *yaml.Node:
		return x
	case int:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(x)}
	case []string:
		n := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
		for _, s := range x {
			n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: s})
		}
		return n
	}
	n := &yaml.Node{}
	_ = n.Encode(v)
	return n
}

var nameCleanRe = regexp.MustCompile(`[^a-z0-9_-]`)
var frontendRe = regexp.MustCompile(`web|front|ui|client|app`)

// RunInit writes an orbit.yaml by scanning the project (`orbit init`).
func RunInit(dir string, force bool) int {
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	target := filepath.Join(dir, "orbit.yaml")
	if fileExists(target) && !force {
		fmt.Fprintln(os.Stderr, red(target+" already exists (use --force to overwrite)"))
		return 1
	}
	var found []detected
	scan(dir, dir, 0, &found)
	compose := config.FindComposeFile(dir)
	var composeServices []*config.ServiceConfig
	if compose != "" {
		text, _ := os.ReadFile(compose)
		svcs, err := config.ParseComposeFile(compose, string(text), "", nil)
		if err != nil {
			fmt.Fprintln(os.Stderr, red(err.Error()))
			return 1
		}
		composeServices = svcs
	}

	used := map[string]bool{}
	var composeNames []string
	for _, s := range composeServices {
		used[s.Name] = true
		composeNames = append(composeNames, s.Name)
	}
	services := yamlMap()
	for _, d := range found {
		name := nameCleanRe.ReplaceAllString(strings.ToLower(d.name), "-")
		for used[name] {
			name += "-app"
		}
		used[name] = true
		entry := yamlMap("cmd", d.cmd)
		if rel, err := filepath.Rel(dir, d.cwd); err == nil && rel != "." {
			entry.Content = append(entry.Content, toNode("cwd"), toNode("./"+filepath.ToSlash(rel)))
		}
		if d.port != 0 {
			entry.Content = append(entry.Content, toNode("port"), toNode(d.port))
		}
		// backends usually need the databases of the compose file
		if len(composeNames) > 0 && !frontendRe.MatchString(name) {
			entry.Content = append(entry.Content, toNode("depends_on"), toNode(composeNames))
		}
		services.Content = append(services.Content, toNode(name), entry)
	}
	if len(services.Content) == 0 {
		services = yamlMap("app", yamlMap("cmd", "echo 'replace me with your dev command'; sleep 3600", "port", 3000, "health", "http://localhost:3000"))
	}
	doc := yamlMap("name", filepath.Base(dir), "services", services)

	header := []string{
		"# yaml-language-server: $schema=https://raw.githubusercontent.com/JGoterris/orbit/main/orbit.schema.json",
		"# orbit.yaml — services managed by orbit",
		"# types: process (cmd), docker (image), compose (imported from docker-compose.yml)",
		"# per service: cmd, cwd, env, port, depends_on, health (http/tcp/cmd), restart (no|on-failure|always)",
	}
	if compose != "" {
		rel, _ := filepath.Rel(dir, compose)
		header = append(header, fmt.Sprintf("# services from %s are imported automatically: %s", rel, strings.Join(composeNames, ", ")))
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		fmt.Fprintln(os.Stderr, red(err.Error()))
		return 1
	}
	enc.Close()
	if err := os.WriteFile(target, []byte(strings.Join(header, "\n")+"\n\n"+buf.String()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, red(err.Error()))
		return 1
	}
	fmt.Println(green("wrote " + target))
	for _, d := range found {
		line := fmt.Sprintf("  %s %s", bold(d.name), dim(d.cmd))
		if d.port != 0 {
			line += dim(fmt.Sprintf(" :%d", d.port))
		}
		fmt.Println(line)
	}
	for _, s := range composeServices {
		line := fmt.Sprintf("  %s %s", bold(s.Name), dim("compose"))
		if s.Port != 0 {
			line += dim(fmt.Sprintf(" :%d", s.Port))
		}
		fmt.Println(line)
	}
	fmt.Println(dim("review depends_on and ports, then run `orbit`"))
	return 0
}
