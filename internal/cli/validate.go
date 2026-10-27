package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/JGoterris/orbit/internal/config"
	"github.com/JGoterris/orbit/internal/plugins"
)

// RunValidate checks the orbit.yaml (`orbit validate`); exit code 1 when it has errors.
func RunValidate(opts config.LoadOptions) int {
	res := config.ValidateFile(opts)
	if len(res.Errors) > 0 {
		for _, e := range res.Errors {
			fmt.Fprintf(os.Stderr, "%s %s\n", red("✖"), e)
		}
		return 1
	}
	name := "docker-compose"
	if res.File != "" {
		name = filepath.Base(res.File)
		if res.LocalFile != "" {
			name += " + " + filepath.Base(res.LocalFile)
		}
	}
	// the plugins know what their `with:` has to look like
	if errs := plugins.ValidateServices(res.Config, Version); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "%s %s\n", red("✖"), e)
		}
		return 1
	}
	WarnMissingPlugins(res.Config)
	n, g := len(res.Config.Services), len(res.Config.Groups)
	fmt.Printf("%s %s: %d service%s, %d group%s\n", green("✓"), name, n, plural(n), g, plural(g))
	return 0
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
