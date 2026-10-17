// Package tasks builds the command line of a task (`orbit run`, the palette); running it is up to the caller, which
// owns the terminal.
package tasks

import (
	"strings"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/platform"
)

// Spec is what to run for a task.
type Spec struct {
	Argv []string
	// Verbatim: Windows must get the arguments untouched (see platform.ShellCommand)
	Verbatim bool
	Cwd      string
	Env      map[string]string
	// Title is the command line, as the user would read it
	Title string
}

// Command is the shell command of the task with args appended, in its cwd and with its environment: env_file
// entries first, then the inline env on top.
func Command(t *config.TaskConfig, args []string) (Spec, error) {
	env, err := config.ReadEnvFiles(t.EnvFiles)
	if err != nil {
		return Spec{}, err
	}
	for k, v := range t.Env {
		env[k] = v
	}
	env["ORBIT_TASK"] = t.Name
	line := t.Cmd
	if len(args) > 0 {
		line += " " + platform.QuoteArgs(args, t.Shell, platform.IsWindows)
	}
	argv, verbatim := platform.ShellCommand(line, t.Shell, platform.IsWindows)
	return Spec{Argv: argv, Verbatim: verbatim, Cwd: t.Cwd, Env: env, Title: strings.TrimSpace(line)}, nil
}
