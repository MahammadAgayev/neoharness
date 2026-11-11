# AGENTS.md

neoharness is a Go TUI with a hub in the same process that runs many Claude
Code sessions: a coordinator and threads per workspace, local or over ssh.

Read first:

- [docs/architecture.md](docs/architecture.md): the hub, turns, states, MCP
  tools, artifacts.
- [docs/development.md](docs/development.md): code map, import rules, and how
  to run a scratch hub end to end.
- [docs/usage.md](docs/usage.md) and [docs/configuration.md](docs/configuration.md):
  what the person sees and sets.

## Build and test

```sh
go vet ./...
go test ./...
./install.sh          # builds to ~/.local/bin/neoharness
```

The tests need no model and no terminal: a fake agent, a fake client and the
tcell simulation screen. A bug fix starts with a test that fails without it.

## Rules

- The hub and the TUI are agent-agnostic. Claude-specific code stays in
  `internal/agent/claude`; the others see neutral fields only.
- The import rules in `docs/development.md` are enforced by
  `internal/architecture`. Do not work around them.
- Never test on the person's data in `~/.neoharness`. Use a scratch hub with
  its own `NEOHARNESS_HOME` and `mcp_port`, and check its socket exists before
  you type into it. Stop only processes you started.
- The repository is public. Keep employer names, hosts, internal tools and
  screen captures out of code, docs, tests and commits.
- Commit messages start with `neoharness: ` and say what changed for the
  person, in one line.
- Comments say why, only where the code does not. Write simple English.
- When a key, command, setting or prompt changes, update `:help`
  (`internal/tui/cmdline.go`) and the docs in the same commit.

## TUI design rules

1. Minimal: names, states, the conversation, the permission and question
   cards, the status line and toasts. Key hints and extra data go in `:help`.
2. Vim motions and ex commands, not new shortcuts.
3. Start simple and unfold: a workspace opens as the threads list and the
   coordinator; a thread shows only when you open it; an artifact opens in
   the window, and `<C-o>` goes back.
4. A thing shows its own state. No mode labels, except a muted `visual` /
   `v-line`. The cursor shape never changes.
5. Red (Love) only for errors and things that need the person. One accent
   (Iris) for "you type here". Bold only for names with focus.
6. No all-caps labels.
