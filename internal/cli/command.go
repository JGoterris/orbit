package cli

import (
	"fmt"
	"os"
	"slices"
)

// Subcommands are the words that select a command; anything else in first position is the TUI's directory.
var Subcommands = []string{"up", "down", "logs", "graph", "ls", "init", "validate", "open", "projects", "status", "ctl", "daemon", "run"}

// Invocation is a parsed command line: which command, its arguments and the directory it works on.
type Invocation struct {
	Options Options
	// Sub is empty for the TUI.
	Sub string
	// Rest are the positionals after the subcommand (service names, ctl action...).
	Rest []string
	// Args are what follows `--` in `orbit run <task> -- args`: they go to the task, not to orbit.
	Args []string
	// Dir is the directory given on the command line, "" when there is none.
	Dir string
}

// UsageError is a mistake in the command line; the caller prints it with a pointer to --help.
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

func usagef(format string, a ...any) error { return &UsageError{Msg: fmt.Sprintf(format, a...)} }

// Parse turns os.Args[1:] into an Invocation, applying the same positional rules as the TS version.
func Parse(args []string) (Invocation, error) {
	head, tail := args, []string(nil)
	if i := slices.Index(args, "--"); i >= 0 {
		head, tail = args[:i], args[i+1:]
	}
	opts, pos, err := ParseArgs(head)
	if err != nil {
		return Invocation{}, &UsageError{Msg: err.Error()}
	}
	inv := Invocation{Options: opts}
	if len(pos) > 0 && pos[0] == "run" {
		inv.Args = tail
	} else {
		pos = append(pos, tail...)
	}
	if opts.Help || opts.Version || len(pos) == 0 {
		return inv, nil
	}
	command, rest := pos[0], pos[1:]
	if slices.Contains(Subcommands, command) {
		inv.Sub, inv.Rest = command, rest
	} else {
		// `orbit <dir>`: the TUI for that directory
		inv.Dir, inv.Rest = command, rest
	}

	maxExtra := 0
	switch inv.Sub {
	case "up", "logs", "ctl":
		maxExtra = -1 // any number
	case "init", "open", "daemon", "validate", "run":
		maxExtra = 1
	}
	if inv.Sub == "open" && len(rest) != 1 {
		return inv, usagef("usage: orbit open <project name or path>")
	}
	if maxExtra >= 0 && len(inv.Rest) > maxExtra {
		return inv, usagef("unexpected argument %q", inv.Rest[maxExtra])
	}
	switch inv.Sub {
	case "init", "daemon", "validate":
		if len(inv.Rest) > 0 {
			inv.Dir = inv.Rest[0]
		}
	}
	if inv.Dir != "" && inv.Sub != "init" && inv.Sub != "open" {
		if st, err := os.Stat(inv.Dir); err != nil || !st.IsDir() {
			return inv, usagef("unknown command or directory %q", inv.Dir)
		}
	}
	return inv, nil
}
