package ui

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// copyResult is the outcome of a copy to the system clipboard.
type copyResult struct {
	ok  bool
	via string
	err string
}

// clipboardTools in order of preference once OSC 52 is not an option.
var clipboardTools = [][]string{
	{"wl-copy"},
	{"xclip", "-selection", "clipboard"},
	{"xsel", "--clipboard", "--input"},
	{"pbcopy"},
	{"clip.exe"},
}

// osc52 asks the terminal to put text in the clipboard; it also works over ssh and in WSL.
func osc52(text string) bool {
	seq := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x07"
	_, err := os.Stdout.WriteString(seq)
	return err == nil
}

func pipeTo(argv []string, text string) bool {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run() == nil
}

// copyText copies text to the system clipboard: through the terminal (OSC 52) first when orbit runs over ssh,
// otherwise a clipboard tool, with OSC 52 as the last resort.
func copyText(text string) copyResult {
	if os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" {
		if osc52(text) {
			return copyResult{ok: true, via: "terminal"}
		}
	}
	for _, tool := range clipboardTools {
		if _, err := exec.LookPath(tool[0]); err == nil && pipeTo(tool, text) {
			return copyResult{ok: true, via: tool[0]}
		}
	}
	if osc52(text) {
		return copyResult{ok: true, via: "terminal (OSC 52, unverified)"}
	}
	return copyResult{err: "no clipboard available (install wl-copy/xclip, or use a terminal with OSC 52)"}
}

// clipboardCopy is a variable so tests can capture what would be copied.
var clipboardCopy = copyText

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func describeCopy(r copyResult, what, unit string, n int) (string, bool) {
	if unit == "" {
		unit = "line"
	}
	if r.ok {
		return fmt.Sprintf("copied %s (%d %s%s) via %s", what, n, unit, plural(n), r.via), true
	}
	if r.err != "" {
		return r.err, false
	}
	return "copy failed", false
}
