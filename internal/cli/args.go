// Package cli holds the command line of orbit: argument parsing, help text and the headless commands.
package cli

import (
	"fmt"
	"strings"
)

// Options are the flags orbit understands, whichever subcommand they are given to.
type Options struct {
	Config   string
	Up       bool
	Help     bool
	Force    bool
	JSON     bool
	NoDaemon bool
	Idle     string
	Follow   bool
	Lines    string
	Grep     string
	Since    string
}

type optSpec struct {
	long, short string
	takesValue  bool
	set         func(o *Options, v string)
}

var optSpecs = []optSpec{
	{long: "config", short: "c", takesValue: true, set: func(o *Options, v string) { o.Config = v }},
	{long: "up", short: "u", set: func(o *Options, _ string) { o.Up = true }},
	{long: "help", short: "h", set: func(o *Options, _ string) { o.Help = true }},
	{long: "force", set: func(o *Options, _ string) { o.Force = true }},
	{long: "json", set: func(o *Options, _ string) { o.JSON = true }},
	{long: "no-daemon", set: func(o *Options, _ string) { o.NoDaemon = true }},
	{long: "idle", takesValue: true, set: func(o *Options, v string) { o.Idle = v }},
	{long: "follow", short: "f", set: func(o *Options, _ string) { o.Follow = true }},
	{long: "lines", short: "n", takesValue: true, set: func(o *Options, v string) { o.Lines = v }},
	{long: "grep", takesValue: true, set: func(o *Options, v string) { o.Grep = v }},
	{long: "since", takesValue: true, set: func(o *Options, v string) { o.Since = v }},
}

func (s optSpec) describe() string {
	name := "--" + s.long
	if s.short != "" {
		name = "-" + s.short + ", " + name
	}
	if s.takesValue {
		name += " <value>"
	}
	return name
}

func findLong(name string) *optSpec {
	for i := range optSpecs {
		if optSpecs[i].long == name {
			return &optSpecs[i]
		}
	}
	return nil
}

func findShort(name string) *optSpec {
	for i := range optSpecs {
		if optSpecs[i].short == name {
			return &optSpecs[i]
		}
	}
	return nil
}

// ParseArgs splits args into options and positionals. Like util.parseArgs in the TS version, options and
// positionals may be interleaved, `--opt=value` and `-nVALUE` work, and `--` ends the options.
func ParseArgs(args []string) (Options, []string, error) {
	var opts Options
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			pos = append(pos, args[i+1:]...)
			return opts, pos, nil
		case strings.HasPrefix(a, "--"):
			name, value, hasValue := strings.Cut(a[2:], "=")
			spec := findLong(name)
			if spec == nil {
				return opts, nil, fmt.Errorf("Unknown option '--%s'", name)
			}
			if !spec.takesValue {
				if hasValue {
					return opts, nil, fmt.Errorf("Option '%s' does not take an argument", spec.describe())
				}
				spec.set(&opts, "")
				continue
			}
			if !hasValue {
				if i+1 >= len(args) {
					return opts, nil, fmt.Errorf("Option '%s' argument missing", spec.describe())
				}
				i++
				value = args[i]
			}
			spec.set(&opts, value)
		case len(a) > 1 && a[0] == '-':
			// a cluster of short options: -fn 20, -n20, -uf
			for j := 1; j < len(a); j++ {
				spec := findShort(string(a[j]))
				if spec == nil {
					return opts, nil, fmt.Errorf("Unknown option '-%c'", a[j])
				}
				if !spec.takesValue {
					spec.set(&opts, "")
					continue
				}
				if j+1 < len(a) {
					spec.set(&opts, a[j+1:])
				} else if i+1 < len(args) {
					i++
					spec.set(&opts, args[i])
				} else {
					return opts, nil, fmt.Errorf("Option '%s' argument missing", spec.describe())
				}
				break
			}
		default:
			pos = append(pos, a)
		}
	}
	return opts, pos, nil
}
