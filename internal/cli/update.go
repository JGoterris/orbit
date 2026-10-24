package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/JGoterris/orbit/internal/selfupdate"
)

// RunUpdate is `orbit update`: it looks for a newer release and installs it the way orbit was installed.
// With check it only reports (exit code 1 when there is a newer version). stamped is whether this is a release
// build, as opposed to `go install` or `go build`.
func RunUpdate(check, stamped bool) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		errorln(err.Error())
		return 1
	}
	selfupdate.CleanOld(exe)
	in := selfupdate.Detect(exe, stamped)

	latest, err := selfupdate.Latest(ctx)
	if err != nil {
		errorln("cannot look for updates: " + err.Error())
		return 1
	}
	// a go install or a local build has no release version of its own: Version is the module's, or "dev"
	if Version != "dev" && !selfupdate.Newer(latest, Version) {
		fmt.Printf("orbit %s is up to date %s\n", Version, dim("("+in.Method.String()+")"))
		return 0
	}
	fmt.Printf("orbit %s → %s %s\n", Version, bold(latest), dim("("+in.Method.String()+")"))
	if check {
		return 1
	}

	manual, err := selfupdate.Update(ctx, in, exe, latest, os.Stdout, os.Stderr)
	if err != nil {
		errorln(err.Error())
		return 1
	}
	if manual != "" {
		fmt.Println("run this to finish the update:\n  " + cyan(manual))
		return 0
	}
	fmt.Println(green("updated to orbit " + latest))
	fmt.Println(dim("an orbit daemon that is already running keeps the old version until `orbit down`"))
	return 0
}
