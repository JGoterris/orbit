package theme

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/JGoterris/orbit/internal/userconfig"
)

var hex = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func TestBuiltinThemes(t *testing.T) {
	want := []string{"orbit", "catppuccin-mocha", "catppuccin-macchiato", "catppuccin-frappe", "catppuccin-latte", "tokyo-night", "dracula", "nord", "gruvbox", "rose-pine", "one-dark"}
	if !reflect.DeepEqual(Builtin, want) {
		t.Fatalf("themes = %v", Builtin)
	}
	for name, p := range Themes {
		for _, k := range ColorKeys {
			if v, ok := p.Get(k); !ok || !hex.MatchString(v) {
				t.Errorf("%s.%s = %q", name, k, v)
			}
		}
		if len(p.Services) == 0 {
			t.Errorf("%s has no service colours", name)
		}
		for _, c := range p.Services {
			if !hex.MatchString(c) {
				t.Errorf("%s service colour %q", name, c)
			}
		}
	}
}

func TestApplyChangesTheHelpers(t *testing.T) {
	t.Cleanup(func() { Apply(Themes[DefaultName]) })
	if StyleFor("healthy", false).Color != Themes["orbit"].Green {
		t.Fatal("default theme not active")
	}
	Apply(Themes["dracula"])
	if Active().Accent != Themes["dracula"].Accent || StyleFor("crashed", false).Color != Themes["dracula"].Red {
		t.Fatal("helpers did not follow the theme")
	}
	if ServiceColor("a", []string{"a", "b"}) != Themes["dracula"].Services[0] {
		t.Fatal("service colour")
	}
}

func TestOneshotStyles(t *testing.T) {
	if s := StyleFor("exited", true); s.Icon != "✓" || s.Label != "done" {
		t.Errorf("done = %+v", s)
	}
	if s := StyleFor("exited", false); s.Label != "exited" {
		t.Errorf("exited = %+v", s)
	}
}

func TestSparklineAndAreaChart(t *testing.T) {
	if got := Sparkline([]float64{0, 50, 100}, 5, 100); got != "  ▁▅█" {
		t.Errorf("sparkline = %q", got)
	}
	rows := AreaChart([]float64{100}, 3, 2, 100)
	if len(rows) != 2 || rows[0] != "  █" || rows[1] != "▁▁█" {
		t.Errorf("chart = %q", rows)
	}
}

func TestFitAndMix(t *testing.T) {
	if Fit("hello", 3) != "he…" || Fit("hi", 4) != "hi  " || Fit("x", 0) != "" {
		t.Error("Fit")
	}
	if got := Mix("#000000", "#ffffff", 0.5); got != "#808080" {
		t.Errorf("Mix = %s", got)
	}
}

func TestCustomThemeExtendsAndOverrides(t *testing.T) {
	p, err := ParseThemeFile(map[string]any{
		"extends": "catppuccin-mocha",
		"colors":  map[string]any{"accent": "#FF00FF", "services": []any{"#112233"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Accent != "#ff00ff" || !reflect.DeepEqual(p.Services, []string{"#112233"}) || p.Bg != Themes["catppuccin-mocha"].Bg {
		t.Fatalf("palette = %+v", p)
	}
	if Themes["catppuccin-mocha"].Accent == "#ff00ff" {
		t.Fatal("the parent was mutated")
	}
}

func TestCustomThemeErrors(t *testing.T) {
	if p, err := ParseThemeFile(map[string]any{}); err != nil || p.Bg != Themes["orbit"].Bg {
		t.Errorf("default extends: %v", err)
	}
	cases := []struct {
		doc  any
		want string
	}{
		{map[string]any{"colors": map[string]any{"accent": "red"}}, "#rrggbb"},
		{map[string]any{"colors": map[string]any{"nope": "#000000"}}, "unknown color"},
		{map[string]any{"extends": "nope"}, "unknown built-in"},
		{[]any{}, "object"},
	}
	for _, c := range cases {
		if _, err := ParseThemeFile(c.doc); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: err = %v, want %q", c.doc, err, c.want)
		}
	}
}

func TestLoadCustomReportsBrokenFiles(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := filepath.Join(userconfig.Dir(), "themes")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "mine.json"), []byte(`{"extends":"nord","colors":{"accent":"#010203"}}`), 0o644)
	os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{"), 0o644)
	os.WriteFile(filepath.Join(dir, "nord.json"), []byte("{}"), 0o644)
	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("ignored"), 0o644)
	themes, names, errs := LoadCustom()
	if !reflect.DeepEqual(names, []string{"mine"}) || themes["mine"].Accent != "#010203" {
		t.Fatalf("themes = %v, names = %v", themes, names)
	}
	joined := strings.Join(errs, "\n")
	if len(errs) != 2 || !strings.Contains(joined, "broken.json") || !strings.Contains(joined, "clashes") {
		t.Fatalf("errors = %v", errs)
	}
}

func TestNoThemesDirIsFine(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	themes, names, errs := LoadCustom()
	if len(themes) != 0 || len(names) != 0 || len(errs) != 0 {
		t.Fatalf("themes = %v %v %v", themes, names, errs)
	}
}

func TestFitMidKeepsBothEnds(t *testing.T) {
	cases := []struct {
		in    string
		width int
		want  string
	}{
		{"api", 6, "api   "},
		{"api-gateway-prod-eu-1", 11, "api-g…eu-1"},
		{"api-gateway-prod-eu-1", 10, "api-g…eu-1"},
		{"abcdefghij", 4, "abc…"},
		{"abcdefghij", 0, ""},
	}
	for _, c := range cases {
		got := FitMid(c.in, c.width)
		if len([]rune(got)) != max(0, c.width) {
			t.Errorf("FitMid(%q, %d) = %q is not %d wide", c.in, c.width, got, c.width)
		}
		if c.in == "api" && got != c.want {
			t.Errorf("FitMid(%q, %d) = %q", c.in, c.width, got)
		}
	}
	if got := FitMid("api-gateway-prod-eu-1", 11); !strings.HasPrefix(got, "api-g") || !strings.HasSuffix(got, "eu-1") || !strings.Contains(got, "…") {
		t.Errorf("both ends must survive: %q", got)
	}
}
