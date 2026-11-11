-- neoharness defaults.
--
-- neoharness runs this file first, then ~/.neoharness/init.lua, which can
-- change any of it: copy a line there and edit it. Vim's own keys (motions,
-- operators, :commands, <C-w> windows) are built in and not listed here.
--
-- Maps here take the leader that init.lua sets, so a new vim.g.mapleader
-- moves all of them. A map runs keys, an ex command (":threads<CR>"), a
-- <Plug>(neoharness-...) action, or a Lua function. Maps are noremap unless
-- { remap = true }.

vim.g.mapleader = " "
vim.o.equalalways = true -- after a split or a close, windows get the same size

-- The hub. Its settings take effect when it starts.
require("neoharness").setup({
  mcp_port = 47821,              -- remote hosts reach the hub through a tunnel on it
})

-- The claude agent. /model and /effort change the model and effort per
-- thread; "default" goes back to these. Leave effort out for claude's own.
require("neoharness.claude").setup({
  command = "claude",            -- here; a wrapper works too
  remote_command = "claude",     -- on ssh hosts
  model = "claude-opus-5-5[1m]",
  context_window = 400000,       -- ctx % counts up to this, though the model takes more
})

local map = vim.keymap.set
local hl = function(group, opts) vim.api.nvim_set_hl(0, group, opts) end

-- Pickers.
map("n", "<leader><leader>", ":ls<CR>")                -- buffers
map("n", "<leader>pb", ":ls<CR>")
map("n", "<leader>pt", "<Plug>(neoharness-threads)")
map("n", "<leader>pa", ":artifacts<CR>")
map("n", "<leader>ps", "<Plug>(neoharness-grep)")      -- search every transcript
map("n", "<leader>w", ":workspace<CR>")

-- Windows that are not pickers.
map("n", "<leader>pv", ":workspaces<CR>")              -- workspaces screen
map("n", "<leader>t", ":threads<CR>")                  -- threads pane
map("n", "<leader>c", "<Plug>(neoharness-coordinator)")

-- Permissions: the card of the thread in this window.
map("n", "<leader>a", ":allow<CR>")
map("n", "<leader>A", ":always<CR>")
map("n", "<leader>x", ":deny<CR>")

-- Notifications.
map("n", "<leader>nc", "<Plug>(neoharness-dismiss)")
map("n", "<leader>nh", ":messages<CR>")

-- The system clipboard (OSC 52): the line, or the selection.
map({ "n", "x" }, "<leader>y", "<Plug>(neoharness-yank)")
map({ "n", "x" }, "<leader>Y", "<Plug>(neoharness-yank)")

-- Write to the thread; open or close a turn's tool calls.
map("n", "<leader>r", "i")
map("n", "<leader>e", "za")

-- Windows.
map("n", "<Tab>", "<C-w>w")
map("n", "<S-Tab>", "<C-w>W")
map("n", "<C-h>", "<C-w>h")
map("n", "<C-j>", "<C-w>j")
map("n", "<C-k>", "<C-w>k")
map("n", "<C-l>", "<C-w>l")

-- Colors: Rose Pine Moon (https://rosepinetheme.com/palette). The
-- background stays the terminal's, except the cursor line and selection.
hl("Text", { fg = "#e0def4" })
hl("Subtle", { fg = "#908caa" })
hl("Muted", { fg = "#6e6a86" })
hl("Love", { fg = "#eb6f92" })                         -- errors only
hl("Gold", { fg = "#f6c177" })
hl("Rose", { fg = "#ea9a97" })
hl("Pine", { fg = "#3e8fb0" })
hl("Foam", { fg = "#9ccfd8" })
hl("Iris", { fg = "#c4a7e7" })                         -- where you type
hl("Code", { fg = "#ea9a97" })
hl("Separator", { fg = "#393552" })
hl("CursorLine", { bg = "#2a283e" })
hl("Selection", { bg = "#44415a" })
