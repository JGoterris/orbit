package ipc

import (
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
)

// maxSocketPath is the longest socket path the kernel accepts (sun_path minus the NUL): 104 bytes on macOS, 108 on
// Linux.
func maxSocketPath() int {
	if runtime.GOOS == "darwin" {
		return 103
	}
	return 107
}

// SocketPath is where a project's supervisor listens: `<stateDir>/orbit.sock`, or a short path when that one is
// too long for a Unix socket. On Windows it is a named pipe.
func SocketPath(stateDir string) string {
	sum := sha1.Sum([]byte(stateDir))
	hash := hex.EncodeToString(sum[:])[:12]
	if runtime.GOOS == "windows" {
		return `\\.\pipe\orbit-` + hash
	}
	direct := filepath.Join(stateDir, "orbit.sock")
	if len(direct) <= maxSocketPath() {
		return direct
	}
	var base string
	if x := os.Getenv("XDG_RUNTIME_DIR"); x != "" {
		base = filepath.Join(x, "orbit")
	} else {
		base = filepath.Join(os.TempDir(), "orbit-"+strconv.Itoa(os.Getuid()))
	}
	return filepath.Join(base, hash+".sock")
}

// socketDir is the directory that must exist (mode 0700) before listening; "" for named pipes.
func socketDir(path string) string {
	if runtime.GOOS == "windows" {
		return ""
	}
	return filepath.Dir(path)
}
