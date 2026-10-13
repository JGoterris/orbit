package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/JGoterris/orbit/internal/cli"
)

// version is set at build time (-ldflags "-X main.version=...").
var version = "dev"

const red = "\x1b[31morbit:\x1b[0m"

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	inv, err := cli.Parse(args)
	if err != nil {
		var ue *cli.UsageError
		if errors.As(err, &ue) {
			fmt.Fprintf(os.Stderr, "%s %s\nrun `orbit --help` for usage\n", red, ue.Msg)
			return 1
		}
		fmt.Fprintf(os.Stderr, "%s %v\n", red, err)
		return 1
	}
	if inv.Options.Help {
		fmt.Print(cli.Help + "\n")
		return 0
	}

	name := inv.Sub
	if name == "" {
		name = "tui"
	}
	// Every command is wired up by a later phase of the migration (see the plan).
	fmt.Fprintf(os.Stderr, "%s %q is not implemented yet in this Go build (%s)\n", red, name, version)
	return 1
}
