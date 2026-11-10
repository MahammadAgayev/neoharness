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

## Environment

| Variable | Use |
|---|---|
| `NEOHARNESS_HOME` | data folder instead of `~/.neoharness` |
| `NEOHARNESS_MCP_PORT` | MCP port, default 47821 |
| `NEOHARNESS_CLAUDE_COMMAND` | local claude command, for example `claude --model haiku` or a wrapper that adds credentials |
| `NEOHARNESS_REMOTE_CLAUDE_COMMAND` | claude command on remote hosts, default `claude` |

The hub runs in the TUI's process, so it has your shell's environment
(PATH, `SSH_AUTH_SOCK`, credentials).

## Remote threads

A thread can run on another machine: `:open <name> host:dir`. You need:

- an ssh host alias with key authentication;
- `claude` installed and logged in on that host.

The hub opens one ssh reverse tunnel per host, so the remote session reaches
the same MCP server.
