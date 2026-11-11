# Configuration

## init.lua

neoharness reads `~/.neoharness/init.lua` when it starts, after the built-in
[`defaults.lua`](../internal/tui/defaults.lua), which lists every default
key, color and setting. Copy a line from there and edit it.

The API is the global `neoharness` table; `require("neoharness")` is the same
table.

```lua
neoharness.o.leader = ","                                 -- default <Space>
neoharness.o.equalalways = false                          -- default true
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
| `neoharness.keymap.set(mode, lhs, rhs, opts)` | `n`, `x` or `i`; `rhs` is keys, an ex command or a function; `{ remap = true }` |
| `neoharness.keymap.del(mode, lhs)` | removes a map |
| `neoharness.hl(group, opts)` | `Text Subtle Muted Iris Rose Foam Gold Pine Love Code Separator CursorLine Selection`; `fg`, `bg`, `bold`, `italic`, `underline` |
| `neoharness.cmd(line)` | runs an ex command |

A map can run keys, an ex command, a `<Plug>(neoharness-...)` action, or a
Lua function. Actions with no ex command:

| Action | Default |
|---|---|
| `<Plug>(neoharness-threads)` | `<leader>pt` |
| `<Plug>(neoharness-grep)` | `<leader>ps` |
| `<Plug>(neoharness-coordinator)` | `<leader>c` |
| `<Plug>(neoharness-yank)` | `<leader>y` |
| `<Plug>(neoharness-dismiss)` | `<leader>nc` |

The colors are Rose Pine Moon. The background is never painted, so
transparent terminals stay transparent. `NO_COLOR` is supported.

## The hub and the agent

`setup()` calls set the hub and the claude agent. A call sets only the keys
it names, so `init.lua` changes what `defaults.lua` set. They take effect
when the hub starts: restart neoharness after a change.

```lua
require("neoharness").setup({
  mcp_port = 47821,                     -- remote hosts reach the hub through a tunnel on it
})

require("neoharness.claude").setup({
  command = "claude",                   -- here; a wrapper that adds credentials works too
  remote_command = "claude",            -- on ssh hosts
  model = "claude-opus-5-5[1m]",        -- for threads with no /model of their own
  effort = "high",                      -- low, medium, high, xhigh, max; leave out for claude's own
  context_window = 400000,              -- ctx % counts up to this; claude itself can go further
})
```

`/model` and `/effort` change a thread's model and effort; `/model default`
goes back to the setting.

## Environment

`NEOHARNESS_HOME` is the data folder, and the folder of `init.lua`, instead
of `~/.neoharness`.

The hub runs in the TUI's process, so it has your shell's environment
(PATH, `SSH_AUTH_SOCK`, credentials).

## Remote threads

A thread can run on another machine: `:open <name> host:dir`. You need:

- an ssh host alias with key authentication;
- `claude` installed and logged in on that host.

The hub opens one ssh reverse tunnel per host, so the remote session reaches
the same MCP server.
