// Package runner starts, follows and stops the things orbit supervises: processes, docker containers and compose
// services.
package runner

import (
	"regexp"
	"strings"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/core/logs"
	"github.com/JGoterris/orbit/internal/state"
)

// Callbacks are how a runner reports back to its owner.
type Callbacks struct {
	Log func(stream logs.Stream, text string)
	// Exit says the service stopped (on its own or because we stopped it). code is nil when it died by a signal.
	Exit func(code *int, signal string)
}

// Runner is one supervised service.
type Runner interface {
	// Start launches the service. It returns once it is launched (not necessarily ready).
	Start() error
	// Attach re-attaches to an instance that is already running (a container, or a process saved by a previous
	// session). saved is nil when nothing was saved.
	Attach(saved *state.SavedService) (bool, error)
	// Stop stops the service, waiting up to timeoutMs before killing it.
	Stop(timeoutMs int64) error
	// Release stops following the service (logs, exit watchers) without touching it: it keeps running.
	Release()
	// KillSync is a synchronous last-resort cleanup on process exit.
	KillSync()
	Pid() int
	StartTime() int64
	ContainerID() string
}

// New is the runner for the type of svc. project is the name of the orbit project.
func New(svc *config.ServiceConfig, project string, cb Callbacks, files state.ProcFiles) Runner {
	switch svc.Type {
	case config.TypeDocker:
		return newDocker(svc, project, cb)
	case config.TypeCompose:
		return newCompose(svc, cb)
	case config.TypeExternal:
		return &External{}
	}
	return newProcess(svc, cb, files)
}

// External has nothing to run: the supervisor only health-checks an external service.
type External struct{}

func (External) Start() error                             { return nil }
func (External) Attach(*state.SavedService) (bool, error) { return true, nil }
func (External) Stop(int64) error                         { return nil }
func (External) Release()                                 {}
func (External) KillSync()                                {}
func (External) Pid() int                                 { return 0 }
func (External) StartTime() int64                         { return 0 }
func (External) ContainerID() string                      { return "" }

// SplitArgs is a tiny shell-words splitter (quotes and backslashes), for docker commands.
func SplitArgs(cmd string) []string {
	var out []string
	var cur strings.Builder
	var quote rune
	has := false
	rs := []rune(cmd)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		switch {
		case quote != 0:
			switch {
			case c == quote:
				quote = 0
			case c == '\\' && quote == '"' && i+1 < len(rs):
				i++
				cur.WriteRune(rs[i])
			default:
				cur.WriteRune(c)
			}
		case c == '\'' || c == '"':
			quote = c
			has = true
		case c == '\\' && i+1 < len(rs):
			i++
			cur.WriteRune(rs[i])
			has = true
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			if cur.Len() > 0 || has {
				out = append(out, cur.String())
			}
			cur.Reset()
			has = false
		default:
			cur.WriteRune(c)
		}
	}
	if cur.Len() > 0 || has {
		out = append(out, cur.String())
	}
	return out
}

var containerUnsafe = regexp.MustCompile(`[^a-zA-Z0-9_.-]`)

// ContainerName is the name of the container orbit gives a docker service.
func ContainerName(project, service string) string {
	return strings.ToLower(containerUnsafe.ReplaceAllString("orbit-"+project+"-"+service, "-"))
}
