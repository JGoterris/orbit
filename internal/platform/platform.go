// Package platform holds everything that differs between Linux, macOS and Windows: shells, process trees, pids,
// process metrics, who owns a port and where per-user files go. The rest of orbit stays platform-agnostic.
package platform

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	IsWindows = runtime.GOOS == "windows"
	IsMac     = runtime.GOOS == "darwin"
	IsLinux   = runtime.GOOS == "linux"
)

// ── shells ──────────────────────────────────────────────────────────────────────────────────────

// ShellArgv is the argv that runs cmd through a shell: `sh -c` on POSIX, `cmd.exe /d /s /c` on Windows, or the
// configured `shell`. win says which of the two defaults to use (IsWindows in production).
func ShellArgv(cmd, shell string, win bool) []string {
	if shell != "" {
		name := shell
		if i := strings.LastIndexAny(name, `\/`); i >= 0 {
			name = name[i+1:]
		}
		name = strings.ToLower(strings.TrimSuffix(strings.TrimSuffix(name, ".exe"), ".EXE"))
		switch name {
		case "pwsh", "powershell":
			return []string{shell, "-NoProfile", "-Command", cmd}
		case "cmd":
			return []string{shell, "/d", "/s", "/c", cmd}
		}
		return []string{shell, "-c", cmd}
	}
	if win {
		comspec := os.Getenv("ComSpec")
		if comspec == "" {
			comspec = "cmd.exe"
		}
		return []string{comspec, "/d", "/s", "/c", cmd}
	}
	return []string{"/bin/sh", "-c", cmd}
}

var cmdShellRe = regexp.MustCompile(`(?i)(^|[\\/])cmd(\.exe)?$`)

// ShellCommand is ShellArgv plus whether Windows must get the arguments untouched: `cmd.exe /s /c "<cmd>"` strips the
// outer quotes itself and does not understand the `\"` escaping Go applies to a normal argument, so for cmd the
// quoting is ours (see SetVerbatimArgs).
func ShellCommand(cmd, shell string, win bool) (argv []string, verbatim bool) {
	argv = ShellArgv(cmd, shell, win)
	if !win || !cmdShellRe.MatchString(argv[0]) || len(argv) < 5 || argv[3] != "/c" {
		return argv, false
	}
	argv[4] = `"` + cmd + `"`
	return argv, true
}

// QuoteArgs quotes args so the shell reads each one back as a single word, for appending to a command line.
func QuoteArgs(args []string, shell string, win bool) string {
	name := shell
	if name == "" && win {
		name = "cmd"
	}
	if i := strings.LastIndexAny(name, `\/`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.ToLower(strings.TrimSuffix(strings.ToLower(name), ".exe"))
	quoted := make([]string, len(args))
	for i, a := range args {
		switch name {
		case "pwsh", "powershell":
			quoted[i] = "'" + strings.ReplaceAll(a, "'", "''") + "'"
		case "cmd":
			quoted[i] = `"` + strings.ReplaceAll(a, `"`, `""`) + `"`
		default:
			quoted[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
	}
	return strings.Join(quoted, " ")
}

// UserShell is the interactive shell of the user.
func UserShell() string {
	if IsWindows {
		if s := os.Getenv("ComSpec"); s != "" {
			return s
		}
		return "cmd.exe"
	}
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/sh"
}

// ── per-user directories ────────────────────────────────────────────────────────────────────────

func home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

// StateBase is where state (logs, pids, sockets) goes: $XDG_STATE_HOME, %LOCALAPPDATA% on Windows, ~/.local/state elsewhere.
func StateBase() string {
	if s := os.Getenv("XDG_STATE_HOME"); s != "" {
		return s
	}
	if IsWindows {
		if s := os.Getenv("LOCALAPPDATA"); s != "" {
			return s
		}
		return filepath.Join(home(), "AppData", "Local")
	}
	return filepath.Join(home(), ".local", "state")
}

// ConfigBase is where per-user settings go: $XDG_CONFIG_HOME, %APPDATA% on Windows, ~/.config elsewhere.
func ConfigBase() string {
	if s := os.Getenv("XDG_CONFIG_HOME"); s != "" {
		return s
	}
	if IsWindows {
		if s := os.Getenv("APPDATA"); s != "" {
			return s
		}
		return filepath.Join(home(), "AppData", "Roaming")
	}
	return filepath.Join(home(), ".config")
}

// DataBase is where data that is installed for the user goes: $XDG_DATA_HOME, %LOCALAPPDATA% on Windows,
// ~/.local/share elsewhere.
func DataBase() string {
	if s := os.Getenv("XDG_DATA_HOME"); s != "" {
		return s
	}
	if IsWindows {
		if s := os.Getenv("LOCALAPPDATA"); s != "" {
			return s
		}
		return filepath.Join(home(), "AppData", "Local")
	}
	return filepath.Join(home(), ".local", "share")
}

var (
	totalMemOnce  sync.Once
	totalMemBytes int64
)

// TotalMemory is the physical memory of the machine in bytes; 0 when it cannot be told.
func TotalMemory() int64 {
	totalMemOnce.Do(func() {
		switch {
		case IsLinux:
			data, err := os.ReadFile("/proc/meminfo")
			if err != nil {
				return
			}
			for _, line := range strings.Split(string(data), "\n") {
				if rest, ok := strings.CutPrefix(line, "MemTotal:"); ok {
					fields := strings.Fields(rest)
					if len(fields) > 0 {
						kb, _ := strconv.ParseInt(fields[0], 10, 64)
						totalMemBytes = kb * 1024
					}
					return
				}
			}
		case IsMac:
			out, err := runOutput([]string{"sysctl", "-n", "hw.memsize"}, 3*time.Second)
			if err == nil {
				totalMemBytes, _ = strconv.ParseInt(strings.TrimSpace(out), 10, 64)
			}
		case IsWindows:
			out, err := runOutput([]string{"powershell", "-NoProfile", "-NonInteractive", "-Command", "(Get-CimInstance Win32_ComputerSystem).TotalPhysicalMemory"}, 8*time.Second)
			if err == nil {
				totalMemBytes, _ = strconv.ParseInt(strings.TrimSpace(out), 10, 64)
			}
		}
	})
	return totalMemBytes
}
