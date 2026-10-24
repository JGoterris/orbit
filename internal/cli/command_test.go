package cli

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseArgsOptions(t *testing.T) {
	opts, pos, err := ParseArgs([]string{"logs", "api", "-f", "-n", "20", "--grep=err", "--since", "10m", "web", "-c", "x.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	want := Options{Follow: true, Lines: "20", Grep: "err", Since: "10m", Config: "x.yaml"}
	if opts != want {
		t.Fatalf("options = %+v, want %+v", opts, want)
	}
	if !reflect.DeepEqual(pos, []string{"logs", "api", "web"}) {
		t.Fatalf("positionals = %v", pos)
	}
}

func TestParseArgsShortClusters(t *testing.T) {
	opts, _, err := ParseArgs([]string{"-fn5", "-ud"})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.Follow || opts.Lines != "5" || !opts.Up || !opts.Detach {
		t.Fatalf("options = %+v", opts)
	}
}

func TestParseArgsTerminator(t *testing.T) {
	opts, pos, err := ParseArgs([]string{"up", "--", "-f", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Follow || opts.JSON {
		t.Fatalf("options after -- must not be parsed: %+v", opts)
	}
	if !reflect.DeepEqual(pos, []string{"up", "-f", "--json"}) {
		t.Fatalf("positionals = %v", pos)
	}
}

func TestParseArgsErrors(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--nope"}, "unknown option '--nope'"},
		{[]string{"-x"}, "unknown option '-x'"},
		{[]string{"--config"}, "option '-c, --config <value>' argument missing"},
		{[]string{"--json=1"}, "does not take an argument"},
	} {
		_, _, err := ParseArgs(tc.args)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("ParseArgs(%v) error = %v, want %q", tc.args, err, tc.want)
		}
	}
}

func TestParseSubcommands(t *testing.T) {
	dir := t.TempDir()
	inv, err := Parse([]string{"validate", dir})
	if err != nil {
		t.Fatal(err)
	}
	if inv.Sub != "validate" || inv.Dir != dir {
		t.Fatalf("invocation = %+v", inv)
	}

	inv, err = Parse([]string{"up", "api", "web"})
	if err != nil || inv.Sub != "up" || !reflect.DeepEqual(inv.Rest, []string{"api", "web"}) {
		t.Fatalf("up: %+v, %v", inv, err)
	}

	inv, err = Parse([]string{"update", "--check"})
	if err != nil || inv.Sub != "update" || !inv.Options.Check {
		t.Fatalf("update: %+v, %v", inv, err)
	}

	inv, err = Parse([]string{dir})
	if err != nil || inv.Sub != "" || inv.Dir != dir {
		t.Fatalf("tui: %+v, %v", inv, err)
	}

	inv, err = Parse(nil)
	if err != nil || inv.Sub != "" || inv.Dir != "" {
		t.Fatalf("no args: %+v, %v", inv, err)
	}
}

func TestParseUsageErrors(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"open"}, "usage: orbit open"},
		{[]string{"graph", "extra"}, `unexpected argument "extra"`},
		{[]string{"validate", "a", "b"}, `unexpected argument "b"`},
		{[]string{"/definitely/not/a/dir"}, `unknown command or directory "/definitely/not/a/dir"`},
	} {
		_, err := Parse(tc.args)
		var ue *UsageError
		if !errors.As(err, &ue) || !strings.Contains(ue.Msg, tc.want) {
			t.Errorf("Parse(%v) error = %v, want %q", tc.args, err, tc.want)
		}
	}
}

func TestHelpFlagWins(t *testing.T) {
	inv, err := Parse([]string{"-h", "bogus-dir"})
	if err != nil || !inv.Options.Help {
		t.Fatalf("%+v, %v", inv, err)
	}
}

func TestParseRunKeepsArgsForTheTask(t *testing.T) {
	inv, err := Parse([]string{"run", "test", "--", "-v", "--json", "-run", "Foo"})
	if err != nil {
		t.Fatal(err)
	}
	if inv.Sub != "run" || !reflect.DeepEqual(inv.Rest, []string{"test"}) || !reflect.DeepEqual(inv.Args, []string{"-v", "--json", "-run", "Foo"}) {
		t.Fatalf("invocation = %+v", inv)
	}
	if inv.Options.JSON {
		t.Fatal("options after -- belong to the task")
	}
	if _, err := Parse([]string{"run", "a", "b"}); err == nil {
		t.Fatal("run takes one task")
	}
	inv, err = Parse([]string{"logs", "--", "api"})
	if err != nil || !reflect.DeepEqual(inv.Rest, []string{"api"}) || inv.Args != nil {
		t.Fatalf("logs -- api: %+v, %v", inv, err)
	}
}

func TestProfileFlags(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want []string
		set  bool
	}{
		{[]string{"up"}, nil, false},
		{[]string{"up", "--profile", "a,b", "--profile=c"}, []string{"a", "b", "c"}, true},
		{[]string{"--profiles", "debug", "ls"}, []string{"debug"}, true},
		{[]string{"ls", "--no-profiles"}, nil, true},
		{[]string{"ls", "--no-profiles", "--profile", "x"}, []string{"x"}, true},
	} {
		inv, err := Parse(tc.args)
		if err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		got, set := inv.Options.ProfileSelection()
		if set != tc.set || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%v: got %v, %v; want %v, %v", tc.args, got, set, tc.want, tc.set)
		}
	}
}
