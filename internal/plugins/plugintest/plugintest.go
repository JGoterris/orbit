// Package plugintest gives the tests of orbit the demo plugin, built: it has something for every extension point.
package plugintest

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

var (
	once sync.Once
	dir  string
	err  error
)

func root() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..")
}

// Demo is the folder of the demo plugin, with its manifest and its binary. It is built once per test binary.
func Demo(t testing.TB) string {
	t.Helper()
	once.Do(func() {
		dir, err = os.MkdirTemp("", "orbit-demo-plugin")
		if err != nil {
			return
		}
		src := filepath.Join(root(), "test", "fixtures", "plugins", "demo")
		manifest, rerr := os.ReadFile(filepath.Join(src, "orbit-plugin.yaml"))
		if rerr != nil {
			err = rerr
			return
		}
		if err = os.WriteFile(filepath.Join(dir, "orbit-plugin.yaml"), manifest, 0o644); err != nil {
			return
		}
		bin := "demo"
		if runtime.GOOS == "windows" {
			bin += ".exe"
		}
		cmd := exec.Command("go", "build", "-o", filepath.Join(dir, bin), ".")
		cmd.Dir = src
		if out, berr := cmd.CombinedOutput(); berr != nil {
			err = &buildError{string(out)}
		}
	})
	if err != nil {
		t.Fatalf("building the demo plugin: %v", err)
	}
	return dir
}

type buildError struct{ out string }

func (e *buildError) Error() string { return e.out }
