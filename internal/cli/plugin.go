package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/plugins"
	"github.com/JGoterris/orbit/internal/ui"
	"github.com/JGoterris/orbit/internal/userconfig"
)

const pluginUsage = `usage: orbit plugin <command>

  install <owner/repo[@tag] | folder>   install a plugin (asks first; -y skips the question)
  link <folder>                         use a folder as a plugin without copying it, to develop one
  ls [--json]                           installed plugins
  upgrade [name]                        install the latest version of a plugin (all of them without a name)
  remove <name> [--purge]               uninstall a plugin (--purge also deletes its settings and data)
  enable <name> | disable <name>        turn a plugin on or off for you
  doctor [name]                         check the plugins: manifests, commands, keys, service types
  logs <name> [-n lines] [-f]           the log of the process of a plugin
  dir [name]                            where the plugins (or one of them) are installed
  new <name>                            start a plugin in a new folder`

func RunPlugin(opts Options, rest []string) int {
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, pluginUsage)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	cmd, args := rest[0], rest[1:]
	need := func(n int, what string) bool {
		if len(args) < n {
			errorln("usage: orbit plugin " + cmd + " " + what)
			return false
		}
		return true
	}
	switch cmd {
	case "install":
		if !need(1, "<owner/repo[@tag] | folder>") {
			return 1
		}
		return pluginInstall(ctx, args[0], opts.Yes)
	case "link":
		if !need(1, "<folder>") {
			return 1
		}
		return pluginLink(args[0])
	case "ls", "list":
		return pluginList(opts.JSON)
	case "upgrade", "update":
		return pluginUpgrade(ctx, args, opts.Yes)
	case "remove", "rm", "uninstall":
		if !need(1, "<name>") {
			return 1
		}
		return pluginRemove(args[0], opts.Purge)
	case "enable", "disable":
		if !need(1, "<name>") {
			return 1
		}
		return pluginSetEnabled(args[0], cmd == "enable")
	case "doctor":
		return pluginDoctor(args)
	case "logs":
		if !need(1, "<name>") {
			return 1
		}
		return pluginLogs(args[0], opts.Lines, opts.Follow)
	case "dir":
		return pluginDir(args)
	case "new":
		if !need(1, "<name>") {
			return 1
		}
		return pluginNew(args[0])
	case "help":
		fmt.Println(pluginUsage)
		return 0
	}
	errorln(fmt.Sprintf("unknown plugin command %q", cmd))
	fmt.Fprintln(os.Stderr, pluginUsage)
	return 1
}

