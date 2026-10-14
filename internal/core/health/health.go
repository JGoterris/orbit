// Package health runs the checks that say whether a service is up.
package health

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/platform"
	"github.com/JGoterris/orbit/internal/proc"
)

// Result is the outcome of one check.
type Result struct {
	OK     bool
	Detail string
}

func ms(v int64) time.Duration { return time.Duration(v) * time.Millisecond }

func parseTCP(target string) (host string, port int) {
	i := strings.LastIndex(target, ":")
	if i == -1 {
		p, _ := strconv.Atoi(target)
		return "127.0.0.1", p
	}
	host = target[:i]
	if host == "" {
		host = "127.0.0.1"
	}
	p, _ := strconv.Atoi(target[i+1:])
	return host, p
}

// Describe is what the check does, in one line.
func Describe(h *config.HealthCheck) string { return h.Describe() }

// DescribeReadiness is everything a service must satisfy to be ready: its health check and/or its log pattern.
func DescribeReadiness(svc *config.ServiceConfig) string {
	var parts []string
	if svc.ReadyWhen != nil {
		parts = append(parts, fmt.Sprintf("log %q", svc.ReadyWhen.Log))
	}
	if svc.Health != nil {
		parts = append(parts, svc.Health.Describe())
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, " + ")
}

// Check runs the health check of a service once. containerID is the container the service runs in, if any.
func Check(h *config.HealthCheck, svc *config.ServiceConfig, containerID string) Result {
	switch {
	case h.Http != "":
		client := &http.Client{
			Timeout:       ms(h.Timeout),
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
		res, err := client.Get(h.Http)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				return Result{Detail: "timeout"}
			}
			if ue, ok := err.(interface{ Unwrap() error }); ok {
				if ne, ok := ue.Unwrap().(net.Error); ok && ne.Timeout() {
					return Result{Detail: "timeout"}
				}
			}
			return Result{Detail: err.Error()}
		}
		res.Body.Close()
		return Result{OK: res.StatusCode < 500, Detail: fmt.Sprintf("HTTP %d", res.StatusCode)}
	case h.Tcp != "":
		host, port := parseTCP(h.Tcp)
		ok := proc.IsPortOpen(port, host, ms(h.Timeout))
		if ok {
			return Result{OK: true, Detail: fmt.Sprintf("port %d open", port)}
		}
		return Result{Detail: fmt.Sprintf("port %d closed", port)}
	case h.Cmd != "":
		argv, verbatim := platform.ShellCommand(h.Cmd, svc.Shell, platform.IsWindows)
		res := proc.Exec(argv, proc.Options{
			Verbatim: verbatim, Dir: svc.Cwd, Env: proc.EnvList(svc.Env), Timeout: ms(h.Timeout),
		})
		return Result{OK: res.Code == 0, Detail: fmt.Sprintf("exit %d", res.Code)}
	case h.Container:
		if containerID == "" {
			return Result{Detail: "no container"}
		}
		res := proc.Exec([]string{"docker", "inspect", "-f",
			"{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}", containerID},
			proc.Options{Timeout: ms(h.Timeout)})
		status := strings.TrimSpace(res.Stdout)
		detail := status
		if detail == "" {
			detail = "unknown"
		}
		return Result{OK: status == "healthy" || status == "running", Detail: detail}
	}
	return Result{OK: true}
}
