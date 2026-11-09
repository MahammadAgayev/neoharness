# neoharness — handover

Date: 2026-11-09. State: all code works and all tests pass (`go vet`, `go
test -race ./...`). The work is committed; see section 10.

## 1. What neoharness is

neoharness lets one person work with many Claude Code sessions at the same
time, like Claude Desktop, but in a terminal.

- A **project** is a workspace. You can pin it to a local folder.
- Each project has one **coordinator**. It is a Claude session. It acts only
  when you write to it. It opens threads, gives them names, sends them
  instructions, and knows the status of the project.
- A **thread** is a separate Claude session that does one piece of work. It
  runs in a local folder or in a folder on another machine over ssh.
- Threads report progress to the coordinator, read the project status, and
  send messages to each other. You can write to every thread directly.

The agents talk to neoharness through an MCP server that the hub adds to every
Claude session. Nothing must be installed in the user's Claude setup.

neoharness is one program, like Neovim. The hub that runs the sessions lives
inside the TUI process. There is no daemon and no tmux. The old unattended
loop (`init/start/run`) is removed.

## 2. Quick start

```sh
go build -o neoharness ./cmd/neoharness
./neoharness            # opens the TUI; the hub runs inside it
```

In the TUI:

```
:new demo ~/src/demo    create a project pinned to a folder, and open it
:new demo               the same, but pick the folder in a fuzzy picker
i                       write to the coordinator; <CR> sends, <Esc> stops
<leader>t               pick a thread (<leader> is <Space>); <C-v> splits
:help                   every key and command
```

- `:new` with no folder opens a folder picker: "not pinned", the folder you
  started in, then every folder up to 6 levels below it. Dot folders,
  `Library` and `node_modules` are skipped (from `~` that is about 9,000
  folders in 0.1 s; with them it was 27,000 in 3.3 s).
- `<Tab>` completes folder paths in `:new <name> <folder>` and in the folder
  of `:open`. With one match, the next `<Tab>` goes one level deeper.
- A relative folder (`:new x .`) is made absolute in the TUI, where you typed
  it.
- After `:new`, you start in Insert mode in the coordinator.

Requirements: Go 1.26, `claude` on PATH. The hub logs to
`~/.neoharness/hub.log`. For remote threads:
an ssh host alias with key auth, and `claude` installed and logged in on that
host.

## 3. Architecture

```
 neoharness (TUI) ──unix socket, HTTP + SSE──▶ hub (same process)
                                               ├─ state of all projects and threads (only writer)
                                               ├─ one turn at a time per thread → claude -p (local or ssh)
                                               ├─ MCP server http://127.0.0.1:47821/mcp (token = which thread)
                                               └─ one ssh reverse tunnel per remote host
```

- The **hub** is the only part that runs Claude and writes state. It starts
  with the TUI and stops with it. The TUI keeps no state of its own.
- **Closing** stops running turns. `:q` on the last window and `:qa` refuse
  while a thread runs ("N running · :qa! stops them"); `:qa!` quits anyway.
  SIGHUP/SIGTERM also stop the turns first, so no `claude` process is left.
- **Reopening** loses nothing: each thread keeps its Claude session UUID on
  disk, and the next turn continues it with `--resume`. A stopped turn is
  noted in the transcript; send the message again.
- A **second TUI** uses the first one's hub through the socket. When the first
  closes, the second starts its own hub.
- Each **turn** is one `claude -p` call:
  `--output-format stream-json --verbose`, then `--session-id <uuid>` on the
  first turn and `--resume <uuid>` after. Also `--mcp-config <json>` and
  `--permission-prompt-tool mcp__neoharness__permission_prompt`. Claude keeps
  the conversation; the hub keeps the UUID. No long-lived Claude process.
  If a turn was stopped after Claude made the session but before it replied,
  Claude refuses the UUID ("already in use"); the adapter then retries with
  `--resume` (`internal/agent/claude/claude.go`, `Run`).
- Messages that arrive while a thread is busy wait in a queue. All queued
  messages go into the next turn as one prompt. Messages from others start
  with `Message from <name>:`.
- **Remote**: the hub keeps `ssh -N -R 47821:127.0.0.1:47821 <host>` open per
  host. A turn runs `ssh <host> -- exec "$SHELL" -lc 'cd <dir> && exec claude …'`
  (login shell, so PATH includes claude). On the remote host the MCP URL is the
  same, through the tunnel.

### Thread states

| State | Meaning |
|---|---|
| Idle | nothing to do |
| Running | a turn runs, or messages wait in the queue |
| Waiting For Input | a permission request is pending, the agent asked a question (`ask_user`), or the loop guard paused it |
| Resolved | closed by you or the coordinator; messages are refused; `:reopen` brings it back with the same Claude session |

