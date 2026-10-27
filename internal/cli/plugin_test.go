package cli

import (
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JGoterris/orbit/internal/plugins"
	"github.com/JGoterris/orbit/internal/plugins/plugintest"
	"github.com/JGoterris/orbit/internal/userconfig"
)

func TestScaffoldIsAValidPlugin(t *testing.T) {
	files, err := scaffold("weather")
	if err != nil {
		t.Fatal(err)
	}
	m, err := plugins.ParseManifest([]byte(files["orbit-plugin.yaml"]))
	if err != nil {
		t.Fatalf("the manifest of a new plugin must be valid: %v", err)
	}
	if m.Name != "weather" || m.Run.Command[0] != "./orbit-weather" || len(m.Contributes.Views) != 1 {
		t.Fatalf("manifest = %+v", m)
	}
	src := files["main.go"]
	if _, err := parser.ParseFile(token.NewFileSet(), "main.go", src, parser.AllErrors); err != nil {
		t.Fatalf("main.go does not parse: %v\n%s", err, src)
	}
	if formatted, err := format.Source([]byte(src)); err != nil || string(formatted) != src {
		t.Fatalf("main.go must come gofmt'd (err %v)", err)
	}
	for path, content := range files {
		if strings.Contains(content, "@NAME@") || strings.Contains(content, "@TITLE@") {
			t.Fatalf("%s has a placeholder left", path)
		}
	}
	if !strings.Contains(files[".goreleaser.yaml"], "orbit-weather") || !strings.Contains(files[".github/workflows/release.yml"], "${{ secrets.GITHUB_TOKEN }}") {
		t.Fatal("the release files must name the plugin and keep the Actions syntax")
	}
}

func TestPluginNewWritesTheFolderAndRefusesToOverwrite(t *testing.T) {
	cwd, _ := os.Getwd()
	dir := t.TempDir()
	os.Chdir(dir)
	t.Cleanup(func() { os.Chdir(cwd) })
	if code := pluginNew("weather"); code != 0 {
		t.Fatalf("code %d", code)
	}
	for _, f := range []string{"orbit-plugin.yaml", "main.go", "go.mod", "Makefile", ".goreleaser.yaml", ".github/workflows/release.yml", "README.md"} {
		if _, err := os.Stat(filepath.Join(dir, "orbit-weather", filepath.FromSlash(f))); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	if code := pluginNew("weather"); code == 0 {
		t.Fatal("a second run must not overwrite the folder")
	}
	for _, bad := range []string{"Weather", "1up", "up", "x y", ""} {
		if code := pluginNew(bad); code == 0 {
			t.Errorf("%q must be refused (it is a command of orbit or not a valid name)", bad)
		}
	}
}

func TestPluginCommandAndFolderWithTheSameName(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if _, err := plugins.Link(plugintest.Demo(t)); err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "demo"), 0o755)
	os.Chdir(dir)
	t.Cleanup(func() { os.Chdir(cwd) })

	if _, handled := RunPluginCommand([]string{"demo"}); handled {
		t.Fatal("on its own, the name of a folder is the folder to open")
	}
	if code, handled := RunPluginCommand([]string{"demo", "hello"}); !handled || code != 0 {
		t.Fatalf("with arguments it is the plugin: handled %v code %d", handled, code)
	}
	if code, handled := RunPluginCommand([]string{"demo", "fail"}); !handled || code != 3 {
		t.Fatalf("the exit code of the plugin is orbit's: handled %v code %d", handled, code)
	}
	for _, args := range [][]string{{"up"}, {"-V"}, {"nosuchplugin", "x"}, {}} {
		if _, handled := RunPluginCommand(args); handled {
			t.Errorf("%v is not a command of a plugin", args)
		}
	}
}

func TestRemoveWithPurgeDeletesEverything(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if _, err := plugins.Link(plugintest.Demo(t)); err != nil {
		t.Fatal(err)
	}
	cfg, data := plugins.ConfigDir("demo"), plugins.StateDir("demo")
	for _, d := range []string{cfg, data} {
		os.MkdirAll(d, 0o755)
		os.WriteFile(filepath.Join(d, "f"), []byte("x"), 0o644)
	}
	off := false
	userconfig.Write(userconfig.Config{Plugins: map[string]userconfig.PluginSetting{"demo": {Enabled: &off}}, Keys: map[string]string{"demo.hello": "alt+x", "other.a": "f2"}})

	if code := pluginRemove("demo", false); code != 0 {
		t.Fatal(code)
	}
	if _, err := os.Stat(cfg); err != nil {
		t.Fatal("without --purge the settings stay")
	}
	if code := pluginRemove("demo", true); code != 0 {
		t.Fatal(code)
	}
	for _, d := range []string{cfg, data} {
		if _, err := os.Stat(d); err == nil {
			t.Fatalf("%s was not deleted", d)
		}
	}
	c := userconfig.Read()
	if _, ok := c.Plugins["demo"]; ok || c.Keys["demo.hello"] != "" || c.Keys["other.a"] != "f2" {
		t.Fatalf("config.json must forget the plugin and only it: %+v", c)
	}
	if code := pluginRemove("nothing", false); code == 0 {
		t.Fatal("removing what is not installed is an error")
	}
}
