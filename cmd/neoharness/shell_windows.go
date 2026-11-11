package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

// localShell is the POSIX shell that runs file lists and `!` commands here:
// the sh of Git for Windows, which Claude Code uses too. Its bin\sh.exe is
// preferred over one on PATH because it puts the Unix tools first, so `find`
// is not the Windows one. Empty when there is none.
var localShell = sync.OnceValue(func() string {
	if git, err := exec.LookPath("git"); err == nil {
		// git.exe is in <Git>\cmd or <Git>\bin (or <Git>\mingw64\bin).
		for _, up := range []string{"..", filepath.Join("..", "..")} {
			sh := filepath.Join(filepath.Dir(git), up, "bin", "sh.exe")
			if _, err := os.Stat(sh); err == nil {
				return filepath.Clean(sh)
			}
		}
	}
	if sh, err := exec.LookPath("sh"); err == nil {
		return sh
	}
	return ""
})
