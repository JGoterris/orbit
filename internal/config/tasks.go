package config

import (
	"path/filepath"
	"strings"
)

// parseTask reads `lint: golangci-lint run` or `{ cmd, description, cwd, env, env_file, shell, depends_on }`.
func parseTask(name string, raw any, root string) *TaskConfig {
	path := "tasks." + name
	var rec *Map
	if s, ok := raw.(string); ok {
		rec = NewMap()
		rec.Set("cmd", s)
	} else {
		rec = asRecord(raw, path)
	}
	cmd, _ := asString(rec.First("cmd", "command"), path+".cmd")
	if strings.TrimSpace(cmd) == "" {
		fail("a task needs `cmd`", path)
	}
	cwd := root
	if s, ok := asString(rec.Get("cwd"), path+".cwd"); ok && s != "" {
		cwd = s
		if !filepath.IsAbs(cwd) {
			cwd = filepath.Join(root, cwd)
		}
	}
	return &TaskConfig{
		Name:        name,
		Description: strOr(rec.Get("description"), path+".description", ""),
		Cmd:         cmd,
		Cwd:         cwd,
		Shell:       strOr(rec.Get("shell"), path+".shell", ""),
		Env:         asEnv(rec.First("env", "environment"), path+".env"),
		EnvFiles:    parseEnvFileRefs(rec.Get("env_file"), path+".env_file", root),
		DependsOn:   orEmpty(asStringList(rec.First("depends_on", "dependsOn"), path+".depends_on")),
	}
}
