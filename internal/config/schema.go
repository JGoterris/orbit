// Package config reads orbit.yaml (and the docker-compose files it imports) into an OrbitConfig.
package config

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/JGoterris/orbit/internal/graph"
)

// ServiceType: `external` is something orbit does not run (a SaaS API, a managed database): it is only health-checked.
type ServiceType string

const (
	TypeProcess  ServiceType = "process"
	TypeDocker   ServiceType = "docker"
	TypeCompose  ServiceType = "compose"
	TypeExternal ServiceType = "external"
)

type RestartPolicy string

const (
	RestartNo        RestartPolicy = "no"
	RestartOnFailure RestartPolicy = "on-failure"
	RestartAlways    RestartPolicy = "always"
)

// HealthCheck: exactly one of Http, Tcp, Cmd or Container is set. Interval and Timeout are in milliseconds.
type HealthCheck struct {
	// Http is a URL that must answer with a status < 500
	Http string `json:"http,omitempty"`
	// Tcp is host:port (or just port) that must accept TCP connections
	Tcp string `json:"tcp,omitempty"`
	// Cmd is a shell command that must exit 0
	Cmd string `json:"cmd,omitempty"`
	// Container uses the container's own Docker HEALTHCHECK status
	Container bool  `json:"container,omitempty"`
	Interval  int64 `json:"interval"`
	Timeout   int64 `json:"timeout"`
}

// ReadyWhen is an extra readiness condition read from the service's output.
type ReadyWhen struct {
	// Log is a substring, or `/regex/flags`, that a stdout/stderr line must match
	Log string `json:"log"`
}

type WatchConfig struct {
	// Paths are globs relative to the service's cwd; a change to a matching file restarts the service
	Paths  []string `json:"paths"`
	Ignore []string `json:"ignore"`
	// Debounce is the ms of quiet needed after the last change before restarting
	Debounce int64 `json:"debounce"`
	// Cooldown is the minimum ms between two automatic restarts; changes in between are batched
	Cooldown int64 `json:"cooldown"`
}

type Hook struct {
	Cmd string `json:"cmd"`
	// Timeout in ms before the hook is killed and counts as failed
	Timeout int64 `json:"timeout"`
}

type Hooks struct {
	PreStart  []Hook `json:"preStart"`
	PostStart []Hook `json:"postStart"`
	PostStop  []Hook `json:"postStop"`
}

func (h *Hooks) empty() bool { return len(h.PreStart)+len(h.PostStart)+len(h.PostStop) == 0 }

// EnvFileRef is an `env_file` entry; a missing required file is an error, optional ones are skipped.
type EnvFileRef struct {
	Path     string `json:"path"`
	Required bool   `json:"required"`
}

// ServiceConfig is one service. The field order is the order `orbit` reports changed fields in, and the json names
// are the ones of the TS implementation. Zero values mean "not set" (port 0, memLimit 0, ""...).
type ServiceConfig struct {
	Name        string      `json:"name"`
	Type        ServiceType `json:"type"`
	Description string      `json:"description,omitempty"`
	Cmd         string      `json:"cmd,omitempty"`
	// Shell runs cmd, hooks, health.cmd and console (default: sh on POSIX, cmd.exe on Windows)
	Shell string `json:"shell,omitempty"`
	// Console is the interactive command for the `i` console (psql, rails console...)
	Console string `json:"console,omitempty"`
	Cwd     string `json:"cwd"`
	// Env are the inline variables; they override the ones read from EnvFiles
	Env map[string]string `json:"env"`
	// EnvFiles are the .env files read (in order) every time the service starts
	EnvFiles  []EnvFileRef `json:"envFiles"`
	DependsOn []string     `json:"dependsOn"`
	// Port is the main host port, used for the port-in-use check, default health and "open"
	Port int    `json:"port,omitempty"`
	URL  string `json:"url,omitempty"`
	// Health is nil when there is no health check
	Health *HealthCheck `json:"health,omitempty"`
	// ReadyWhen: the service is not ready until its output matches (and Health, if any, passes)
	ReadyWhen *ReadyWhen `json:"readyWhen,omitempty"`
	// Oneshot is a task that runs to completion (build, migration): ready once it exits 0
	Oneshot bool         `json:"oneshot,omitempty"`
	Watch   *WatchConfig `json:"watch,omitempty"`
	// Hooks is nil when the service has none
	Hooks   *Hooks        `json:"hooks,omitempty"`
	Restart RestartPolicy `json:"restart"`
	// StartTimeout is the ms to wait for a service to become ready before giving up
	StartTimeout int64 `json:"startTimeout"`
	// StopTimeout is the ms to wait after SIGTERM before SIGKILL
	StopTimeout int64 `json:"stopTimeout"`
	Autostart   bool  `json:"autostart"`
	// MemLimit is the memory (bytes) above which orbit alerts; it does not enforce it
	MemLimit int64 `json:"memLimit,omitempty"`
	// LeakDetection warns about sustained memory growth (default on)
	LeakDetection bool `json:"leakDetection"`
	// docker
	Image      string   `json:"image,omitempty"`
	Ports      []string `json:"ports"`
	Volumes    []string `json:"volumes"`
	DockerArgs []string `json:"dockerArgs"`
	// compose
	ComposeFile    string `json:"composeFile,omitempty"`
	ComposeProject string `json:"composeProject,omitempty"`
	// ComposeHash is a hash of the service's block in the compose file, so a change orbit does not parse (build,
	// command…) still shows up in a config diff
	ComposeHash    string `json:"composeHash,omitempty"`
	ComposeService string `json:"composeService,omitempty"`
}

