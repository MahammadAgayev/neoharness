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
neoharness.hl("Accent", { fg = "#c4a7e7", ctermfg = 183, bold = true })
neoharness.cmd("workspace demo")                          -- runs after start
```

| | |
|---|---|
| `neoharness.o.leader` | the leader; it moves the default maps |
| `neoharness.o.equalalways` | a split or a close makes the windows the same size |
| `neoharness.o.compactwarn` | past this context percent, the status line shows `ctx 82% · /compact` in red: every turn sends the whole context again, so `/compact` then saves tokens |
| `neoharness.keymap.set(mode, lhs, rhs, opts)` | `n`, `x` or `i`; `rhs` is keys, an ex command or a function; `{ remap = true }` |
| `neoharness.keymap.del(mode, lhs)` | removes a map |
| `neoharness.hl(group, opts)` | `Fg Fg2 Fg3 Accent Wait Alert Run Done Peer Code Separator CursorLine StatusLine StatusLineNC Selection Search CurSearch`; `fg`, `bg`, `ctermfg`, `ctermbg`, `bold`, `italic`, `underline`. The older names `Text Subtle Muted Iris Gold Love Foam Pine Rose` still work |
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

The colors are Rose Pine Moon, its text levels drawn brighter as in the
desktop app, so each reads at its contrast. They go by role, with the app's
names: `Fg` what you read, `Fg2` what you scan, `Fg3` meta, `Accent` where
you type, `Wait` what waits for you, `Alert` errors, and `Run`, `Done` and
`Peer` for glyphs and bars. On a terminal without true color (no
`COLORTERM=truecolor`), each role takes its `ctermfg` from the 256 colors,
chosen so waiting, errors and another session's words stay apart. Links
and artifact names have a dotted underline in `Run`, and the command of a
failed tool call a curly one in `Alert`; a terminal without these draws a
plain underline. The
background is never painted, so transparent terminals stay transparent.
`NO_COLOR` is supported.

## The hub and the agents

`setup()` calls set the hub and the agents. A call sets only the keys it
names, so `init.lua` changes what `defaults.lua` set. They take effect when
the hub starts: run `neoharness hub restart` after a change.

```lua
require("neoharness").setup({
  mcp_port = 47821,                     -- remote hosts reach the hub through a tunnel on it
  notify = "threads",                   -- every thread notifies you; "coordinator": the coordinator decides
  reopen_compact = "ask",               -- the coordinator reopens a thread past compactwarn: "compact", "ask" or "off"
  auto_compact = 90,                    -- past this context %, a session compacts before its next turn; 0 never
  textwidth = 0,                        -- the widest column of chats and artifacts; 0: the full width
  todos = true,                         -- threads keep a task list; false: none, and no todo line or progress
  coordinator = "claude",               -- the agent coordinators run
  attention = "attention",              -- the agent that orders what waits for you by your focus; false: none
  focus_edit = "ask",                   -- its edits of your focus: "ask" you, "allow" (saved at once) or "off"
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
  attention = { kind = "claude", model = "haiku" },  -- in defaults.lua
})
```

The attention agent orders the waits list, which `]q` walks, by your focus
(`:focus`), and writes for each stop why it is there and a brief of what
it asks. Each of its turns is new, with no tools: a few seconds after what
waits changes, it reads the waits and the focus and replies with the order.
`attention = false` turns it off; the hub's own order stands then, as it
does while the agent has not answered. It may also suggest an edit of your
focus, mostly striking through what is done; `focus_edit` says whether the
edit waits for you (`"ask"`), is saved at once as the agent's version
(`"allow"`), or is never asked for (`"off"`).

An agent has a name you choose. `kind` is the adapter that runs it; leave it
out when the name is the kind. `claude` is the only kind for now. By default
the coordinator runs `claude` with opus, and threads run the coordinator's
agent.

In a workspace, `:setlocal coordinator=<agent>` and `threads=<agent>` choose
other agents for it, from each session's next turn. `:setlocal threads&`
goes back to the config, and `:setlocal` alone shows both. Threads with no
agent of their own follow the coordinator's here too. A new model or command
restarts claude with `--resume`, so the conversation goes on.

When the coordinator writes to a resolved thread, the thread comes back with
its session. Every turn sends the whole session again, so a thread whose
context is past `neoharness.o.compactwarn` does what `reopen_compact` says:

- `"ask"` (the default) holds the message and asks you: `/compact` first,
  or send it as it is (`<CR>` on the queued message).
- `"compact"` compacts first, then the message starts the new session.
- `"off"` sends the message.

`:setlocal reopen_compact=<value>` sets it for one workspace.

A session whose context is past `auto_compact` percent (90 by default)
compacts by itself before its next turn, so a thread or a coordinator that
runs for a long time goes on without you. It compacts only between turns,
when a message waits for the session, and never while a permission card or
a question waits. The transcript says `Context 92% full: compacting first.`,
then the message starts the new session from the summary. A session with
nothing to do does not compact. If the summary fails, the session goes on
as it is and does not try again until a `/compact` works. Keep
`compactwarn` below `auto_compact` to see the warning first; `0` turns auto
compact off, and the warning still shows. `:setlocal auto_compact=<n>` sets
it for one workspace.

`textwidth` is the widest column of reading text, in characters: a
chat's stream and an artifact's page, in the TUI and in the app. `0`, the
default, uses the full width of the pane or window. Set it, for example
to `90`, if long lines are hard to read for you. `:setlocal textwidth=<n>`
changes it for one workspace at once, in both clients, and
`:setlocal textwidth&` goes back to the config. The name is Vim's
`'textwidth'`: the most columns of text, where `0` means no limit.

With `todos = true` (the default) the thread prompt asks each thread to
keep a task list for work of more than a few steps. It shows above the
thread's prompt and on its row in the threads list. `todos = false` drops
the request and the lists, so neither shows.

Claude Code's own auto-compact stays as it is. It starts only near the
model's whole window, far past `context_window`, so the hub compacts first.

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
