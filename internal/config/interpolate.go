package config

import (
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

var varRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

// Interpolate replaces `${VAR}` and `${VAR:-default}` in every string of a YAML value (an unset or empty variable
// takes the default, or ""). It returns a new value.
func Interpolate(value any, vars map[string]string) any {
	switch x := value.(type) {
	case string:
		return varRe.ReplaceAllStringFunc(x, func(s string) string {
			m := varRe.FindStringSubmatch(s)
			if v := vars[m[1]]; v != "" {
				return v
			}
			return m[2]
		})
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = Interpolate(v, vars)
		}
		return out
	case *Map:
		out := NewMap()
		for _, k := range x.keys {
			out.Set(k, Interpolate(x.vals[k], vars))
		}
		return out
	}
	return value
}
