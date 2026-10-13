package theme

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/JGoterris/orbit/internal/userconfig"
)

var hexRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func hexColor(v any, path string) (string, error) {
	s, ok := v.(string)
	if !ok || !hexRe.MatchString(s) {
		b, _ := json.Marshal(v)
		return "", fmt.Errorf("%s: expected a color like \"#rrggbb\", got %s", path, b)
	}
	return strings.ToLower(s), nil
}

// ParseThemeFile builds a palette from `{ "extends": "<builtin>", "colors": { "accent": "#ff00ff", "services": ["#..."] } }`.
func ParseThemeFile(doc any) (Palette, error) {
	obj, ok := doc.(map[string]any)
	if !ok {
		return Palette{}, fmt.Errorf("expected a JSON object")
	}
	parent := DefaultName
	if v, ok := obj["extends"]; ok {
		s, isStr := v.(string)
		if _, known := Themes[s]; !isStr || !known {
			b, _ := json.Marshal(v)
			return Palette{}, fmt.Errorf("extends: unknown built-in theme %s (one of %s)", b, strings.Join(Builtin, ", "))
		}
		parent = s
	}
	out := clone(Themes[parent])
	colors := map[string]any{}
	if v, ok := obj["colors"]; ok {
		c, isMap := v.(map[string]any)
		if !isMap {
			return Palette{}, fmt.Errorf("colors: expected an object")
		}
		colors = c
	}
	keys := make([]string, 0, len(colors))
	for k := range colors {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := colors[key]
		if key == "services" {
			list, ok := value.([]any)
			if !ok || len(list) == 0 {
				return Palette{}, fmt.Errorf("colors.services: expected a non-empty list of colors")
			}
			var services []string
			for i, c := range list {
				h, err := hexColor(c, fmt.Sprintf("colors.services[%d]", i))
				if err != nil {
					return Palette{}, err
				}
				services = append(services, h)
			}
			out.Services = services
			continue
		}
		if !slices.Contains(ColorKeys, key) {
			return Palette{}, fmt.Errorf("colors.%s: unknown color (known: %s, services)", key, strings.Join(ColorKeys, ", "))
		}
		h, err := hexColor(value, "colors."+key)
		if err != nil {
			return Palette{}, err
		}
		out.Set(key, h)
	}
	return out, nil
}

// LoadCustom reads themes/*.json from the config dir. Broken files are reported in errs, never fatal.
func LoadCustom() (themes map[string]Palette, names []string, errs []string) {
	themes = map[string]Palette{}
	dir := filepath.Join(userconfig.Dir(), "themes")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return themes, nil, nil
	}
	for _, e := range entries {
		file := e.Name()
		if !strings.HasSuffix(file, ".json") {
			continue
		}
		name := strings.TrimSuffix(file, ".json")
		fail := func(err error) { errs = append(errs, fmt.Sprintf("theme %s: %v", file, err)) }
		if _, clash := Themes[name]; clash {
			fail(fmt.Errorf("name clashes with a built-in theme"))
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			fail(err)
			continue
		}
		var doc any
		if err := json.Unmarshal(data, &doc); err != nil {
			fail(err)
			continue
		}
		p, err := ParseThemeFile(doc)
		if err != nil {
			fail(err)
			continue
		}
		themes[name] = p
		names = append(names, name)
	}
	return themes, names, errs
}
