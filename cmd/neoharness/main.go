// Command neoharness works with many Claude Code sessions at once, in the
// terminal: a coordinator per workspace and the threads it opens.
//
// It is one program, like an editor. The hub that runs the sessions lives in
// the same process as the TUI and stops when it closes. Each thread keeps its
// Claude session id on disk, so the first turn after reopening continues the
// conversation with --resume.
package main

import (
	"fmt"
	"os"
)

const usage = `neoharness — many Claude Code sessions, in the terminal

  neoharness         open the workspace
  neoharness help    this text

Workspaces and threads live in ~/.neoharness/. The hub log is ~/.neoharness/hub.log.
`

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-h", "--help", "help":
			fmt.Print(usage)
			return
		}
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err := cmdTUI(); err != nil {
		fmt.Fprintln(os.Stderr, "neoharness: "+err.Error())
		os.Exit(1)
	}
}
