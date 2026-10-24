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
	Detach   bool
	Help     bool
	Version  bool
	Force    bool
	JSON     bool
	NoDaemon bool
	Idle     string
	Follow   bool
	Lines    string
	Grep     string
	Since    string
	Check    bool
	// Profile is every --profile given, joined with commas
	Profile    string
	NoProfiles bool
}

type optSpec struct {
	long, short string
	takesValue  bool
	set         func(o *Options, v string)
}

var optSpecs = []optSpec{
	{long: "config", short: "c", takesValue: true, set: func(o *Options, v string) { o.Config = v }},
	{long: "up", short: "u", set: func(o *Options, _ string) { o.Up = true }},
	{long: "detach", short: "d", set: func(o *Options, _ string) { o.Detach = true }},
	{long: "help", short: "h", set: func(o *Options, _ string) { o.Help = true }},
	{long: "version", short: "V", set: func(o *Options, _ string) { o.Version = true }},
	{long: "force", set: func(o *Options, _ string) { o.Force = true }},
	{long: "json", set: func(o *Options, _ string) { o.JSON = true }},
	{long: "no-daemon", set: func(o *Options, _ string) { o.NoDaemon = true }},
	{long: "idle", takesValue: true, set: func(o *Options, v string) { o.Idle = v }},
	{long: "follow", short: "f", set: func(o *Options, _ string) { o.Follow = true }},
	{long: "lines", short: "n", takesValue: true, set: func(o *Options, v string) { o.Lines = v }},
	{long: "grep", takesValue: true, set: func(o *Options, v string) { o.Grep = v }},
	{long: "since", takesValue: true, set: func(o *Options, v string) { o.Since = v }},
	{long: "check", set: func(o *Options, _ string) { o.Check = true }},
	{long: "profile", takesValue: true, set: addProfiles},
	{long: "profiles", takesValue: true, set: addProfiles},
	{long: "no-profiles", set: func(o *Options, _ string) { o.NoProfiles = true }},
}

func addProfiles(o *Options, v string) {
	if o.Profile != "" {
		o.Profile += ","
	}
	o.Profile += v
}

// ProfileSelection is what --profile / --no-profiles ask for: set is false when the command line says nothing, so
// the environment, the saved selection and the project's defaults decide.
func (o Options) ProfileSelection() (names []string, set bool) {
	if o.NoProfiles && o.Profile == "" {
		return nil, true
	}
	if o.Profile == "" {
		return nil, false
	}
	for _, n := range strings.Split(o.Profile, ",") {
		if n = strings.TrimSpace(n); n != "" {
			names = append(names, n)
		}
	}
	return names, true
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
				return opts, nil, fmt.Errorf("unknown option '--%s'", name)
			}
			if !spec.takesValue {
				if hasValue {
					return opts, nil, fmt.Errorf("option '%s' does not take an argument", spec.describe())
				}
				spec.set(&opts, "")
				continue
			}
			if !hasValue {
				if i+1 >= len(args) {
					return opts, nil, fmt.Errorf("option '%s' argument missing", spec.describe())
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
					return opts, nil, fmt.Errorf("unknown option '-%c'", a[j])
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
					return opts, nil, fmt.Errorf("option '%s' argument missing", spec.describe())
				}
				break
			}
		default:
			pos = append(pos, a)
		}
	}
	return opts, pos, nil
}