The state is not stored. The hub calculates it from the stored fields each
time (`thread.derive()` in `internal/hub/hub.go`), so it cannot drift.

### Permissions

The hub does not skip permission checks. When Claude needs approval, it calls
the MCP tool `permission_prompt`. The hub holds that call open, sets the thread
to Waiting For Input, and the TUI shows a card. Answers: allow, allow always,
deny (with an optional reason).

- The user's own Claude settings and allow-lists still apply. A command that
  the user already allows does not ask.
- The hub's own tools (`mcp__neoharness__*`) are always allowed.
- "Allow always" stores a rule per thread. For Bash it stores the first word
  of the command. A command with `;`, `&`, `|`, `` ` ``, `<`, `>`, a newline or
  `$(` always asks, so `go test; rm -rf ~` cannot pass on a `go` rule.
- A pending request is denied when its turn ends.

### MCP tools (server `neoharness`)

All sessions: `whoami`, `project_status`, `send_message(to, text)`,
`report_progress(summary)`, `ask_user(question)`.
Coordinator only: `open_thread(name, brief, location?)`, `resolve_thread(name)`.
Internal: `permission_prompt`.

The bearer token alone says who calls. No tool takes a project argument, so a
thread cannot act for another thread or another project.

### Loop guard

A thread runs at most 20 turns in a row that you did not start (for example,
two threads that reply to each other). Then it waits for you. A message from
you resets the count.

## 4. Code map

| Package | What it does |
|---|---|
| `cmd/neoharness` | CLI. `main.go` = dispatch; `hub.go` = starts the hub in this process (API socket, MCP, log), agent factory; `tui.go` = opens the TUI, uses another TUI's hub or starts one, stops it on exit or signal |
| `internal/model` | Plain shared types: State, Role, Location, Event, ThreadView, ProjectView, Update. Leaf package |
| `internal/project` | Disk storage of projects, threads, transcripts. Atomic writes. Imports only `model` |
| `internal/hub` | The core: queues, turns, state, routing, permissions, loop guard, update fan-out. Knows agents only through `agent.Agent` |
| `internal/mcpserver` | MCP tools → hub methods (official Go SDK, stateless HTTP) |
| `internal/hubapi` | Client for the local API (unix socket, SSE). Used by the TUI. Imports only `model` |
| `internal/hubserver` | Server for the local API. Every route is one hub call |
| `internal/tunnel` | ssh reverse tunnel per remote host |
| `internal/tui` | The terminal UI (see section 5) |
| `internal/agent` | Agent interface: one `Run` per turn. Request fields: `Prompt`, `SystemPrompt`, `Cwd`, `Conversation`, `Resume`, `MCPConfig`, `PermissionPromptTool`, `Env` |
| `internal/agent/claude` | Claude Code adapter. `remote.go` builds the ssh command and quoting |
| `internal/prompts` | Session prompts: `coordinator.md`, `thread.md` |
| `internal/architecture` | Tests that enforce the import rules below |

Import rules (enforced by tests):
- `hub` imports no adapter, TUI, MCP, API server, tunnel, or MCP SDK.
- `tui` imports only `hubapi` and `model` from this module.
- `model` is a leaf; `project` and `hubapi` import only `model`.

## 5. The TUI

Neovim fundamentals, not a Neovim plugin. Built on tcell, goldmark (markdown),
chroma (code colors, style `rose-pine-moon`).

- **Everything is a buffer**: projects list, threads list, coordinator chat,
  thread chats, messages (`:messages`), help (`:help`). A buffer is a list of
  blocks; each block renders and caches its own lines per width.
- **Windows and splits**: a split tree in `layout.go`. A project opens as the
  coordinator chat alone; the coordinator says which threads it opened. In a
  picker (`<leader>t`, `<leader><leader>`) or the `:threads` list, `<CR>`
  switches the current window to a thread, `<C-v>` / `<C-x>` open it in a
  vertical / horizontal split. New splits go left or above, as in Neovim.
  The bottom line counts running (`●`) and waiting (`◆`) threads.
- **Modes**: Normal, Insert (write in the prompt at the end of a chat buffer),
  Visual (`v`, `V`), Command-line (`:` and `/`), Picker (`<leader>…`).
- **Look**: Rose Pine Moon colors by meaning only. The background is never
  painted, except the cursor line and the selection, so transparent
  terminals stay transparent. `NO_COLOR` is supported.
- **Markdown tables** are padded to column width, with a rule under the
  header; right-aligned columns stay right-aligned. A table wider than the
  window shrinks its widest column first; cut cells end in `…`
  (`markdown.go`, `table`).
- **Speed**: idle, the TUI draws nothing (measured: 0 bytes in 3 s). Keys and
  hub events draw one frame; a burst draws one frame. While a turn runs, the
  spinner draws a frame every 90 ms (about 11 per second). tcell sends only
  changed cells. A frame mostly copies cached lines; a buffer with a running
  turn renders again each frame.

### Design rules (agreed with the user; keep them)

1. Minimal: only names, states, the conversation, and the permission and
   question cards are on screen. Key hints and extra data go in `:help`.
2. Vim motions and ex commands, not new shortcuts. Example: there is no key
   to toggle a panel; a list is a buffer you open with `:threads` and close
   with `:q`.
3. Start simple and unfold: a project is one coordinator chat; threads come
   on screen only when you open them.
4. A thing shows its own state. No INSERT / COMMAND / PICK labels: the bar
   cursor and the colored `›` show Insert, the `:` line shows Command, the
   picker box shows Pick. Only a muted VISUAL / V-LINE (to tell `v` from `V`).
5. Red (Love) only for errors and things that need you. One accent (Iris) for
   "you type here" (prompt caret, `:`). Bold only for names with focus.

### Cursor flicker in tmux

Each tcell frame hides the cursor, draws, shows it again, and sends the cursor
shape again, inside synchronized output (`?2026`). Ghostty supports that, but
tmux between them does not pass it on unless the client has the `sync`
feature. The user's tmux (client `xterm-ghostty`) has `cstyle` but not `sync`,
so while a turn runs (11 frames/s) the cursor flickers. Fix on the user side:

```
set -as terminal-features 'xterm-ghostty:sync'     # ~/.tmux.conf, then restart tmux
```

Not applied: it is the user's dotfile. Running neoharness outside tmux also
avoids it. Drawing fewer frames while a turn runs was offered, not done.

Files: `app.go` (state, hub sync, screens), `keys.go` (modes, key sequences,
motions, insert, yank), `cmdline.go` (`:` commands, completion, search, help
text), `picker.go` (fuzzy pickers), `draw.go` (all drawing), `buffer.go`,
`layout.go`, `text.go` (wrapping), `markdown.go`, `theme.go`.

Main keys: `i` write · `<CR>` send · `<C-j>` new line · `<Esc>` Normal ·
`<C-w>h/j/k/l` move between windows · `<C-w>v`/`<C-w>s` split (new window
left / above) · `<Tab>` next window · `]b`/`[b` next or previous thread ·
`za` open/close a turn's tool calls · `/` search · `<leader>t` threads ·
`<leader>p` projects · `<leader>/` search all transcripts · `<leader>w` go to
a thread that waits · `<leader>y`/`Y`/`n` allow / always / deny · in pickers
and `:threads`: `<CR>` switch, `<C-v>`/`<C-x>` split ·
`:open <name> [folder|host:dir]` (opens in a split) · `:threads` ·
`:resolve` · `:reopen` · `:q` / `:qa` (refuse while threads run) · `:q!` /
`:qa!`.

## 6. Data on disk

```
~/.neoharness/hub.sock                                    local API socket (0600)
~/.neoharness/projects/<project>/project.json             name, pin, created
~/.neoharness/projects/<project>/workspace/               coordinator folder if the project is not pinned
~/.neoharness/projects/<project>/threads/<name>/thread.json   session UUID, MCP token (file is 0600), queue, rules, state fields
~/.neoharness/projects/<project>/threads/<name>/events.jsonl  transcript, append-only
~/.neoharness/hub.log                                     hub log
```

If neoharness closes during a turn, the transcript says "neoharness stopped
during this turn". If it was killed (no chance to write that), the next start
writes "this turn was lost". Either way the thread is Idle; send the message
again and the Claude session continues.

## 7. Configuration

| Variable | Use |
|---|---|
| `NEOHARNESS_HOME` | root folder instead of `~/.neoharness` (tests use it) |
| `NEOHARNESS_MCP_PORT` | MCP port, default 47821 |
| `NEOHARNESS_CLAUDE_COMMAND` | local claude command, e.g. `authwrap agent run claude` or `claude --model haiku` |
| `NEOHARNESS_REMOTE_CLAUDE_COMMAND` | claude command on remote hosts (default `claude`) |

The hub runs in the TUI's process, so it has the shell's full environment
(PATH, `SSH_AUTH_SOCK`, credentials). Keymaps and theme cannot be configured yet.

## 8. How it was tested

- `go vet ./...` and `go test -race ./...`: all pass. Hub, MCP, API, and TUI
  tests need no model and no real terminal (fake agent, fake client, tcell
  simulation screen).
- End to end with the real `claude` CLI (haiku), on a scratch hub:
  - permission request → card → answer through the API → the turn continued;
  - after a hub restart the coordinator resumed its session and remembered
    its earlier reply;
  - with the hub in the TUI process: quit, reopen, the coordinator remembered
    a word from before the quit; `:qa` refused while a thread ran, `:qa!`
    quit and left no `claude` process; a turn stopped before Claude replied
    did not block the thread (the "already in use" retry);
  - the coordinator opened a thread with `open_thread`; the thread called
    `report_progress`; the progress showed in the threads list;
  - the TUI, run in tmux, showed the layout; a message typed in Insert mode
    reached the thread and the reply showed live;
  - a real reply with a markdown table rendered with aligned columns;
  - the folder picker, launched from `~`, found `~/src/metrics` by typing
    `metrics`.

### How to test end to end without touching real data

Run a scratch hub with its own home and MCP port, inside a throwaway tmux
session, and drive it with `tmux send-keys`:

```sh
tmux new-session -d -s nhe2e -x 110 -y 24 \
  -e NEOHARNESS_HOME=/tmp/nh-e2e -e NEOHARNESS_MCP_PORT=47999 \
  -e "NEOHARNESS_CLAUDE_COMMAND=claude --model haiku" ./neoharness
test -S /tmp/nh-e2e/hub.sock || echo "NOT the scratch hub — stop here"
```

Pitfalls seen in this work:
- Pass each `-e` as its own argument. Putting them in a shell variable (`$E`)
  does not split in zsh; the TUI then used the real `~/.neoharness`.
- If a hub answers on the real socket, a TUI with the real home uses it.
  Always check the scratch socket exists before you type.
- Wait about 0.5 s after `Escape` before the next key, or tcell reads
  Alt+key and `:qa` is typed into the prompt and sent.
- `claude -p` started by hand waits 3 s for stdin; use `< /dev/null`.
- **Not tested**: a real remote ssh host. The ssh command, quoting, and tunnel
  logic have unit tests only.

## 9. Known limits and open items

1. **Remote mode is not tested on a real host.** First thing to try:
   `:open job gpu1:~/proj`.
2. Cancelling a remote turn kills the local ssh process. The remote claude
   should stop when its output pipe closes (SIGPIPE). This is not verified.
3. Shift+Enter is the same as Enter in most terminals; use `<C-j>` for a new
   line. `<Esc>` followed very fast by another key reads as Alt+key (tcell).
4. The quit guard counts only Running threads. A thread that waits on a
   permission card also has a turn in flight, and `:qa` does not protect it.
5. Data in `~/.harness/` (from before the rename) is not moved. Old
   `HARNESS_*` variables are not read.
6. Not built yet: configurable keymaps and theme, a picker for "allow always"
   rules, per-thread model choice, other agent backends (the agent interface
   is ready for them).
7. **Open question: keep `:threads`?** It is the same list as `<leader>t`, as
   a normal buffer (live, can stay in a split). The user now has the bottom
   line counts and `<leader>t`; ask whether `:threads` should stay.
8. **Pending cleanup on the user's machine** (from a test mistake; see the
   pitfalls in section 8):
   - An old-style hub (from before the hub moved into the TUI) still runs in
     tmux session `neoharness/hub` with the old code. New TUIs connect to it
     while it answers and cannot stop it. To switch: check that
     `metrics/pr-efficiency-gains` has no open permission card, close the
     TUI, `tmux kill-session -t neoharness/hub`, start `./neoharness`.
   - Project `e2e` in `~/.neoharness/projects/` (pinned to
     `/tmp/nh-e2e-proj`) was created by that test. Delete
     `~/.neoharness/projects/e2e` and `/tmp/nh-e2e-proj` after the old hub
     is stopped (it holds the project in memory and would write it back).
9. **Auto mode**: the user's `claude` accepts `--permission-mode auto`
   (choices: acceptEdits, auto, bypassPermissions, manual, dontAsk, plan). The
   hub passes no mode (default + permission card). The user said "no action
   for now". If added, first test how auto mode works with
   `--permission-prompt-tool` in `claude -p`.
10. `<leader>t` does not show how long a thread has run. Offered, not done.

## 10. Repository

- GitHub: https://github.com/MahammadAgayev/neoharness (renamed from
  `harness`; GitHub redirects the old URL). `origin` points to the new URL.
- Local folder: `~/Documents/src/neoharness`.
- All work in this handover is in one commit on branch `neoharness-hub-tui`
  (from `main` at `68d5cc9`). `main` itself is not changed; merge when ready.
- The approved design plan is at
  `~/.claude/plans/this-was-already-kinda-lively-hanrahan.md`. Changes made
  after it: no pets or emojis, the coordinator names threads, the screen is
  more minimal (no sparkline, no key hints), and the API is split into
  `hubapi` + `hubserver` with a new `model` package. Later in the same
  session: the hub moved into the TUI process (no tmux, no daemon), the old
  loop was removed (`internal/loop`, `run`, `tmuxctl`, `agent/pi`, worker /
  reviewer / evaluator prompts), the layout became "one chat, split on
  demand", and the design rules in section 5 were agreed.