// confirmed asks a yes / no question; yes answers it. It refuses to guess when nobody can answer.
func confirmed(question string, yes bool) bool {
	if yes {
		return true
	}
	if st, err := os.Stdin.Stat(); err != nil || st.Mode()&os.ModeCharDevice == 0 {
		errorln("nobody to ask: run it from a terminal, or pass -y")
		return false
	}
	fmt.Print(question + " [y/N] ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.EqualFold(strings.TrimSpace(line), "y") || strings.EqualFold(strings.TrimSpace(line), "yes")
}

func checkName(name string) error {
	if slices.Contains(Subcommands, name) {
		return fmt.Errorf("%q is a command of orbit: a plugin cannot be called that", name)
	}
	return nil
}

// describe prints what a staged plugin is and does, for the question before installing it.
func describe(st *plugins.Staged, previous *plugins.Plugin) {
	m := st.Plugin.Manifest
	head := bold(m.Name) + " " + m.Version
	if m.Description != "" {
		head += "  " + dim(m.Description)
	}
	fmt.Println(head)
	if src := st.Plugin.Meta.Source; src != "" {
		fmt.Println("  from  " + src)
	}
	if previous != nil {
		fmt.Println("  replaces version " + previous.Manifest.Version)
	}
	for _, line := range plugins.Summary(m) {
		fmt.Println("  adds  " + line)
	}
	if m.Run != nil {
		fmt.Println("  runs  " + strings.Join(m.Run.Command, " "))
	}
	if m.CLI != nil {
		fmt.Println("  runs  " + strings.Join(m.CLI.Command, " ") + dim("  (when you use `orbit "+m.Name+"`)"))
	}
	for _, w := range st.Warnings {
		fmt.Println(yellow("  warning: " + w))
	}
	fmt.Println(dim("  A plugin runs with your permissions: orbit does not sandbox it. Install only what you trust."))
}

func pluginInstall(ctx context.Context, spec string, yes bool) int {
	st, err := plugins.Stage(ctx, spec)
	if err != nil {
		errorln(err.Error())
		return 1
	}
	name := st.Plugin.Manifest.Name
	if err := checkName(name); err != nil {
		st.Discard()
		errorln(err.Error())
		return 1
	}
	previous, _ := plugins.Load(name)
	describe(st, previous)
	if !confirmed("Install?", yes) {
		st.Discard()
		fmt.Println("not installed")
		return 1
	}
	p, err := st.Commit()
	if err != nil {
		errorln(err.Error())
		return 1
	}
	fmt.Println(green("installed " + p.Manifest.Name + " " + p.Manifest.Version))
	fmt.Println(dim("orbit picks it up the next time it starts; an orbit daemon that is running needs `orbit down` first"))
	return 0
}

func pluginLink(dir string) int {
	p, err := plugins.Link(dir)
	if err != nil {
		errorln(err.Error())
		return 1
	}
	if err := checkName(p.Manifest.Name); err != nil {
		plugins.Remove(p.Manifest.Name)
		errorln(err.Error())
		return 1
	}
	fmt.Printf("%s %s -> %s\n", green("linked "+p.Manifest.Name), p.Manifest.Version, p.Dir)
	fmt.Println(dim("edits to the manifest show up the next time orbit starts (`orbit down` first if it runs)"))
	return 0
}

func pluginList(asJSON bool) int {
	list, broken := plugins.Installed()
	user := userconfig.Read()
	enabled := func(name string) bool {
		e := user.Plugins[name].Enabled
		return e == nil || *e
	}
	if asJSON {
		type item struct {
			Name        string   `json:"name"`
			Version     string   `json:"version"`
			Description string   `json:"description,omitempty"`
			Enabled     bool     `json:"enabled"`
			Linked      bool     `json:"linked,omitempty"`
			Source      string   `json:"source,omitempty"`
			Dir         string   `json:"dir"`
			Adds        []string `json:"adds"`
		}
		out := []item{}
		for _, p := range list {
			out = append(out, item{p.Manifest.Name, p.Manifest.Version, p.Manifest.Description, enabled(p.Manifest.Name), p.Meta.Linked, p.Meta.Source, p.Dir, plugins.Summary(p.Manifest)})
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
		return 0
	}
	if len(list) == 0 && len(broken) == 0 {
		fmt.Println(dim("no plugins installed: orbit plugin install <owner/repo>"))
		return 0
	}
	width := 0
	for _, p := range list {
		width = max(width, len(p.Manifest.Name))
	}
	for _, p := range list {
		m := p.Manifest
		state := green("on ")
		if !enabled(m.Name) {
			state = dim("off")
		}
		where := p.Meta.Source
		if p.Meta.Linked {
			where = "linked " + p.Dir
		}
		fmt.Printf("%s %s  %-8s %s\n", state, padEnd(m.Name, width), m.Version, dim(where))
		if m.Description != "" {
			fmt.Printf("    %s\n", m.Description)
		}
	}
	for name, err := range broken {
		fmt.Printf("%s %s  %s\n", red("bad"), name, red(err.Error()))
	}
	return 0
}

func pluginUpgrade(ctx context.Context, names []string, yes bool) int {
	list, _ := plugins.Installed()
	if len(names) > 0 {
		list = slices.DeleteFunc(list, func(p *plugins.Plugin) bool { return !slices.Contains(names, p.Manifest.Name) })
		if len(list) == 0 {
			errorln(fmt.Sprintf("plugin %q is not installed", names[0]))
			return 1
		}
	}
	code := 0
	for _, p := range list {
		name := p.Manifest.Name
		st, err := plugins.Upgrade(ctx, p)
		switch {
		case err != nil:
			errorln(name + ": " + err.Error())
			code = 1
			continue
		case st == nil:
			fmt.Println(dim(name + " is linked: nothing to upgrade"))
			continue
		case st.Plugin.Manifest.Version == p.Manifest.Version:
			st.Discard()
			fmt.Println(name + " " + p.Manifest.Version + " is up to date")
			continue
		}
		describe(st, p)
		if !confirmed("Upgrade "+name+"?", yes) {
			st.Discard()
			continue
		}
		if _, err := st.Commit(); err != nil {
			errorln(name + ": " + err.Error())
			code = 1
			continue
		}
		fmt.Println(green("upgraded " + name + " to " + st.Plugin.Manifest.Version))
	}
	return code
}

func pluginRemove(name string, purge bool) int {
	removeErr := plugins.Remove(name)
	if removeErr != nil && !purge {
		errorln(removeErr.Error())
		return 1
	}
	if removeErr == nil {
		fmt.Println(green("removed " + name))
	}
	cfg, data := plugins.ConfigDir(name), plugins.StateDir(name)
	if !purge {
		fmt.Println(dim("its settings (" + cfg + ") and data (" + data + ") were kept: --purge deletes them"))
		return 0
	}
	for _, dir := range []string{cfg, data} {
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			errorln(err.Error())
			return 1
		}
		fmt.Println(green("deleted " + dir))
	}
	// the choices the user made about it in config.json go too
	userconfig.Forget(name)
	return 0
}

func pluginSetEnabled(name string, on bool) int {
	if _, err := plugins.Load(name); err != nil {
		errorln(fmt.Sprintf("plugin %q is not installed (%v)", name, err))
		return 1
	}
	if !userconfig.Write(userconfig.Config{Plugins: map[string]userconfig.PluginSetting{name: {Enabled: &on}}}) {
		errorln("cannot write the settings")
		return 1
	}
	word := map[bool]string{true: "enabled", false: "disabled"}[on]
	fmt.Println(green(name + " " + word))
	fmt.Println(dim("it takes effect the next time orbit starts (`orbit down` first if it runs)"))
	return 0
}

func pluginDir(args []string) int {
	if len(args) == 0 {
		fmt.Println(plugins.Root())
		return 0
	}
	p, err := plugins.Load(args[0])
	if err != nil {
		errorln(err.Error())
		return 1
	}
	fmt.Println(p.Dir)
	return 0
}

func pluginLogs(name, lines string, follow bool) int {
	file := plugins.LogFile(name)
	n := 50
	if lines != "" {
		v, err := strconv.Atoi(lines)
		if err != nil || v < 0 {
			errorln("--lines must be a number")
			return 1
		}
		n = v
	}
	f, err := os.Open(file)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println(dim("no log yet: " + file))
			return 0
		}
		errorln(err.Error())
		return 1
	}
	defer f.Close()
	data, _ := io.ReadAll(f)
	all := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(all) > n {
		all = all[len(all)-n:]
	}
	for _, l := range all {
		if l != "" {
			fmt.Println(l)
		}
	}
	if !follow {
		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	for ctx.Err() == nil {
		chunk, _ := io.ReadAll(f)
		if len(chunk) > 0 {
			os.Stdout.Write(chunk)
		}
		select {
		case <-ctx.Done():
		case <-time.After(300 * time.Millisecond):
		}
	}
	return 0
}

