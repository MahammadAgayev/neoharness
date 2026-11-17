-- neoharness defaults.
--
-- neoharness runs this file first, then ~/.neoharness/init.lua, which can
-- change any of it: copy a line there and edit it. Vim's own keys (motions,
-- operators, :commands, <C-w> windows) are built in and not listed here.
--
-- Maps here take the leader that init.lua sets, so a new neoharness.o.leader
-- moves all of them. A map runs keys, an ex command (":threads<CR>"), a
-- <Plug>(neoharness-...) action, or a Lua function. Maps are noremap unless
-- { remap = true }.

neoharness.o.leader = " "
neoharness.o.equalalways = true -- after a split or a close, windows get the same size
neoharness.o.compactwarn = 80   -- past this context %, the status line says /compact; 0 never

-- The hub. Its settings take effect when it starts.
require("neoharness").setup({
  mcp_port = 47821,              -- remote hosts reach the hub through a tunnel on it
  notify = "threads",            -- every thread notifies you; "coordinator": the coordinator decides
  reopen_compact = "ask",        -- the coordinator reopens a thread past compactwarn: "compact" first, "ask" you, or "off"
  auto_compact = 90,             -- past this context %, a session compacts before its next turn; 0 never
  textwidth = 0,                 -- the widest column of chats and artifacts; 0: the full width. :setlocal textwidth=<n> in a workspace
  todos = true,                  -- threads keep a task list, shown above the prompt and in the threads list; false: none
  coordinator = "claude",        -- the agent below that coordinators run
  attention = "attention",       -- the agent that orders what waits for you by your focus, so ]q goes to the best next thing; false: none
  focus_edit = "ask",            -- its edits of your focus, such as striking a done line: "ask" you, "allow" (saved at once), or "off"
  -- threads = "claude",         -- the threads' agent; left out: the coordinator's
})

-- Agents, by a name you choose. kind is the adapter that runs one; leave it
-- out when the name is the kind. claude is the only kind for now. Leave
-- effort out for the agent's own. In a workspace, :setlocal coordinator=<name>
-- and threads=<name> choose others; /model and /effort change one session.
require("neoharness.agents").setup({
  claude = {
    command = "claude",          -- here; a wrapper works too
    remote_command = "claude",   -- on ssh hosts
    model = "claude-opus-5-5[1m]",
    context_window = 400000,     -- ctx % counts up to this, though the model takes more
  },
  -- Orders the waits list: short turns with no tools, so a small model.
  attention = { kind = "claude", model = "haiku" },
})

local map = neoharness.keymap.set
local hl = neoharness.hl

-- Pickers.
map("n", "<leader><leader>", ":ls<CR>")                -- buffers
map("n", "<leader>pb", ":ls<CR>")
map("n", "<leader>pt", "<Plug>(neoharness-threads)")       -- every workspace's
map("n", "<leader>pl", "<Plug>(neoharness-local-threads)") -- this workspace's, resolved too
map("n", "<leader>pa", ":artifacts<CR>")
map("n", "<leader>f", ":focus<CR>")                    -- what you work on now, in every workspace
map("n", "<leader>pm", "<Plug>(neoharness-messages)")   -- notifications; a row opens its thread
map("n", "<leader>pq", "<Plug>(neoharness-waits)")      -- the focus list: what to do next, in the attention agent's order
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
map("n", "<leader>T", "<Plug>(neoharness-todo)") -- the todo list above the prompt

-- Windows.
map("n", "<Tab>", "<C-w>w")
map("n", "<S-Tab>", "<C-w>W")
map("n", "<C-h>", "<C-w>h")
map("n", "<C-j>", "<C-w>j")
map("n", "<C-k>", "<C-w>k")
map("n", "<C-l>", "<C-w>l")

-- Colors: Rose Pine Moon (https://rosepinetheme.com/palette), by role, with
-- the brighter Text, Subtle, Muted and Pine the app uses, so each reads at its
-- contrast. ctermfg and ctermbg are for 256-color terminals. The background
-- stays the terminal's, except the cursor line, selection and search.
hl("Fg", { fg = "#ebe9f8", ctermfg = 255 })                -- what you read
hl("Fg2", { fg = "#afacc2", ctermfg = 145 })               -- what you scan
hl("Fg3", { fg = "#9491a9", ctermfg = 246 })               -- meta
hl("Accent", { fg = "#c4a7e7", ctermfg = 183 })            -- where you type
hl("Wait", { fg = "#f6c177", ctermfg = 179 })              -- waits for you
hl("Alert", { fg = "#eb6f92", ctermfg = 204 })             -- errors only
hl("Run", { fg = "#9ccfd8", ctermfg = 116 })
hl("Done", { fg = "#559cb9", ctermfg = 74 })
hl("Peer", { fg = "#ea9a97", ctermfg = 181 })              -- another session's words
hl("Code", { fg = "#ea9a97", ctermfg = 181 })
hl("Separator", { fg = "#393552", ctermfg = 237 })
hl("CursorLine", { bg = "#2a283e", ctermbg = 236 })
hl("StatusLine", { bg = "#393552", ctermbg = 237 })       -- the focused pane's status line
hl("StatusLineNC", { bg = "#2a283e", ctermbg = 236 })     -- the other panes'

hl("Selection", { bg = "#44415a", ctermbg = 238 })
hl("Search", { bg = "#4d4143", ctermbg = 94 })             -- matches while you type / or ?
hl("CurSearch", { fg = "#232136", bg = "#f6c177", ctermfg = 235, ctermbg = 179 }) -- the one the cursor goes to
