package config

import (
	"fmt"
	"regexp"
	"strings"
)

var dotEnvRe = regexp.MustCompile(`^\s*(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*?)\s*$`)
var trailingComment = regexp.MustCompile(`\s+#.*$`)

// ParseDotEnv reads KEY=value lines: `export` prefix, double quotes (with \n and \"), single quotes and trailing
// ` # comments` on unquoted values.
func ParseDotEnv(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSuffix(line, "\r")
		m := dotEnvRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		value := strings.TrimSpace(m[2])
		switch {
		case len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"':
			value = strings.ReplaceAll(strings.ReplaceAll(value[1:len(value)-1], `\n`, "\n"), `\"`, `"`)
		case len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'':
			value = value[1 : len(value)-1]
		default:
			value = trailingComment.ReplaceAllString(value, "")
		}
		out[m[1]] = value
	}
	return out
}

var varRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?:(:?[-?+])([^}]*))?\}`)

// Interpolate expands ${VAR} in every string of a YAML value with compose's syntax (:-, -, :?, ?, :+, +). It
// returns a new value.
func Interpolate(value any, vars map[string]string) any { return interpolateAt(value, vars, "") }

func interpolateAt(value any, vars map[string]string, path string) any {
	switch x := value.(type) {
	case string:
		return varRe.ReplaceAllStringFunc(x, func(s string) string {
			m := varRe.FindStringSubmatch(s)
			v, set := vars[m[1]]
			// with a colon, an empty value counts as unset
			present := set && (v != "" || !strings.HasPrefix(m[2], ":"))
			switch strings.TrimPrefix(m[2], ":") {
			case "-":
				if !present {
					return m[3]
				}
			case "?":
				if !present {
					msg := "required variable " + m[1] + " is missing a value"
					if m[3] != "" {
						msg += ": " + m[3]
					}
					fail(msg, path)
				}
			case "+":
				if present {
					return m[3]
				}
				return ""
			}
			return v
		})
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = interpolateAt(v, vars, fmt.Sprintf("%s[%d]", path, i))
		}
		return out
	case *Map:
		out := NewMap()
		for _, k := range x.keys {
			at := k
			if path != "" {
				at = path + "." + k
			}
			out.Set(k, interpolateAt(x.vals[k], vars, at))
		}
		return out
	}
	return value
}
