// Command neoharness works with many Claude Code sessions at once, in the
// terminal: a coordinator per workspace and the threads it opens.
//
// The hub that runs the sessions is a background process, as the tmux server
// is: the first neoharness starts it, every other one shares it, and it runs
// on after they close, until `neoharness hub stop`. Each thread keeps its
// Claude session id on disk, so the first turn after a new hub continues the
// conversation with --resume.
package main

import (
	"fmt"
	"os"
)

const usage = `neoharness — many Claude Code sessions, in the terminal

  neoharness               open the workspace; starts the hub if it is not running
  neoharness hub start     start the hub in the background
  neoharness hub stop      stop the running turns and the hub
  neoharness hub restart   stop the hub and start a new one, e.g. after an upgrade
  neoharness hub status    pid, uptime, running turns, version
  neoharness hub serve     run the hub in this terminal
  neoharness help          this text

Workspaces and threads live in ~/.neoharness/. The hub log is ~/.neoharness/hub.log.
`

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-h", "--help", "help":
			fmt.Print(usage)
			return
		case "hub":
			if err := cmdHub(os.Args[2:]); err != nil {
				fmt.Fprintln(os.Stderr, "neoharness: "+err.Error())
				os.Exit(1)
			}
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
