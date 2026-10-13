package platform

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// CheapStartTime: querying the start time is cheap only on Linux; elsewhere it spawns a process, so pollers use
// PidAlive instead.
const CheapStartTime = IsLinux

// ProcStartTime is an opaque start time of a pid, to tell a recycled pid from the process we launched: only compared
// for equality. ok is false when the process is gone or the platform cannot say.
func ProcStartTime(pid int) (t int64, ok bool) {
	switch {
	case IsLinux:
		data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if err != nil {
			return 0, false
		}
		stat := string(data)
		// comm may contain spaces/parens: fields start after the last ')', starttime is field 22 (index 19 from field 3)
		i := strings.LastIndex(stat, ")")
		if i < 0 || i+2 > len(stat) {
			return 0, false
		}
		fields := strings.Split(stat[i+2:], " ")
		if len(fields) <= 19 {
			return 0, false
		}
		n, err := strconv.ParseInt(fields[19], 10, 64)
		return n, err == nil
	case IsMac:
		out, err := runOutput([]string{"ps", "-o", "lstart=", "-p", strconv.Itoa(pid)}, 3*time.Second)
		if err != nil {
			return 0, false
		}
		ts, err := time.ParseInLocation("Mon Jan _2 15:04:05 2006", strings.Join(strings.Fields(out), " "), time.Local)
		if err != nil {
			return 0, false
		}
		return ts.UnixMilli(), true
	case IsWindows:
		script := "(Get-Process -Id " + strconv.Itoa(pid) + " -ErrorAction Stop).StartTime.ToFileTimeUtc()"
		out, err := runOutput([]string{"powershell", "-NoProfile", "-NonInteractive", "-Command", script}, 5*time.Second)
		if err != nil {
			return 0, false
		}
		n, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
		return n, err == nil
	}
	return 0, false
}

// runOutput runs argv with LC_ALL=C and returns its stdout; an error when it fails, times out or is missing.
func runOutput(argv []string, timeout time.Duration) (string, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	timer := time.AfterFunc(timeout, func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
	})
	defer timer.Stop()
	out, err := cmd.Output()
	return string(out), err
}
