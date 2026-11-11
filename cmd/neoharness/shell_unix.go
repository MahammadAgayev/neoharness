//go:build unix

package main

// localShell is the POSIX shell that runs file lists and `!` commands here.
func localShell() string { return "sh" }
