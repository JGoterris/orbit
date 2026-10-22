package runner

import (
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/core/logs"
	"github.com/JGoterris/orbit/internal/platform"
	"github.com/JGoterris/orbit/internal/proc"
	"github.com/JGoterris/orbit/internal/state"
)

// containerRunner is the shared plumbing of docker and compose services: follow logs and wait for the exit code of
// a container id.
type containerRunner struct {
	svc *config.ServiceConfig
	cb  Callbacks

	mu          sync.Mutex
	containerID string
	followers   []*exec.Cmd
	generation  int
}

func (c *containerRunner) Pid() int         { return 0 }
func (c *containerRunner) StartTime() int64 { return 0 }

func (c *containerRunner) ContainerID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.containerID
}

// runStreaming runs a launcher command (e.g. an image pull) streaming its stderr into the logs; it returns its
// exit code and stdout.
func (c *containerRunner) runStreaming(argv []string, dir string) (int, string, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	stdout, err1 := cmd.StdoutPipe()
	stderr, err2 := cmd.StderrPipe()
	if err := firstErr(err1, err2, cmd.Start()); err != nil {
		return 127, "", err
	}
	var out strings.Builder
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); logs.PipeLines(stdout, func(l string) { out.WriteString(l + "\n") }) }()
	go func() { defer wg.Done(); logs.PipeLines(stderr, func(l string) { c.cb.Log(logs.System, l) }) }()
	wg.Wait()
	err := cmd.Wait()
	code := 0
	if err != nil {
		code = 1
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
	}
	return code, out.String(), nil
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// follow streams the container's logs and watches for it to exit.
func (c *containerRunner) follow(id string, tail int) {
	c.detach()
	c.mu.Lock()
	c.generation++
	gen := c.generation
	c.containerID = id
	c.mu.Unlock()

	// detached: a ctrl+c in the terminal must not kill our watchers before we stop the container
	logsCmd := exec.Command("docker", "logs", "-f", "--tail", strconv.Itoa(tail), id)
	platform.SetDetached(logsCmd)
	stdout, e1 := logsCmd.StdoutPipe()
	stderr, e2 := logsCmd.StderrPipe()
	waitCmd := exec.Command("docker", "wait", id)
	platform.SetDetached(waitCmd)
	waitOut, e3 := waitCmd.StdoutPipe()
	if err := firstErr(e1, e2, e3, logsCmd.Start(), waitCmd.Start()); err != nil {
		c.cb.Log(logs.System, "cannot follow the container: "+err.Error())
		return
	}
	c.mu.Lock()
	c.followers = []*exec.Cmd{logsCmd, waitCmd}
	c.mu.Unlock()

	go logs.PipeLines(stdout, func(l string) { c.cb.Log(logs.Stdout, l) })
	go logs.PipeLines(stderr, func(l string) { c.cb.Log(logs.Stderr, l) })
	go func() {
		data, _ := io.ReadAll(waitOut)
		waitCmd.Wait()
		logsCmd.Wait()
		c.mu.Lock()
		stale := gen != c.generation
		c.mu.Unlock()
		if stale {
			return // detached on purpose
		}
		code, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			// the watcher died, not necessarily the container
			res := proc.Exec([]string{"docker", "inspect", "-f", "{{.State.Running}} {{.State.ExitCode}}", id}, proc.Options{Timeout: 5 * time.Second})
			running, exitCode, _ := strings.Cut(strings.TrimSpace(res.Stdout), " ")
			c.mu.Lock()
			stale = gen != c.generation
			c.mu.Unlock()
			if stale {
				return
			}
			if running == "true" {
				c.follow(id, 0)
				return
			}
			if n, err := strconv.Atoi(exitCode); err == nil {
				c.cb.Exit(&n, "")
			} else {
				c.cb.Exit(nil, "")
			}
			return
		}
		c.cb.Exit(&code, "")
	}()
}

func (c *containerRunner) detach() {
	c.mu.Lock()
	c.generation++
	followers := c.followers
	c.followers = nil
	c.mu.Unlock()
	for _, f := range followers {
		if f.Process != nil {
			f.Process.Kill()
		}
	}
}

func (c *containerRunner) KillSync() { c.detach() }
func (c *containerRunner) Release()  { c.detach() }

// RemoveLeftoverContainer removes the container of a service no longer in orbit.yaml, if orbit labelled it for
// that service.
func RemoveLeftoverContainer(project, service string) bool {
	name := ContainerName(project, service)
	res := proc.Exec([]string{"docker", "inspect", "-f", `{{index .Config.Labels "orbit.service"}}`, name}, proc.Options{Timeout: 5 * time.Second})
	if res.Code != 0 || strings.TrimSpace(res.Stdout) != service {
		return false
	}
	return proc.Exec([]string{"docker", "rm", "-f", name}, proc.Options{Timeout: 15 * time.Second}).Code == 0
}

