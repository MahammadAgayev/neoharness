# Configuration

## init.lua

neoharness reads `~/.neoharness/init.lua` when it starts, after the built-in
[`defaults.lua`](../tui/defaults.lua), which lists every default
key, color and setting. Copy a line from there and edit it.

The API is the global `neoharness` table; `require("neoharness")` is the same
table.

```lua
neoharness.o.leader = ","                                 -- default <Space>
neoharness.o.equalalways = false                          -- default true
neoharness.o.compactwarn = 60                             -- default 80; 0 never
neoharness.keymap.set("n", "<leader>t", ":threads<CR>")   -- keys or an ex command
neoharness.keymap.set("n", "gt", function() neoharness.cmd("threads") end)
neoharness.keymap.set("n", "Q", "<leader>t", { remap = true })
neoharness.keymap.del("n", "<C-d>")
neoharness.hl("Iris", { fg = "#c4a7e7", bold = true })
neoharness.cmd("workspace demo")                          -- runs after start
```

| | |
|---|---|
| `neoharness.o.leader` | the leader; it moves the default maps |
| `neoharness.o.equalalways` | a split or a close makes the windows the same size |
| `neoharness.o.compactwarn` | past this context percent, the status line shows `ctx 82% · /compact` in red: every turn sends the whole context again, so `/compact` then saves tokens |
| `neoharness.keymap.set(mode, lhs, rhs, opts)` | `n`, `x` or `i`; `rhs` is keys, an ex command or a function; `{ remap = true }` |
| `neoharness.keymap.del(mode, lhs)` | removes a map |
| `neoharness.hl(group, opts)` | `Text Subtle Muted Iris Rose Foam Gold Pine Love Code Separator CursorLine Selection Search CurSearch`; `fg`, `bg`, `bold`, `italic`, `underline` |
| `neoharness.cmd(line)` | runs an ex command |

A map can run keys, an ex command, a `<Plug>(neoharness-...)` action, or a
Lua function. Actions with no ex command:

| Action | Default |
|---|---|
| `<Plug>(neoharness-threads)` | `<leader>pt` |
| `<Plug>(neoharness-local-threads)` | `<leader>pl` |
| `<Plug>(neoharness-grep)` | `<leader>ps` |
| `<Plug>(neoharness-messages)` | `<leader>pm` |
| `<Plug>(neoharness-coordinator)` | `<leader>c` |
| `<Plug>(neoharness-yank)` | `<leader>y` |
| `<Plug>(neoharness-dismiss)` | `<leader>nc` |

The colors are Rose Pine Moon. The background is never painted, so
transparent terminals stay transparent. `NO_COLOR` is supported.

## The hub and the agents

`setup()` calls set the hub and the agents. A call sets only the keys it
names, so `init.lua` changes what `defaults.lua` set. They take effect when
the hub starts: run `neoharness hub restart` after a change.

```lua
require("neoharness").setup({
  mcp_port = 47821,                     -- remote hosts reach the hub through a tunnel on it
  notify = "threads",                   -- every thread notifies you; "coordinator": the coordinator decides
  coordinator = "claude",               -- the agent coordinators run
  threads = "quick",                    -- the agent threads run; left out: the coordinator's
})

require("neoharness.agents").setup({
  claude = {
    command = "claude",                 -- here; a wrapper that adds credentials works too
    remote_command = "claude",          -- on ssh hosts
    model = "claude-opus-5-5[1m]",      -- for threads with no /model of their own
    effort = "high",                    -- low, medium, high, xhigh, max; leave out for claude's own
    context_window = 400000,            -- ctx % counts up to this; claude itself can go further
  },
  quick = { kind = "claude", model = "sonnet" },
})
```

An agent has a name you choose. `kind` is the adapter that runs it; leave it
out when the name is the kind. `claude` is the only kind for now. By default
the coordinator runs `claude` with opus, and threads run the coordinator's
agent.

In a workspace, `:setlocal coordinator=<agent>` and `threads=<agent>` choose
other agents for it, from each session's next turn. `:setlocal threads&`
goes back to the config, and `:setlocal` alone shows both. Threads with no
agent of their own follow the coordinator's here too. A new model or command
restarts claude with `--resume`, so the conversation goes on.

`require("neoharness.claude").setup({...})` still works: it sets the entry
`claude`.

`/model` and `/effort` change a thread's model and effort; `/model default`
goes back to the setting. With no name they list the choices: the models
come from `claude` itself, with the version each runs now.

## Environment

`NEOHARNESS_HOME` is the data folder, and the folder of `init.lua`, instead
of `~/.neoharness`.

The hub gets the environment of the shell that started it (PATH,
`SSH_AUTH_SOCK`, credentials). After you change them, run
`neoharness hub restart` from a shell that has the new ones.

## Remote threads

A thread can run on another machine: `:open <name> host:dir`. You need:

- an ssh host alias with key authentication;
- `claude` installed and logged in on that host.

The hub opens one ssh reverse tunnel per host, so the remote session reaches
the same MCP server.
