//go:build unix

package workspace

import "os"

// linkDir makes link a symlink to the folder target, which is relative to
// link's own folder.
func linkDir(target, link string) error { return os.Symlink(target, link) }
