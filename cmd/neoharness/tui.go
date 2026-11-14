package main

import (
	"os"
	"os/exec"
	"strings"

	"github.com/MahammadAgayev/neoharness/tui"
)

func cmdTUI() error {
	c, err := ensureHub()
	if err != nil {
		return err
	}
	return tui.Run(c, tui.Hub{
		Start: func() error {
			_, err := ensureHub()
			return err
		},
		Restart: func() error {
			if c.Ping() == nil {
				if err := stopHub(c); err != nil {
					return err
				}
			}
			return spawnHub(c)
		},
		Version: func() string {
			exe, err := os.Executable()
			if err != nil {
				return ""
			}
			return binaryVersion(exe)
		},
	})
}

// binaryVersion is the commit of the neoharness binary at exe, empty when it
// does not say.
func binaryVersion(exe string) string {
	out, err := exec.Command(exe, "version").Output()
	if v := strings.TrimSpace(string(out)); err == nil && v != "unknown" {
		return v
	}
	return ""
}