func pluginDoctor(names []string) int {
	list, broken := plugins.Installed()
	var infos []plugins.Info
	for _, p := range list {
		infos = append(infos, plugins.Info{Name: p.Manifest.Name, Contributes: p.Manifest.Contributes})
	}
	_, keyProblems := ui.ResolvePluginKeys(infos, userconfig.Read().Keys)

	bad := 0
	report := func(name, version string, problems []string) {
		if len(names) > 0 && !slices.Contains(names, name) {
			return
		}
		fmt.Println(strings.TrimSpace(bold(name) + " " + version))
		if len(problems) == 0 {
			fmt.Println("  " + green("✓ ok"))
		}
		for _, p := range problems {
			bad++
			fmt.Println("  " + red("✖ "+p))
		}
	}
	for name, err := range broken {
		report(name, "", []string{err.Error()})
	}
	owner := map[string]string{} // service type → the plugin that has it
	for _, p := range list {
		m := p.Manifest
		var problems []string
		for _, c := range []struct {
			what string
			argv []string
		}{{"run", plugins.Argv(m.Run, p.Dir)}, {"cli", plugins.Argv(m.CLI, p.Dir)}} {
			if len(c.argv) == 0 {
				continue
			}
			if _, err := exec.LookPath(c.argv[0]); err != nil {
				problems = append(problems, fmt.Sprintf("%s: %s cannot be run", c.what, c.argv[0]))
			}
		}
		for _, t := range m.Contributes.ServiceTypes {
			if other, ok := owner[t.Name]; ok {
				problems = append(problems, fmt.Sprintf("the service type %q is also provided by %s: only one of them is used", t.Name, other))
				continue
			}
			owner[t.Name] = m.Name
		}
		for _, kp := range keyProblems {
			if strings.HasPrefix(kp, m.Name+".") {
				problems = append(problems, kp)
			}
		}
		report(m.Name, m.Version, problems)
	}
	if bad > 0 {
		return 1
	}
	return 0
}

// RunPluginCommand runs `orbit <plugin> args…` when the first word is a plugin that has a command. The arguments go
// to the plugin as they are: orbit does not parse them. handled is false when it is not one.
func RunPluginCommand(args []string) (code int, handled bool) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") || slices.Contains(Subcommands, args[0]) {
		return 0, false
	}
	name := args[0]
	// `orbit <name>` alone opens a folder with that name; with arguments (the command of a plugin has them, the
	// TUI takes none) it cannot be that
	if st, err := os.Stat(name); err == nil && st.IsDir() && len(args) == 1 {
		return 0, false
	}
	p, err := plugins.Load(name)
	if err != nil || p.Manifest.CLI == nil {
		return 0, false
	}
	if e := userconfig.Read().Plugins[name].Enabled; e != nil && !*e {
		errorln(fmt.Sprintf("plugin %s is disabled: orbit plugin enable %s", name, name))
		return 1, true
	}
	cwd, _ := os.Getwd()
	root := cwd
	if file := config.FindConfigFile(cwd); file != "" {
		root = filepath.Dir(file)
	}
	argv := append(plugins.Argv(p.Manifest.CLI, p.Dir), args[1:]...)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), plugins.Env(p, root)...)
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return ee.ExitCode(), true
		}
		errorln(fmt.Sprintf("cannot run plugin %s: %v", name, err))
		return 1, true
	}
	return 0, true
}