// DockerRunner runs a `docker run` container.
type DockerRunner struct {
	containerRunner
	name string
}

func newDocker(svc *config.ServiceConfig, project string, cb Callbacks) *DockerRunner {
	return &DockerRunner{containerRunner: containerRunner{svc: svc, cb: cb}, name: ContainerName(project, svc.Name)}
}

func (r *DockerRunner) Attach(*state.SavedService) (bool, error) {
	res := proc.Exec([]string{"docker", "inspect", "-f", "{{.State.Running}} {{.Id}}", r.name}, proc.Options{Timeout: 5 * time.Second})
	running, id, _ := strings.Cut(strings.TrimSpace(res.Stdout), " ")
	if res.Code != 0 || running != "true" || id == "" {
		return false, nil
	}
	r.follow(id, 50)
	return true, nil
}

func (r *DockerRunner) Start() error {
	proc.Exec([]string{"docker", "rm", "-f", r.name}, proc.Options{Timeout: 15 * time.Second})
	argv := []string{"docker", "run", "-d", "--name", r.name, "--label", "orbit.service=" + r.svc.Name}
	for _, p := range r.svc.Ports {
		argv = append(argv, "-p", p)
	}
	for k, v := range r.svc.Env {
		argv = append(argv, "-e", k+"="+v)
	}
	for _, v := range r.svc.Volumes {
		argv = append(argv, "-v", v)
	}
	argv = append(argv, r.svc.DockerArgs...)
	argv = append(argv, r.svc.Image)
	if r.svc.Cmd != "" {
		argv = append(argv, SplitArgs(r.svc.Cmd)...)
	}
	r.cb.Log(logs.System, "$ "+strings.Join(argv, " "))
	code, stdout, err := r.runStreaming(argv, r.svc.Cwd)
	if err != nil {
		return fmt.Errorf("docker run failed: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	id := strings.TrimSpace(lines[len(lines)-1])
	if code != 0 || id == "" {
		return fmt.Errorf("docker run failed (exit %d)", code)
	}
	r.follow(id, 200)
	return nil
}

func (r *DockerRunner) Stop(timeoutMs int64) error {
	target := r.ContainerID()
	if target == "" {
		target = r.name
	}
	secs := strconv.FormatInt((timeoutMs+999)/1000, 10)
	proc.Exec([]string{"docker", "stop", "-t", secs, target}, proc.Options{Timeout: time.Duration(timeoutMs+10_000) * time.Millisecond})
	proc.Exec([]string{"docker", "rm", "-f", target}, proc.Options{Timeout: 10 * time.Second})
	return nil
}

// ComposeRunner runs one service of a compose file.
type ComposeRunner struct{ containerRunner }

func newCompose(svc *config.ServiceConfig, cb Callbacks) *ComposeRunner {
	return &ComposeRunner{containerRunner{svc: svc, cb: cb}}
}

func (r *ComposeRunner) base() []string {
	argv := []string{"docker", "compose", "-f", r.svc.ComposeFile}
	if r.svc.ComposeProject != "" {
		argv = append(argv, "-p", r.svc.ComposeProject)
	}
	return argv
}

// currentID is the service's container, also when it already exited.
func (r *ComposeRunner) currentID() string {
	res := proc.Exec(append(r.base(), "ps", "-a", "-q", r.svc.ComposeService), proc.Options{Timeout: 10 * time.Second})
	if res.Code != 0 {
		return ""
	}
	first, _, _ := strings.Cut(strings.TrimSpace(res.Stdout), "\n")
	return strings.TrimSpace(first)
}

func (r *ComposeRunner) Attach(*state.SavedService) (bool, error) {
	id := r.currentID()
	if id == "" {
		return false, nil
	}
	res := proc.Exec([]string{"docker", "inspect", "-f", "{{.State.Running}}", id}, proc.Options{Timeout: 5 * time.Second})
	if strings.TrimSpace(res.Stdout) != "true" {
		return false, nil
	}
	r.follow(id, 50)
	return true, nil
}

func (r *ComposeRunner) Start() error {
	argv := append(r.base(), "up", "-d", "--no-deps", r.svc.ComposeService)
	r.cb.Log(logs.System, "$ "+strings.Join(argv, " "))
	code, _, err := r.runStreaming(argv, r.svc.Cwd)
	if err != nil {
		return fmt.Errorf("docker compose up failed: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("docker compose up failed (exit %d)", code)
	}
	id := r.currentID()
	if id == "" {
		return fmt.Errorf("container not found after docker compose up")
	}
	r.follow(id, 200)
	return nil
}

func (r *ComposeRunner) Stop(timeoutMs int64) error {
	argv := append(r.base(), "stop", "-t", strconv.FormatInt((timeoutMs+999)/1000, 10), r.svc.ComposeService)
	proc.Exec(argv, proc.Options{Dir: r.svc.Cwd, Timeout: time.Duration(timeoutMs+15_000) * time.Millisecond})
	return nil
}
