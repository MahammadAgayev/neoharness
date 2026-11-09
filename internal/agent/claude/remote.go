package claude

import "strings"

// sshCommand wraps a local claude command line to run in dir on host.
//
// The remote side runs it through the user's login shell, because a
// non-interactive ssh command gets a minimal PATH and claude is usually
// installed somewhere only the login profile adds. ~/.local/bin, where
// Claude's own installer puts it, is added too: some profiles add it only in
// the interactive rc, and an interactive shell may start tmux or ask things.
// BatchMode makes a missing key fail at once instead of waiting on a password
// prompt nobody can see.
func sshCommand(host, dir string, env, args []string) []string {
	var script strings.Builder
	script.WriteString(`export PATH="$HOME/.local/bin:$PATH"; `)
	if dir != "" {
		script.WriteString("cd " + quoteDir(dir) + " && ")
	}
	script.WriteString("exec ")
	if len(env) > 0 {
		script.WriteString("env ")
		for _, kv := range env {
			script.WriteString(shellQuote(kv) + " ")
		}
	}
	for i, a := range args {
		if i > 0 {
			script.WriteByte(' ')
		}
		script.WriteString(shellQuote(a))
	}
	remote := `exec "$SHELL" -lc ` + shellQuote(script.String())
	return []string{"ssh", "-o", "BatchMode=yes", host, "--", remote}
}

// SSHCommand runs args in dir on host the way a remote turn runs, for other
// work the hub does in a remote thread's folder.
func SSHCommand(host, dir string, args []string) []string {
	return sshCommand(host, dir, nil, args)
}

// quoteDir quotes a directory but leaves a leading ~ outside the quotes, so
// the remote shell still expands it to the home directory.
func quoteDir(dir string) string {
	switch {
	case dir == "~":
		return "~"
	case strings.HasPrefix(dir, "~/"):
		return "~/" + shellQuote(dir[2:])
	}
	return shellQuote(dir)
}

// shellQuote makes s one word for a POSIX shell.
func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_./=:,@+%", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
