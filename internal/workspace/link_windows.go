package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// linkDir makes link point to the folder target, which is relative to link's
// own folder. A symlink needs a privilege most Windows accounts lack, so it
// falls back to a junction, which needs none.
func linkDir(target, link string) error {
	symErr := os.Symlink(target, link)
	if symErr == nil {
		return nil
	}
	abs := filepath.Join(filepath.Dir(link), target)
	if out, err := exec.Command("cmd", "/C", "mklink", "/J", link, abs).CombinedOutput(); err != nil {
		return fmt.Errorf("%w; junction: %s", symErr, strings.TrimSpace(string(out)))
	}
	return nil
}