// OrbitConfig is a project. Order and GroupOrder keep the declaration order of services and groups.
type OrbitConfig struct {
	Name string `json:"name"`
	Root string `json:"root"`
	File string `json:"file,omitempty"`
	// LocalFile is the orbit.local.yaml merged over File, when there is one
	LocalFile  string                    `json:"localFile,omitempty"`
	Services   map[string]*ServiceConfig `json:"services"`
	Order      []string                  `json:"order"`
	Groups     map[string][]string       `json:"groups"`
	GroupOrder []string                  `json:"groupOrder"`
}

// DepMap is the dependency graph of the services, in declaration order.
func (c *OrbitConfig) DepMap() graph.DepMap {
	d := graph.DepMap{Names: c.Order, Deps: map[string][]string{}}
	for _, n := range c.Order {
		d.Deps[n] = c.Services[n].DependsOn
	}
	return d
}

// ------------------------------------------------------------------ scalar parsers

var durationRe = regexp.MustCompile(`^(\d+(?:\.\d+)?)\s*(ms|s|m|h)?$`)

// parseDuration reads `2s`, `150ms`, `1m` or a bare number of milliseconds.
func parseDuration(v any, path string, fallback int64) int64 {
	switch x := v.(type) {
	case nil:
		return fallback
	case int64:
		return x
	case float64:
		return int64(math.Round(x))
	case string:
		m := durationRe.FindStringSubmatch(strings.TrimSpace(x))
		if m == nil {
			fail(fmt.Sprintf("invalid duration %s (use e.g. 500ms, 2s, 1m)", jsonOf(x)), path)
		}
		n, _ := strconv.ParseFloat(m[1], 64)
		mult := map[string]float64{"": 1, "ms": 1, "s": 1000, "m": 60_000, "h": 3_600_000}[m[2]]
		return int64(math.Round(n * mult))
	}
	fail(fmt.Sprintf("expected a duration like \"2s\", got %s", jsonOf(v)), path)
	return 0
}

// ParseDuration is parseDuration for callers outside the package (`--since`, tests).
func ParseDuration(s string) (ms int64, err error) {
	defer recoverConfig(&err)
	return parseDuration(s, "", 0), nil
}

var memUnits = map[string]float64{"b": 1, "k": 1024, "kb": 1024, "m": 1 << 20, "mb": 1 << 20, "g": 1 << 30, "gb": 1 << 30}
var memRe = regexp.MustCompile(`(?i)^(\d+(?:\.\d+)?)\s*([a-z]*)$`)

// ParseMemSize reads a memory size as docker / compose write it (`1G`, `512m`, `1.5gb`; binary units).
// ok is false when it is not valid or not positive.
func ParseMemSize(s string) (int64, bool) {
	m := memRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, false
	}
	unit := strings.ToLower(m[2])
	if unit == "" {
		unit = "b"
	}
	mult, ok := memUnits[unit]
	if !ok {
		return 0, false
	}
	n, _ := strconv.ParseFloat(m[1], 64)
	n = math.Round(n * mult)
	return int64(n), n > 0
}

// asMemSize reads `mem_limit` / `deploy.resources.limits.memory`: "1G", "512m", or bytes. 0 means not set.
func asMemSize(v any, path string) int64 {
	switch x := v.(type) {
	case nil:
		return 0
	case int64:
		if x > 0 {
			return x
		}
	case float64:
		if x > 0 && !math.IsInf(x, 0) && !math.IsNaN(x) {
			return int64(math.Round(x))
		}
	case string:
		if n, ok := ParseMemSize(x); ok {
			return n
		}
	default:
		fail(fmt.Sprintf("expected a size like \"1G\", got %s", typeName(v)), path)
	}
	fail(fmt.Sprintf("invalid memory size %s (use e.g. 512m, 1G)", jsonOf(v)), path)
	return 0
}

// hostPortOf: "8080:80" -> 8080, "127.0.0.1:5432:5432" -> 5432, "3000" -> 3000; 0 when there is none.
func hostPortOf(mapping string) int {
	withoutProto, _, _ := strings.Cut(mapping, "/")
	parts := strings.Split(withoutProto, ":")
	host := parts[0]
	if len(parts) >= 2 {
		host = parts[len(parts)-2]
	}
	n, err := strconv.Atoi(host)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// HostPortOf is hostPortOf for callers outside the package.
func HostPortOf(mapping string) int { return hostPortOf(mapping) }

var logPatternRe = regexp.MustCompile(`(?s)^/(.+)/([a-z]*)$`)

// CompileLogPattern: `Listening on` matches as a substring; `/listening on \d+/i` as a regular expression
// (Go's RE2 syntax: no lookaround or backreferences). Flags i, m and s are honoured; g, y and u are ignored.
func CompileLogPattern(src string) (func(line string) bool, error) {
	m := logPatternRe.FindStringSubmatch(src)
	if m == nil {
		return func(line string) bool { return strings.Contains(line, src) }, nil
	}
	flags := ""
	for _, f := range m[2] {
		switch f {
		case 'i', 'm', 's':
			if !strings.ContainsRune(flags, f) {
				flags += string(f)
			}
		case 'g', 'y', 'u', 'd':
		default:
			return nil, fmt.Errorf("invalid flag %q", f)
		}
	}
	pattern := m[1]
	if flags != "" {
		pattern = "(?" + flags + ")" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	return re.MatchString, nil
}
