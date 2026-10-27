//go:build !windows

package e2e

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/JGoterris/orbit/internal/plugins/plugintest"
)

const pluginProject = `name: withplugin
services:
  plain:
    cmd: sleep 60
  tick:
    type: ticker
    with: { message: hi, intervalMs: 50 }
`

// demoPlugin installs the demo plugin, with the real `orbit plugin link`, in a data folder of its own. It returns
// the environment that makes the app use it: only where the plugins are, because the app sets up its own state and
// configuration (and the first one switches the splash off, which would eat the first key).
func demoPlugin(t *testing.T) (appEnv map[string]string) {
	t.Helper()
	data := t.TempDir()
	out, code := runOrbit(t, t.TempDir(), cliEnv(t, data), "plugin", "link", plugintest.Demo(t))
	if code != 0 {
		t.Fatalf("orbit plugin link failed (%d): %s", code, out)
	}
	return map[string]string{"XDG_DATA_HOME": data}
}

// cliEnv is the environment of a command of orbit: nothing it writes may reach the real home of whoever runs the
// tests.
func cliEnv(t *testing.T, data string) map[string]string {
	t.Helper()
	return map[string]string{"XDG_DATA_HOME": data, "XDG_STATE_HOME": t.TempDir(), "XDG_CONFIG_HOME": t.TempDir()}
}

func runOrbit(t *testing.T, dir string, env map[string]string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return out.String(), 0
	case errors.As(err, &ee):
		return out.String(), ee.ExitCode()
	}
	t.Fatalf("running orbit %v: %v", args, err)
	return "", 0
}

func TestPluginTabActionsAndServiceTypeInTheRealApp(t *testing.T) {
	env := demoPlugin(t)
	sc := startEnv(t, projectDir(t, pluginProject), 150, 38, env, "--no-daemon")
	sc.waitFor("the tab and the badge of the plugin", "5 Items", "demo", "Services 0/2")

	sc.send("5")
	sc.waitFor("the view of the plugin", "Items · 3", "alpha", "beta", "gamma", "item alpha", "log of alpha")
	if text := sc.text(); !strings.Contains(text, "not red") || strings.Contains(text, "[31m") || strings.Contains(text, "[0m") {
		t.Fatalf("the text of the plugin must be drawn, without its escape sequences:\n%s", sc.text())
	}

	sc.send("j")
	sc.waitFor("the bound panes follow the selection", "item beta", "log of beta")

	sc.send("B")
	sc.waitFor("the dialog of the action", "Bump", "minor", "major")
	sc.send("\x1b[B") // down
	sc.send("\r")
	sc.waitFor("the confirmation", "Bump beta (major)?", "confirm")
	sc.send("y")
	sc.waitFor("the action done and the data reloaded", "bumped beta (major)", "0.3.0")
	sc.waitFor("the badge of the header changed", "bumps 1")
	sc.dump()

	// a service of a type the plugin provides
	sc.send("1")
	sc.waitFor("the dashboard", "Services 0/2", "tick")
	sc.send("j")                                // tick
	sc.waitFor("its detail", "Demo", "hi tick") // the section the plugin adds to the detail
	sc.send(" ")
	sc.waitFor("the service running through the plugin", "ticker started", "hi #2")
	sc.waitFor("the badge of the plugin in the list", "running")
	sc.dump()
}

func TestPluginCommandsOfTheCLI(t *testing.T) {
	data := demoPlugin(t)["XDG_DATA_HOME"]
	env := cliEnv(t, data)
	dir := projectDir(t, pluginProject)

	out, code := runOrbit(t, dir, env, "demo", "hello", "a", "--not-orbits-flag")
	if code != 0 || !strings.Contains(out, "hello from the demo plugin") || !strings.Contains(out, "a --not-orbits-flag") {
		t.Fatalf("the arguments belong to the plugin (code %d): %q", code, out)
	}
	if !strings.Contains(out, dir) && !strings.Contains(strings.ReplaceAll(out, "/private", ""), strings.ReplaceAll(dir, "/private", "")) {
		t.Fatalf("the plugin must know the project it runs in: %q", out)
	}
	if _, code := runOrbit(t, dir, env, "demo", "fail"); code != 3 {
		t.Fatalf("the exit code of the plugin must be orbit's: %d", code)
	}
	out, code = runOrbit(t, dir, env, "plugin", "ls")
	if code != 0 || !strings.Contains(out, "demo") || !strings.Contains(out, "0.1.0") {
		t.Fatalf("plugin ls (%d): %q", code, out)
	}
	out, code = runOrbit(t, dir, env, "plugin", "ls", "--json")
	if code != 0 || !strings.Contains(out, `"name": "demo"`) {
		t.Fatalf("plugin ls --json (%d): %q", code, out)
	}
	out, code = runOrbit(t, dir, env, "plugin", "doctor")
	if code != 0 || !strings.Contains(out, "ok") {
		t.Fatalf("plugin doctor (%d): %q", code, out)
	}

	// disabled: the command says so, instead of "unknown command or directory"
	if out, code := runOrbit(t, dir, env, "plugin", "disable", "demo"); code != 0 {
		t.Fatalf("disable (%d): %s", code, out)
	}
	out, code = runOrbit(t, dir, env, "demo", "hello")
	if code == 0 || !strings.Contains(out, "plugin demo is disabled") {
		t.Fatalf("a disabled plugin must say so (%d): %q", code, out)
	}
	// and a project that uses its type fails to open the plugin's services, but opens
	if out, code := runOrbit(t, dir, env, "plugin", "remove", "demo"); code != 0 {
		t.Fatalf("remove (%d): %s", code, out)
	}
	out, code = runOrbit(t, dir, env, "validate")
	if code == 0 || !strings.Contains(out, `unknown type "ticker"`) {
		t.Fatalf("without the plugin the project is invalid, and says why (%d): %q", code, out)
	}
}

func TestPluginsThroughTheDaemon(t *testing.T) {
	appEnv := demoPlugin(t)
	dir := projectDir(t, pluginProject)
	// the daemon is a process of its own: whatever happens, it must not outlive the test. It lives in the state
	// folder of the app, so `down` has to be given the same one.
	state := t.TempDir()
	appEnv["XDG_STATE_HOME"] = state
	t.Cleanup(func() {
		runOrbit(t, dir, map[string]string{"XDG_DATA_HOME": appEnv["XDG_DATA_HOME"], "XDG_STATE_HOME": state, "XDG_CONFIG_HOME": t.TempDir()}, "down")
	})

	sc := startEnv(t, dir, 150, 38, appEnv)
	sc.waitFor("the tab of a plugin that runs in the daemon", "5 Items", "Services 0/2")
	sc.send("5")
	sc.waitFor("its data through the socket", "Items · 3", "alpha", "item alpha")
	sc.send("jB")
	sc.waitFor("the dialog", "Bump", "minor")
	sc.send("\r")
	sc.waitFor("the confirmation", "Bump beta (minor)?")
	sc.send("y")
	sc.waitFor("the result through the socket", "bumped beta (minor)", "0.3.0")
	time.Sleep(100 * time.Millisecond)
	sc.dump()
}
