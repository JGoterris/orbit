package config

import (
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/JGoterris/orbit/internal/graph"
)

var ConfigFilenames = []string{"orbit.yaml", "orbit.yml"}

// FindConfigFile walks up from start looking for orbit.yaml; "" when there is none.
func FindConfigFile(start string) string {
	dir, err := filepath.Abs(start)
	if err != nil {
		return ""
	}
	for {
		for _, name := range ConfigFilenames {
			if p := filepath.Join(dir, name); exists(p) {
				return p
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func parseHealth(raw any, path string, port int) *HealthCheck {
	h := parseHealthCheck(raw, path, port)
	if h != nil {
		validateHealth(h, path)
	}
	return h
}

// validateHealth rejects checks that can never pass or check nothing.
func validateHealth(h *HealthCheck, path string) {
	if h.Interval <= 0 {
		fail("the health check interval must be greater than 0", path+".interval")
	}
	if h.Timeout <= 0 {
		fail("the health check timeout must be greater than 0", path+".timeout")
	}
	switch {
	case h.Http != "":
		u, err := url.Parse(h.Http)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			fail(fmt.Sprintf("invalid health URL %s (use e.g. http://localhost:3000/health)", jsonOf(h.Http)), path)
		}
	case h.Tcp != "" || h.Cmd == "" && !h.Container:
		target := h.Tcp
		if i := strings.LastIndex(target, ":"); i != -1 {
			target = target[i+1:]
		}
		if n, err := strconv.Atoi(target); err != nil || n < 1 || n > 65535 {
			fail(fmt.Sprintf("invalid tcp health target %s (use a port, or host:port)", jsonOf(h.Tcp)), path)
		}
	}
}

func parseHealthCheck(raw any, path string, port int) *HealthCheck {
	switch x := raw.(type) {
	case bool:
		if !x {
			return nil
		}
	case nil:
		if port > 0 {
			return &HealthCheck{Tcp: strconv.Itoa(port), Interval: 1000, Timeout: 1000}
		}
		return nil
	case string:
		// shorthand: "http://..." | "tcp:5432" | "cmd: ..." | "container"
		switch {
		case strings.HasPrefix(x, "http://") || strings.HasPrefix(x, "https://"):
			return &HealthCheck{Http: x, Interval: 2000, Timeout: 2000}
		case x == "container":
			return &HealthCheck{Container: true, Interval: 2000, Timeout: 5000}
		case strings.HasPrefix(x, "tcp:"):
			return &HealthCheck{Tcp: x[4:], Interval: 1000, Timeout: 1000}
		}
		return &HealthCheck{Cmd: x, Interval: 2000, Timeout: 5000}
	}
	rec := asRecord(raw, path)
	h := &HealthCheck{
		Container: rec.Get("container") == true,
		Interval:  parseDuration(rec.Get("interval"), path+".interval", 2000),
		Timeout:   parseDuration(rec.Get("timeout"), path+".timeout", 2000),
	}
	h.Http, _ = asString(rec.Get("http"), path+".http")
	h.Tcp, _ = asString(rec.Get("tcp"), path+".tcp")
	h.Cmd, _ = asString(rec.Get("cmd"), path+".cmd")
	if h.Http == "" && h.Tcp == "" && h.Cmd == "" && !h.Container {
		fail("health needs one of http, tcp, cmd or container", path)
	}
	return h
}

// parseWatch reads `watch: ["src/**"]` or `watch: { paths, ignore, debounce, cooldown }`.
func parseWatch(raw any, path string) *WatchConfig {
	if raw == nil || raw == false {
		return nil
	}
	rec, ok := raw.(*Map)
	if !ok {
		rec = NewMap()
		rec.Set("paths", raw)
	}
	paths := asStringList(rec.Get("paths"), path+".paths")
	if len(paths) == 0 {
		fail("watch needs at least one path pattern", path)
	}
	ignore := orEmpty(asStringList(rec.Get("ignore"), path+".ignore"))
	for _, p := range append(slices.Clone(paths), ignore...) {
		if !doublestar.ValidatePattern(strings.TrimPrefix(p, "./")) {
			fail(fmt.Sprintf("invalid glob %s", jsonOf(p)), path)
		}
	}
	return &WatchConfig{
		Paths:    paths,
		Ignore:   ignore,
		Debounce: parseDuration(rec.Get("debounce"), path+".debounce", 1000),
		Cooldown: parseDuration(rec.Get("cooldown"), path+".cooldown", 10_000),
	}
}

// parseReadyWhen reads `ready_when: { log: "Listening on" }`.
func parseReadyWhen(raw any, path string) *ReadyWhen {
	if raw == nil || raw == false {
		return nil
	}
	log, _ := asString(asRecord(raw, path).Get("log"), path+".log")
	log = strings.TrimSpace(log)
	if log == "" {
		fail("ready_when needs a non-empty `log` pattern", path)
	}
	if _, err := CompileLogPattern(log); err != nil {
		fail(fmt.Sprintf("invalid regex %s: %s", jsonOf(log), err), path+".log")
	}
	return &ReadyWhen{Log: log}
}

// parseHookList reads `"cmd"`, `{ cmd, timeout }`, or a list of either.
func parseHookList(raw any, path string) []Hook {
	if raw == nil || raw == false {
		return nil
	}
	items, isList := raw.([]any)
	if !isList {
		items = []any{raw}
	}
	out := make([]Hook, 0, len(items))
	for i, item := range items {
		at := path
		if isList {
			at = fmt.Sprintf("%s[%d]", path, i)
		}
		rec, ok := item.(*Map)
		if !ok {
			rec = NewMap()
			rec.Set("cmd", item)
		}
		cmd, _ := asString(rec.Get("cmd"), at+".cmd")
		cmd = strings.TrimSpace(cmd)
		if cmd == "" {
			fail("a hook needs a non-empty `cmd`", at)
		}
		out = append(out, Hook{Cmd: cmd, Timeout: parseDuration(rec.Get("timeout"), at+".timeout", 60_000)})
	}
	return out
}

// parseHooks: a phase not mentioned in the yaml is inherited from the compose import; none at all gives nil.
func parseHooks(rec *Map, path string, base *Hooks) *Hooks {
	phase := func(key string, inherited func(*Hooks) []Hook) []Hook {
		if rec.Has(key) {
			return parseHookList(rec.Get(key), path+"."+key)
		}
		if base != nil {
			return inherited(base)
		}
		return nil
	}
	h := &Hooks{
		PreStart:  phase("pre_start", func(b *Hooks) []Hook { return b.PreStart }),
		PostStart: phase("post_start", func(b *Hooks) []Hook { return b.PostStart }),
		PostStop:  phase("post_stop", func(b *Hooks) []Hook { return b.PostStop }),
	}
	if h.empty() {
		return nil
	}
	return h
}

var pluginName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

func parsePluginConfig(name string, raw any) *PluginConfig {
	path := "plugins." + name
	if !pluginName.MatchString(name) {
		fail(fmt.Sprintf("invalid plugin name %q", name), "plugins")
	}
	switch v := raw.(type) {
	case nil:
		return &PluginConfig{Enabled: true}
	case bool:
		return &PluginConfig{Enabled: v}
	case *Map:
		return &PluginConfig{Enabled: true, Settings: plain(v).(map[string]any)}
	}
	fail("expected true, false or the settings of the plugin", path)
	return nil
}

var restartPolicies = []RestartPolicy{RestartNo, RestartOnFailure, RestartAlways}
var serviceTypes = []ServiceType{TypeProcess, TypeDocker, TypeCompose, TypeExternal}

// PluginTypes says which plugin provides each service type that is not one of orbit's own (type → plugin). The
// plugins package sets it; without plugins every type that is not orbit's own is unknown.
var PluginTypes = func() map[string]string { return nil }

// IsCoreType is whether t is one of the service types orbit itself runs.
func IsCoreType(t ServiceType) bool { return slices.Contains(serviceTypes, t) }

// unknownType is the error of a type nobody provides, with the way out when a plugin does.
func unknownType(s string) string {
	names := make([]string, len(serviceTypes))
	for i, t := range serviceTypes {
		names[i] = string(t)
	}
	known := PluginTypes()
	var fromPlugins []string
	for t := range known {
		fromPlugins = append(fromPlugins, t)
	}
	slices.Sort(fromPlugins)
	hint := ""
	if sug := Suggest(s, append(slices.Clone(names), fromPlugins...)); sug != "" {
		hint = fmt.Sprintf(" — did you mean %q?", sug)
	}
	all := strings.Join(append(names, fromPlugins...), ", ")
	return fmt.Sprintf("unknown type %q (expected %s)%s; a plugin can provide more types: orbit plugin ls", s, all, hint)
}

func parseService(name string, raw any, root string, base *ServiceConfig, composeFile string) *ServiceConfig {
	path := "services." + name
	var rec *Map
	if s, ok := raw.(string); ok {
		rec = NewMap()
		rec.Set("cmd", s)
	} else {
		rec = asRecord(raw, path)
	}
	if base == nil {
		base = &ServiceConfig{}
	}
	isBase := base.Name != ""

	var typ ServiceType
	if s, ok := asString(rec.Get("type"), path+".type"); ok {
		typ = ServiceType(s)
		if !IsCoreType(typ) {
			if _, ok := PluginTypes()[s]; !ok {
				fail(unknownType(s), path+".type")
			}
		}
	} else {
		switch {
		case truthy(rec.Get("image")):
			typ = TypeDocker
		case truthy(rec.Get("service")) || base.Type == TypeCompose:
			typ = TypeCompose
		default:
			typ = TypeProcess
		}
	}

	cwd := base.Cwd
	if cwd == "" {
		cwd = root
	}
	if s, ok := asString(rec.Get("cwd"), path+".cwd"); ok && s != "" {
		cwd = s
		if !filepath.IsAbs(cwd) {
			cwd = filepath.Join(root, cwd)
		}
	}

	ports := base.Ports
	if rec.Has("ports") {
		ports = asStringList(rec.Get("ports"), path+".ports")
	}
	port := base.Port
	if rec.Has("port") {
		n := toNumber(rec.Get("port"))
		if math.IsNaN(n) || n != math.Trunc(n) || n <= 0 || n > 65535 {
			fail(fmt.Sprintf("invalid port %s", jsonOf(rec.Get("port"))), path+".port")
		}
		port = int(n)
	} else if port == 0 {
		for _, p := range ports {
			if hp := hostPortOf(p); hp != 0 {
				port = hp
				break
			}
		}
	}

	restart := base.Restart
	if restart == "" {
		restart = RestartNo
	}
	if s, ok := asString(rec.Get("restart"), path+".restart"); ok {
		restart = RestartPolicy(s)
	}
	if !slices.Contains(restartPolicies, restart) {
		fail(fmt.Sprintf("invalid restart %q (expected no, on-failure, always)", string(restart)), path+".restart")
	}

	oneshot := rec.Get("oneshot") == true
	if oneshot && rec.Has("ready_when") {
		fail("a oneshot is ready when it exits 0: `ready_when` has no effect", path+".ready_when")
	}
	hooks := parseHooks(rec, path, base.Hooks)
	if oneshot && hooks != nil && len(hooks.PostStart) > 0 {
		fail("a oneshot has no running phase: use post_stop instead", path+".post_start")
	}

	svc := &ServiceConfig{
		Name:           name,
		Type:           typ,
		Description:    strOr(rec.Get("description"), path+".description", base.Description),
		Cmd:            strOr(rec.First("cmd", "command"), path+".cmd", base.Cmd),
		Console:        strOr(rec.Get("console"), path+".console", base.Console),
		Shell:          strOr(rec.Get("shell"), path+".shell", base.Shell),
		Cwd:            cwd,
		Env:            map[string]string{},
		EnvFiles:       parseEnvFileRefs(rec.Get("env_file"), path+".env_file", root),
		Port:           port,
		URL:            strOr(rec.Get("url"), path+".url", base.URL),
		ReadyWhen:      base.ReadyWhen,
		Oneshot:        oneshot,
		Watch:          base.Watch,
		Hooks:          hooks,
		Restart:        restart,
		StartTimeout:   parseDuration(rec.Get("start_timeout"), path+".start_timeout", orInt(base.StartTimeout, 60_000)),
		StopTimeout:    parseDuration(rec.Get("stop_timeout"), path+".stop_timeout", orInt(base.StopTimeout, 8_000)),
		Autostart:      true,
		MemLimit:       base.MemLimit,
		LeakDetection:  true,
		Image:          strOr(rec.Get("image"), path+".image", base.Image),
		Ports:          orEmpty(ports),
		DockerArgs:     orEmpty(asStringList(rec.Get("docker_args"), path+".docker_args")),
		ComposeFile:    base.ComposeFile,
		ComposeProject: base.ComposeProject,
		ComposeHash:    base.ComposeHash,
	}
	if rec.Has("with") {
		if IsCoreType(typ) {
			fail("`with` is for the services of a type a plugin provides", path+".with")
		}
		if !isMapping(rec.Get("with")) {
			fail("`with` must be a mapping", path+".with")
		}
		svc.With = plain(rec.Get("with")).(map[string]any)
	}
	if svc.ComposeFile == "" {
		svc.ComposeFile = composeFile
	}
	svc.ComposeService = strOr(rec.Get("service"), path+".service", base.ComposeService)
	if svc.ComposeService == "" {
		svc.ComposeService = name
	}
	for k, v := range base.Env {
		svc.Env[k] = v
	}
	for k, v := range asEnv(rec.First("env", "environment"), path+".env") {
		svc.Env[k] = v
	}
	deps := slices.Clone(base.DependsOn)
	for _, d := range asStringList(rec.First("depends_on", "dependsOn"), path+".depends_on") {
		if !slices.Contains(deps, d) {
			deps = append(deps, d)
		}
	}
	svc.DependsOn = orEmpty(deps)

	switch {
	case oneshot:
		svc.Health = nil
	case rec.Has("health") || !isBase:
		healthPort := port
		if typ == TypeExternal {
			healthPort = 0
		}
		svc.Health = parseHealth(rec.Get("health"), path+".health", healthPort)
	default:
		svc.Health = base.Health
	}
	if rec.Has("ready_when") {
		svc.ReadyWhen = parseReadyWhen(rec.Get("ready_when"), path+".ready_when")
	}
	if rec.Has("watch") {
		svc.Watch = parseWatch(rec.Get("watch"), path+".watch")
	}
	if rec.Has("autostart") {
		svc.Autostart = rec.Get("autostart") != false
	} else if isBase {
		svc.Autostart = base.Autostart
	}
	if rec.Has("mem_limit") {
		svc.MemLimit = asMemSize(rec.Get("mem_limit"), path+".mem_limit")
	}
	if rec.Has("leak_detection") {
		svc.LeakDetection = rec.Get("leak_detection") != false
	} else if isBase {
		svc.LeakDetection = base.LeakDetection
	}
	svc.Volumes = []string{}
	for _, v := range asStringList(rec.Get("volumes"), path+".volumes") {
		// resolve relative host paths of bind mounts against the project root
		parts := strings.Split(v, ":")
		if h := parts[0]; strings.HasPrefix(h, "./") || strings.HasPrefix(h, "../") {
			parts[0] = filepath.Join(root, h)
			v = strings.Join(parts, ":")
		}
		svc.Volumes = append(svc.Volumes, v)
	}

	if svc.Type == TypeExternal {
		for _, key := range []string{"cmd", "command", "image", "watch", "oneshot", "ready_when", "pre_start", "post_start", "post_stop", "depends_on", "dependsOn"} {
			if rec.Has(key) {
				fail(fmt.Sprintf("an external service is not run by orbit: `%s` has no effect", key), path+"."+key)
			}
		}
		if svc.Health == nil {
			fail("an external service needs `health` (it is only monitored)", path+".health")
		}
	}
	if svc.Type == TypeProcess && svc.Cmd == "" {
		fail("a process service needs `cmd`", path)
	}
	if svc.Type == TypeDocker && svc.Image == "" {
		fail("a docker service needs `image`", path)
	}
	if svc.Type == TypeCompose && len(svc.EnvFiles) > 0 {
		fail("env_file has no effect on compose services: set it in the compose file", path+".env_file")
	}
	if svc.Type == TypeCompose && svc.ComposeFile == "" {
		fail("a compose service needs a compose file (set top-level `compose:`)", path)
	}
	return svc
}

func orInt(v, fallback int64) int64 {
	if v == 0 {
		return fallback
	}
	return v
}

// LoadOptions says where to look for the configuration.
type LoadOptions struct {
	// Dir is the directory to start searching from (default: the working directory)
	Dir string
	// File is an explicit config file
	File string
	// AllowEmpty makes a directory with neither orbit.yaml nor compose a project with no services instead of an error
	AllowEmpty bool
	// Env feeds `${VAR}` in the yaml; nil means the process environment
	Env map[string]string
	// Profiles are the profiles to activate when ProfilesSet is true (empty: none). Otherwise ORBIT_PROFILES, the
	// selection saved for the project and `default_profiles` decide, in that order.
	Profiles    []string
	ProfilesSet bool
	// SavedProfiles gives the selection last saved for the project with this root and name, if any
	SavedProfiles func(root, name string) ([]string, bool)
	// errs collects errors instead of stopping at the first one (validate)
	errs *[]error
}

// check runs fn; when collecting, a config error is recorded and check returns false.
func (o LoadOptions) check(fn func()) (ok bool) {
	if o.errs == nil {
		fn()
		return true
	}
	defer func() {
		if r := recover(); r != nil {
			ce, isConfig := r.(*ConfigError)
			if !isConfig {
				panic(r)
			}
			*o.errs = append(*o.errs, ce)
			ok = false
		}
	}()
	fn()
	return true
}

func graphCycle(c *OrbitConfig) []string { return graph.FindCycle(c.DepMap()) }

// defaultProjectName is the name of a project that does not set one: its directory.
func defaultProjectName(root string) string {
	name := filepath.Base(root)
	if name == "" || name == string(filepath.Separator) {
		return "orbit"
	}
	return name
}

// validateGraph checks the dependencies. removed (service → what removed it) only improves the message.
func validateGraph(c *OrbitConfig, removed map[string]string, check func(func()) bool) {
	unknown := func(dep string) string {
		msg := fmt.Sprintf("depends on unknown service %q", dep)
		if by := removed[dep]; by != "" {
			msg += " (removed by " + by + ")"
		}
		return msg
	}
	for _, n := range c.Order {
		svc := c.Services[n]
		for _, dep := range svc.DependsOn {
			check(func() {
				if c.Services[dep] == nil {
					fail(unknown(dep), "services."+svc.Name+".depends_on")
				}
				if dep == svc.Name {
					fail("a service cannot depend on itself", "services."+svc.Name+".depends_on")
				}
			})
		}
	}
	for _, n := range c.TaskOrder {
		for _, dep := range c.Tasks[n].DependsOn {
			check(func() {
				if c.Services[dep] == nil {
					fail(unknown(dep), "tasks."+n+".depends_on")
				}
			})
		}
	}
	check(func() {
		// unknown deps were already reported
		for _, n := range c.Order {
			for _, dep := range c.Services[n].DependsOn {
				if c.Services[dep] == nil {
					return
				}
			}
		}
		if cycle := graphCycle(c); cycle != nil {
			fail("dependency cycle: "+strings.Join(cycle, " → "), "")
		}
	})
}

// LoadConfig reads orbit.yaml (searched upwards from Dir), importing the compose file next to it, or a plain
// docker-compose project when there is no orbit.yaml. Errors in the file are *ConfigError.
func LoadConfig(opts LoadOptions) (cfg *OrbitConfig, err error) {
	defer recoverConfig(&err)
	dir := opts.Dir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	startDir, _ := filepath.Abs(dir)
	file := ""
	if opts.File != "" {
		file, _ = filepath.Abs(opts.File)
	} else {
		file = FindConfigFile(startDir)
	}

	if file == "" {
		// No orbit.yaml: fall back to a plain docker-compose project.
		compose := FindComposeFile(startDir)
		if compose == "" {
			if opts.AllowEmpty {
				name := filepath.Base(startDir)
				if name == "" || name == string(filepath.Separator) {
					name = "orbit"
				}
				return &OrbitConfig{Name: name, Root: startDir, Services: map[string]*ServiceConfig{}, Groups: map[string][]string{}, Tasks: map[string]*TaskConfig{}}, nil
			}
			fail(fmt.Sprintf("no orbit.yaml or docker-compose.yml found in %s (run `orbit init`)", startDir), "")
		}
		text, rerr := os.ReadFile(compose)
		if rerr != nil {
			fail(rerr.Error(), compose)
		}
		services, cerr := ParseComposeFile(compose, string(text), "", nil)
		if cerr != nil {
			return nil, cerr
		}
		c := &OrbitConfig{Name: "orbit", Root: startDir, Services: map[string]*ServiceConfig{}, Groups: map[string][]string{}, Tasks: map[string]*TaskConfig{}}
		if len(services) > 0 {
			c.Name = services[0].ComposeProject
		}
		for _, s := range services {
			c.Services[s.Name] = s
			c.Order = append(c.Order, s.Name)
		}
		validateGraph(c, nil, opts.check)
		return c, nil
	}

	if !exists(file) {
		fail("config file not found: "+file, "")
	}
	root := filepath.Dir(file)
	vars := map[string]string{}
	if data, rerr := os.ReadFile(filepath.Join(root, ".env")); rerr == nil {
		for k, v := range ParseDotEnv(string(data)) {
			vars[k] = v
		}
	}
	env := opts.Env
	if env == nil {
		env = environ()
	}
	for k, v := range env {
		vars[k] = v
	}

	// the profiles are taken out before anything is merged or interpolated: an inactive one is never evaluated
	base := readYAMLDoc(file)
	declared := parseProfiles(base)
	projectName, _ := asString(base.Get("name"), "name")
	if projectName == "" {
		projectName = defaultProjectName(root)
	}
	active := activeProfiles(opts, env, root, projectName, declared, base)
	layers := &overlays{doc: mergeDoc(base, NewMap()), removed: map[string]string{}}
	layers.doc.Delete("profiles")
	layers.doc.Delete("default_profiles")
	for _, name := range active {
		layers.lay(declared.bodies[name], "profile "+name)
	}
	local := FindLocalConfigFile(root)
	if local != "" {
		layers.lay(readYAMLDoc(local), filepath.Base(local))
	}
	doc := Interpolate(layers.doc, vars).(*Map)
	globalEnv := asEnv(doc.Get("env"), "env")
	globalEnvFiles := parseEnvFileRefs(doc.Get("env_file"), "env_file", root)

	// compose: <file> | [files] | false. When omitted, a compose file next to orbit.yaml is imported.
	var composeFiles []string
	switch {
	case doc.Get("compose") == false:
	case !doc.Has("compose"):
		if f := FindComposeFile(root); f != "" {
			composeFiles = []string{f}
		}
	default:
		for _, f := range asStringList(doc.Get("compose"), "compose") {
			composeFiles = append(composeFiles, resolvePath(f, root))
		}
	}

	globalShell, hasShell := asString(doc.Get("shell"), "shell")
	if hasShell && strings.TrimSpace(globalShell) == "" {
		fail("shell must not be empty", "shell")
	}

	imported := map[string]*ServiceConfig{}
	var importedOrder []string
	composeProject, _ := asString(doc.Get("compose_project"), "compose_project")
	for _, cf := range composeFiles {
		data, rerr := os.ReadFile(cf)
		if rerr != nil {
			fail("compose file not found: "+cf, "compose")
		}
		svcs, cerr := ParseComposeFile(cf, string(data), composeProject, vars)
		if cerr != nil {
			return nil, cerr
		}
		for _, s := range svcs {
			if _, seen := imported[s.Name]; !seen {
				importedOrder = append(importedOrder, s.Name)
			}
			imported[s.Name] = s
		}
	}

	c := &OrbitConfig{Root: root, File: file, LocalFile: local, Services: map[string]*ServiceConfig{}, Groups: map[string][]string{}, Tasks: map[string]*TaskConfig{},
		Profiles: orEmpty(active), AvailableProfiles: declared.info}
	rawServices := asRecord(doc.Get("services"), "services")
	for _, name := range importedOrder {
		if !rawServices.Has(name) && layers.removed[name] == "" {
			c.Services[name] = imported[name]
			c.Order = append(c.Order, name)
		}
	}
	composeFirst := ""
	if len(composeFiles) > 0 {
		composeFirst = composeFiles[0]
	}
	for _, name := range rawServices.Keys() {
		parsed := opts.check(func() {
			// the CLI cannot address these
			if strings.TrimSpace(name) == "" || strings.HasPrefix(name, "-") {
				fail(fmt.Sprintf("invalid service name %s", jsonOf(name)), "services")
			}
			c.Services[name] = parseService(name, rawServices.Get(name), root, imported[name], composeFirst)
		})
		if !parsed {
			// placeholder, so references to it don't fail too
			c.Services[name] = &ServiceConfig{Name: name, Type: TypeProcess, Env: map[string]string{}, DependsOn: []string{}}
		}
		if !slices.Contains(c.Order, name) {
			c.Order = append(c.Order, name)
		}
	}
	for _, name := range c.Order {
		svc := c.Services[name]
		merged := map[string]string{}
		for k, v := range globalEnv {
			merged[k] = v
		}
		for k, v := range svc.Env {
			merged[k] = v
		}
		svc.Env = merged
		if svc.Shell == "" {
			svc.Shell = globalShell
		}
		// compose services get their environment from docker compose itself
		if svc.Type != TypeCompose {
			svc.EnvFiles = append(slices.Clone(globalEnvFiles), svc.EnvFiles...)
		}
		opts.check(func() {
			if missing := MissingEnvFiles(svc.EnvFiles); len(missing) > 0 {
				fail(fmt.Sprintf("env_file not found: %s (use { path, required: false } for optional files)", strings.Join(missing, ", ")), "services."+svc.Name+".env_file")
			}
		})
		if svc.EnvFiles == nil {
			svc.EnvFiles = []EnvFileRef{}
		}
	}

	groups := asRecord(doc.Get("groups"), "groups")
	for _, g := range groups.Keys() {
		opts.check(func() {
			// ambiguous on the CLI
			if c.Services[g] != nil {
				fail(fmt.Sprintf("a group cannot have the name of a service (%q)", g), "groups."+g)
			}
			members := orEmpty(asStringList(groups.Get(g), "groups."+g))
			for _, m := range members {
				if c.Services[m] == nil {
					fail(fmt.Sprintf("unknown service %q", m), "groups."+g)
				}
			}
			c.Groups[g] = members
			c.GroupOrder = append(c.GroupOrder, g)
		})
	}

	rawTasks := asRecord(doc.Get("tasks"), "tasks")
	for _, name := range rawTasks.Keys() {
		opts.check(func() {
			c.addTask(name, parseTask(name, rawTasks.Get(name), root), globalEnv, globalEnvFiles, globalShell)
		})
	}

	rawPlugins := asRecord(doc.Get("plugins"), "plugins")
	for _, name := range rawPlugins.Keys() {
		opts.check(func() {
			if c.Plugins == nil {
				c.Plugins = map[string]*PluginConfig{}
			}
			c.Plugins[name] = parsePluginConfig(name, rawPlugins.Get(name))
		})
	}

	c.Name, _ = asString(doc.Get("name"), "name")
	if c.Name == "" {
		c.Name = defaultProjectName(root)
	}
	validateGraph(c, layers.removed, opts.check)
	return c, nil
}

// addTask completes a parsed task with the project-wide env, env files and shell.
func (c *OrbitConfig) addTask(name string, t *TaskConfig, globalEnv map[string]string, globalEnvFiles []EnvFileRef, globalShell string) {
	merged := map[string]string{}
	for k, v := range globalEnv {
		merged[k] = v
	}
	for k, v := range t.Env {
		merged[k] = v
	}
	t.Env = merged
	if t.Shell == "" {
		t.Shell = globalShell
	}
	t.EnvFiles = append(slices.Clone(globalEnvFiles), t.EnvFiles...)
	if missing := MissingEnvFiles(t.EnvFiles); len(missing) > 0 {
		fail(fmt.Sprintf("env_file not found: %s (use { path, required: false } for optional files)", strings.Join(missing, ", ")), "tasks."+name+".env_file")
	}
	c.Tasks[name] = t
	c.TaskOrder = append(c.TaskOrder, name)
}
