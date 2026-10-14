package platform

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// WhoListens is a best-effort description of the process that listens on a TCP port ("node (pid 123)"); "" when
// nobody does or the tools to find out are missing.
func WhoListens(port int) string {
	switch {
	case IsMac:
		out, _ := runOutput([]string{"lsof", "-nP", "-iTCP:" + strconv.Itoa(port), "-sTCP:LISTEN", "-Fpc"}, 3*time.Second)
		pid := regexp.MustCompile(`(?m)^p(\d+)`).FindStringSubmatch(out)
		if pid == nil {
			return ""
		}
		name := "process"
		if m := regexp.MustCompile(`(?m)^c(.+)$`).FindStringSubmatch(out); m != nil {
			name = m[1]
		}
		return fmt.Sprintf("%s (pid %s)", name, pid[1])
	case IsWindows:
		out, _ := runOutput([]string{"netstat", "-ano", "-p", "tcp"}, 5*time.Second)
		re := regexp.MustCompile(fmt.Sprintf(`(?im)^\s*TCP\s+\S+:%d\s+\S+\s+LISTENING\s+(\d+)`, port))
		m := re.FindStringSubmatch(out)
		if m == nil {
			return ""
		}
		task, _ := runOutput([]string{"tasklist", "/FI", "PID eq " + m[1], "/FO", "CSV", "/NH"}, 3*time.Second)
		name := "process"
		if n := regexp.MustCompile(`^"([^"]+)"`).FindStringSubmatch(strings.TrimSpace(task)); n != nil {
			name = n[1]
		}
		return fmt.Sprintf("%s (pid %s)", name, m[1])
	}
	out, _ := runOutput([]string{"ss", "-ltnpH", fmt.Sprintf("sport = :%d", port)}, 2*time.Second)
	if m := regexp.MustCompile(`users:\(\("([^"]+)",pid=(\d+)`).FindStringSubmatch(out); m != nil {
		return fmt.Sprintf("%s (pid %s)", m[1], m[2])
	}
	if strings.TrimSpace(out) != "" {
		return "another process"
	}
	return ""
}
