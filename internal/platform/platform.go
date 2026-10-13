// Package platform holds everything that differs between Linux, macOS and Windows: shells, process trees, pids,
// process metrics, who owns a port and where per-user files go. The rest of orbit stays platform-agnostic.
package platform

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
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
