# Development

```sh
go vet ./...
go test -race ./...
./install.sh
```

The tests need no model and no real terminal: they use a fake agent, a fake
client and the tcell simulation screen.

## Code map

| Package | What it does |
|---|---|
| `cmd/neoharness` | CLI: starts the hub in this process, opens the TUI, file lists and `!` commands, locally or over ssh |
| `internal/model` | shared types; a leaf package |
| `internal/workspace` | disk storage of workspaces, threads, transcripts, artifacts |
| `internal/hub` | the core: queues, turns, state, routing, permissions, slash commands, artifacts |
| `internal/mcpserver` | MCP tools to hub methods |
| `internal/hubapi` / `internal/hubserver` | the local API client and server (unix socket, SSE) |
| `internal/tunnel` | ssh reverse tunnel per remote host |
| `internal/tui` | the terminal UI |
| `internal/agent` | the agent interface: one `Run` per turn, and a `Provider` per kind that lives as long as the hub |
| `internal/agent/claude` | the Claude Code adapter |
| `internal/prompts` | system prompts and notes for agents |
| `internal/linediff` | line diff for the artifact merge and diff mode |
| `internal/architecture` | tests that enforce the import rules |

Import rules, enforced by tests:

- `hub` imports no adapter, TUI, MCP, API server or tunnel.
- `tui` imports only `hubapi`, `model` and `linediff` from this module.
- `model` and `linediff` are leaves. `workspace` and `hubapi` import only
  `model`.

Keep Claude-specific code in `internal/agent/claude`. The hub and the TUI see
only neutral fields.

To add an agent kind, write an `agent.Provider` in `internal/agent/<kind>`
and add it to `kinds` in `cmd/neoharness/hub.go`. `init.lua` then names it
as an agent's `kind`.

## End-to-end runs on a scratch hub

Do not test on your real data. Run a scratch hub with its own home and port
in a throwaway tmux session, and drive it with `tmux send-keys`:

```sh
T=$(mktemp -d); mkdir -p $T/home $T/dir
cat > $T/home/init.lua <<'EOF'
require("neoharness").setup({ mcp_port = 47999 })
require("neoharness.agents").setup({ claude = { model = "haiku" } })
EOF
tmux new-session -d -s nhe2e -x 160 -y 45
tmux set -t nhe2e remain-on-exit on
tmux respawn-pane -k -t nhe2e "env NEOHARNESS_HOME=$T/home neoharness"
test -S $T/home/hub.sock || echo "NOT the scratch hub — stop here"
```

- If a hub answers on the real socket, a TUI with the real home uses it.
  Always check that the scratch socket exists before you type.
- `:new` leaves you in Insert mode.
- Use `send-keys -l` for text with special characters.
