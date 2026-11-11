# Configuration

## init.lua

neoharness reads `~/.neoharness/init.lua` when it starts. It supports a small
part of Neovim's Lua API. It runs after the built-in
[`defaults.lua`](../internal/tui/defaults.lua), which lists every default key
and color. Copy a line from there and edit it.

```lua
vim.g.mapleader = ","                               -- default <Space>
vim.o.equalalways = false                           -- default true
vim.keymap.set("n", "<leader>t", ":threads<CR>")    -- keys or an ex command
vim.keymap.set("n", "gt", function() vim.cmd("threads") end)
vim.keymap.set("n", "Q", "<leader>t", { remap = true })
vim.keymap.del("n", "<C-d>")
vim.api.nvim_set_hl(0, "Iris", { fg = "#c4a7e7", bold = true })
vim.cmd("workspace demo")                           -- runs after start
vim.notify("loaded")
```

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
