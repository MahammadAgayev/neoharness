# neoharness — handover

Date: 2026-11-09. State: all code works and all tests pass (`go vet`, `go
test -race ./...`, 105 test functions). The first part of the work is committed; the
second part (section 11) is **not committed yet**. See section 10.

## 1. What neoharness is

neoharness lets one person work with many Claude Code sessions at the same
time, like Claude Desktop, but in a terminal.

- A **workspace** is one piece of work with its sessions. You can pin it to
  one local folder.
- Each workspace has one **coordinator**. It is a Claude session. It acts only
  when you write to it. It opens threads, gives them names, sends them
  instructions, and knows the status of the workspace.
- A **thread** is a separate Claude session that does one piece of work. It
  runs in a local folder or in a folder on another machine over ssh.
- Threads report progress to the coordinator, read the workspace status, read
  each other's transcripts, and send messages to each other. You can write to
  every thread directly.

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
:new demo ~/src/demo    create a workspace pinned to a folder, and open it
:new demo               the same, but pick the folder in a fuzzy picker
i                       write to the coordinator; <CR> sends, <Esc> stops
<leader>t               threads pane on the right (<leader> is <Space>); <CR> opens a thread in it
:help                   every key and command
```

- `:new` with no folder opens a folder picker: "not pinned", the folder you
  started in, then every folder up to 6 levels below it. Dot folders,
  `Library` and `node_modules` are skipped (from `~` that is about 9,000
  folders in 0.1 s; with them it was 27,000 in 3.3 s).
- `<Tab>` completes folder paths in `:new <name> <folder>`, `:folder
  <folder>` and the folder of `:open`. With one match, the next `<Tab>` goes
  one level deeper.
- A relative folder (`:new x .`) is made absolute in the TUI, where you typed
  it.
- After `:new`, you start in Insert mode in the coordinator.

Requirements: Go 1.26, `claude` on PATH. The hub logs to
`~/.neoharness/hub.log`. For remote threads: an ssh host alias with key auth,
and `claude` installed and logged in on that host.

## 3. Architecture

```
 neoharness (TUI) ──unix socket, HTTP + SSE──▶ hub (same process)
                                               ├─ state of all workspaces and threads (only writer)
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
  closes, the second starts its own hub (and its own tunnels). Closing the
  first stops its running turns; send again from the other instance.
- Each **turn** is one `claude -p` call:
  `--output-format stream-json --verbose`, then `--session-id <uuid>` on the
  first turn and `--resume <uuid>` after. Also `--mcp-config <json>`,
  `--permission-prompt-tool mcp__neoharness__permission_prompt`,
  `--append-system-prompt <role>`, and `--model` / `--effort` when the thread
  has a choice (see slash commands). Claude keeps the conversation; the hub
  keeps the UUID. No long-lived Claude process. If a turn was stopped after
  Claude made the session but before it replied, Claude refuses the UUID
  ("already in use"); the adapter then retries with `--resume`
  (`internal/agent/claude/claude.go`, `Run`).
- From each turn the adapter also reads: the slash command list and model
  (init record), the context size (last assistant message's usage), the
  context window and cost (result record). The hub saves them per thread.
- **Send now** (`<C-x><C-s>` in Insert, `hub.SendNow`, `"now": true` on the
  messages route): the message is queued and the running turn is stopped, so
  the queue goes in the next turn at once. Claude keeps the stopped turn's
  work in its session (`--resume`). If the turn had no output yet, Claude
  never saw its messages, so they go again with the new one. The chat shows
  "Interrupted." (a note, not an error). Under `-p` text cannot go into a
  running turn, so this is how Claude Code's send-now maps here.
- Messages that arrive while a thread is busy wait in a queue. All queued
  messages go into the next turn as one prompt. Messages from others start
  with `Message from <name>:`. A `/compact` request is always a turn of its
  own.
- **Remote**: the hub keeps `ssh -N -R 47821:127.0.0.1:47821 <host>` open per
  host. A turn runs `ssh <host> -- exec "$SHELL" -lc 'cd <dir> && exec claude …'`
  (login shell, so PATH includes claude). On the remote host the MCP URL is the
  same, through the tunnel. File lists and the git branch of a remote thread
  use the same ssh command (`claude.SSHCommand`, `cmd/neoharness/files.go`).

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
deny (with an optional reason). Keys: `<leader>a` / `<leader>A` / `<leader>x`,
or `:allow` / `:always` / `:deny [reason]`.

- The user's own Claude settings and allow-lists still apply. A command that
  the user already allows does not ask.
- The hub's own tools (`mcp__neoharness__*`) are always allowed.
- "Allow always" stores a rule per thread. For Bash it stores the first word
  of the command. A command with `;`, `&`, `|`, `` ` ``, `<`, `>`, a newline or
  `$(` always asks, so `go test; rm -rf ~` cannot pass on a `go` rule.
- A pending request is denied when its turn ends.

### MCP tools (server `neoharness`)

All sessions: `whoami`, `workspace_status`, `send_message(to, text)`,
`report_progress(summary)`, `ask_user(question)`,
`read_thread(name, tail, query?)`.
Coordinator only: `open_thread(name, brief, location?)`, `resolve_thread(name)`.
Internal: `permission_prompt`.

- `read_thread` returns the last `tail` events of a thread as plain text,
  only those that contain `query` if given. `tail` is required (1–200) and has
  no default: a whole transcript can be larger than the caller's context.
- The bearer token alone says who calls. No tool takes a workspace argument,
  so a thread cannot act for another thread or another workspace.
- A message from `send_message` arrives as the receiver's next turn. It never
  interrupts a running turn; it waits in the queue.

### Thread mentions

When you write `@name` and `name` is a thread of the workspace (the whole
word; `@api,` counts, `@api.go` does not), the hub adds one line to the
prompt: "@name is the thread name in this workspace. Read its transcript with
read_thread if you need it." It never pastes the history; the agent decides
how much to read.

### Slash commands under `-p`

Each turn is a new `claude -p` process. All of this was tested with the real
CLI:

| Command | Under `-p` | What neoharness does |
|---|---|---|
| `/context`, `/usage`, `/cost`, `/config`, `/mcp`, `/doctor`, `/init`, `/model` (no name)… | works, but the answer is only in the result record | the adapter shows the result text when a turn had no assistant message |
| `/clear` | does nothing (the next turn still remembered) | hub: new session UUID and an `EventClear`; the TUI empties the chat at that event. Refused while a turn runs. Not a turn |
| `/model <name>`, `/effort <level>` | lasts one turn ("this session only") | hub keeps it per thread, passes `--model` / `--effort` every turn; `default` goes back. Effort: low, medium, high, xhigh, max. Not a turn |
| `/compact [focus]` | does nothing (context grew) | hub: one turn asks for a summary (`prompts.Compact`); on success the thread gets a new session, and the summary goes before the next message, once (`prompts.Compacted`). Needs a started session |
| `/fast`, `/agents` | not available | Claude's own message shows |
| `!<command>` (shell mode) | not available | hub: runs it in the thread's folder in the user's `$SHELL` (over ssh for a remote thread), shows the output (10 lines, `za` for all, exit code if not 0), and puts `<bash-input>`/`<bash-stdout>` before the next message, once. Not a turn. Limit 10 min, last 30,000 characters kept (`hub.shell`, `runShell` in `files.go`, event `shell`) |

The code is `hub.command` and `hub.clear`. The transcript on disk always keeps
everything.

### Loop guard

A thread runs at most 20 turns in a row that you did not start (for example,
two threads that reply to each other). Then it waits for you. A message from
you resets the count.

## 4. Code map

| Package | What it does |
|---|---|
| `cmd/neoharness` | CLI. `main.go` = dispatch; `hub.go` = starts the hub in this process (API socket, MCP, log, data move), agent factory; `tui.go` = opens the TUI, uses another TUI's hub or starts one, stops it on exit or signal; `files.go` = file list and git branch of a thread's folder, locally or over ssh |
| `internal/model` | Plain shared types: State, Role, Location, Event, ThreadView, WorkspaceView, Completions, Update. Leaf package |
| `internal/workspace` | Disk storage of workspaces, threads, transcripts. Atomic writes. `Migrate` moves data from before the rename. Imports only `model` |
| `internal/hub` | The core: queues, turns, state, routing, permissions, loop guard, slash commands, completions, update fan-out. Knows agents only through `agent.Agent`; gets file lists and branches through injected functions |
| `internal/mcpserver` | MCP tools → hub methods (official Go SDK, stateless HTTP) |
| `internal/hubapi` | Client for the local API (unix socket, SSE). Used by the TUI. Imports only `model` |
| `internal/hubserver` | Server for the local API. Every route is one hub call |
| `internal/tunnel` | ssh reverse tunnel per remote host |
| `internal/tui` | The terminal UI (see section 5) |
| `internal/agent` | Agent interface: one `Run` per turn. Request: `Prompt`, `SystemPrompt`, `Cwd`, `Conversation`, `Resume`, `MCPConfig`, `PermissionPromptTool`, `Env`, `Model`, `Effort`, `OnEvent`. Result: `Text`, `Tools`, `Tokens`, `ExitCode`, `Stderr`, `Commands`, `Model`, `Context`, `ContextWindow`, `Cost` |
| `internal/agent/claude` | Claude Code adapter. `remote.go` builds the ssh command and quoting (`SSHCommand` is exported for `files.go`) |
| `internal/prompts` | Prompts: `coordinator.md`, `thread.md`, `compact.md` |
| `internal/architecture` | Tests that enforce the import rules below |

Import rules (enforced by tests):
- `hub` imports no adapter, TUI, MCP, API server, tunnel, or MCP SDK.
- `tui` imports only `hubapi` and `model` from this module.
- `model` is a leaf; `workspace` and `hubapi` import only `model`.

Local API routes (all under `/workspaces`): list and create workspaces;
`PUT /{w}/folder`; `DELETE /{w}` (soft); threads list and create;
`GET/POST/DELETE /{w}/threads/{t}…` for events, messages, resolve, reopen,
answer, completions, delete; `GET /updates` (SSE).

## 5. The TUI

Neovim fundamentals, not a Neovim plugin. Built on tcell, goldmark (markdown),
chroma (code colors, style `rose-pine-moon`).

- **Everything is a buffer**: workspaces list, threads list, coordinator chat,
  thread chats, messages (`:messages`), help (`:help`). A buffer is a list of
  blocks; each block renders and caches its own lines per width.
- **Windows and splits**: a split tree in `layout.go`. A workspace opens as
  the coordinator chat alone.
- **Threads pane**: `<leader>t` (or `:threads`) opens one full-height pane on
  the right with the threads list, or focuses it and shows the list again.
  The list is grouped by state (waiting, running, idle; the coordinator is not
  listed, `<leader>pc` goes to it); resolved threads fold into `▸ N resolved`, and `<CR>` on that
  row unfolds it. `<CR>` on a thread shows it in the pane; the next one
  replaces it; `<C-^>` goes back to the list. `<C-v>` (list or picker) and
  `:open` also use the pane. `<C-x>` splits above. `<leader>pc` focuses the
  coordinator and leaves the pane as it is (`App.pane`, `toPane`). New vertical
  splits go right, horizontal ones above, like the user's Neovim
  (`splitright`, `nosplitbelow`). The bottom line counts running (`●`) and
  waiting (`◆`) threads.
- **Modes**: Normal, Insert (write in the prompt at the end of a chat buffer),
  Visual (`v`, `V`), Command-line (`:` and `/`), Picker (`<leader>…`).
- **Prompt**: at least 3 lines tall, grows to 12 lines before it scrolls.
  `<CR>` sends, `<C-j>` makes a new line.
- **Completion in Insert** (`complete.go`): `/` at the start of the prompt
  completes slash commands; `@` at the start of a word completes threads
  (marked `#` in the popup, listed first) and files. `<C-n>` / `<C-p>` move,
  `<Tab>` / `<CR>` take, `<Esc>` closes the popup only (it stays closed for
  that word). The text goes to Claude as typed: Claude runs `/cmd` and expands
  `@file` itself (tested), on the machine where the thread runs. The lists
  come from `hub.Completions`, loaded in the background when you enter Insert
  and again when a turn of that thread ends:
  - commands: from Claude's init record, saved per thread, without the
    terminal-only ones; before a thread's first turn, another thread's list in
    the same folder, else on the same host;
  - files: `git ls-files` (tracked and untracked, not ignored), else a walk 6
    levels deep, at most 20,000; over ssh for remote threads; cached 30 s; a
    failure shows as no files;
  - threads: the other threads of the workspace.
- **Status line** (each window): name, state, elapsed; on the right
  `+N queued · branch · model · ctx N% · $cost · ssh:host`. In the window
  with focus: branch Foam, model Rose, ctx Gold (Love from 80%: time to
  `/compact`), cost Pine; other windows are muted. Model, context and cost
  update at the end of each turn; cost adds up per thread (counted from
  2026-11-09 afternoon). The branch is read at start, when a thread opens, and
  after each turn. A narrow window drops branch, then model, then cost;
  context stays. Effort and lines changed are not in Claude's stream.
- **Toasts** (`toast.go`), like snacks.nvim's notifier: top right, at most 3,
  newest on top, only for threads not on screen:

  | Event | Look | How long |
  |---|---|---|
  | waits for permission, an answer, or after a pause | `◆` Gold | until it stops waiting or comes on screen |
  | finished a turn | `✓` Pine, first line of the reply | 4 s |
  | opened by the coordinator | `+` Iris | 4 s |
  | resolved | `✓` muted | 4 s |
  | error | `✗` Love | 8 s |

  Each toast also goes to `:messages`. `<leader>nc` dismisses all,
  `<leader>nh` shows the history.
- **Delete a thread**: `<C-d>` on a thread in a picker (`<leader><leader>`),
  then `y`. It stops the
  turn, denies pending permissions, and removes the thread's folder (hard
  delete). Claude's own session files in `~/.claude` stay. The coordinator
  cannot be deleted. New threads come from the coordinator, not from the
  picker.
- **Workspace folder**: one folder per workspace. `:folder [folder]` changes
  it (no folder: the same picker as `:new`). The coordinator moves there and
  keeps its conversation (tested: Claude resumes a session from another
  folder). Open threads stay where they are; new ones open in the new folder.
- **Delete a workspace**: `<C-d>` on a workspace in the workspaces picker,
  then `y`. Soft delete: it stops its turns, hides it, and sets
  `"deleted": true` in `workspace.json`. Data stays. To restore, set it back
  to `false` and restart. The name stays taken while the data is there.
- **Cursor**: one shape in every mode (the terminal's default block), like the
  user's Neovim (`guicursor = ""`). Insert shows through the Iris `›`.
- **Look**: Rose Pine Moon colors by meaning only. The background is never
  painted, except the cursor line and the selection, so transparent
  terminals stay transparent. `NO_COLOR` is supported.
- **Markdown tables** are padded to column width, with a rule under the
  header; right-aligned columns stay right-aligned. A table wider than the
  window shrinks its widest column first; cut cells end in `…`
  (`markdown.go`, `table`).
- **Speed**: idle, the TUI draws nothing (measured again on 2026-11-09: 0
  bytes in 3 s in Normal, Insert, and the threads picker). Keys and hub
  events draw one frame; a burst draws one frame. While a turn runs, the
  spinner draws a frame every 90 ms (about 11 per second). Toasts that time
  out post one redraw.

Files: `app.go` (state, hub sync, screens, delete and folder actions),
`keys.go` (modes, key sequences, motions, insert, yank), `cmdline.go` (`:`
commands, completion, search, help text), `complete.go` (Insert completion),
`picker.go` (fuzzy pickers), `toast.go` (notifications), `draw.go` (all
drawing, status line), `buffer.go`, `layout.go`, `text.go` (wrapping),
`markdown.go`, `theme.go`.

### Keys

The user's Neovim config is in `~/devenv/nvim` (`~/.config/nvim` links to
it). Read it before adding keys; several keys below copy it.

| Keys | What |
|---|---|
| `i a o A I` / `<CR>` / `<C-j>` / `<Esc>` | write / send / new line / Normal |
| `<C-x><C-s>` (Insert) | send now: stop the running turn |
| `j k gg G h l w b 0 ^ $` | move |
| `<C-d>` `<C-u>` `n` `N` | half page, search; all recenter like `zz` (scrolloff 8) |
| `]m` `[m` / `]b` `[b` / `<C-^>` | next/previous message / thread / alternate |
| `/` `n` `N` | search in the buffer |
| `za` `<leader>e` | open or close a turn's tool calls |
| `v V y yy` `<leader>y` `<leader>Y` | select and yank to the system clipboard (OSC 52) |
| `<C-w>h/j/k/l` `<C-h/j/k/l>` `<Tab>` | move between windows |
| `<C-w>v` `<C-w>s` `<C-w>c` `<C-w>o` `<C-w>=` | split right / above, close, only, equal |
| `<leader><leader>` / `<leader>w` / `<leader>/` | pickers: buffers (threads included) / workspaces / search all transcripts |
| `<leader>t` | threads pane on the right |
| `<leader>pv` / `<leader>pc` | workspaces screen / coordinator |
| `<leader>a` `<leader>A` `<leader>x` | allow / allow always / deny a permission |
| `<leader>nc` / `<leader>nh` | dismiss toasts / their history |
| in pickers: `<CR>` `<C-v>` `<C-x>` `<C-d>` | open, open in the pane, split above, delete (after `y`) |
| in the completion popup: `<C-n>` `<C-p>` `<Tab>` `<CR>` `<Esc>` | move, take, close |

Commands: `:new <name> [folder]`, `:folder [folder]`, `:open <name>
[folder|host:dir]` (opens in a split), `:workspace [name]`, `:workspaces`,
`:threads`, `:b <name>`, `:resolve`, `:reopen`, `:allow`, `:always`, `:deny
[reason]`, `:messages`, `:help`, `:split`, `:vsplit`, `:only`, `:q`, `:qa`
(refuse while threads run), `:q!`, `:qa!`.

### Design rules (agreed with the user; keep them)

1. Minimal: only names, states, the conversation, the permission and question
   cards, the status line and toasts are on screen. Key hints and extra data
   go in `:help`.
2. Vim motions and ex commands, not new shortcuts. Keys that copy the user's
   Neovim config are fine. Example: there is no key to toggle a panel; a list
   is a buffer you open with `:threads` and close with `:q`.
3. Start simple and unfold: a workspace is one coordinator chat; threads come
   on screen only when you open them.
4. A thing shows its own state. No INSERT / COMMAND / PICK labels: the Iris
   `›` shows Insert, the `:` line shows Command, the picker box shows Pick.
   Only a muted `visual` / `v-line` (to tell `v` from `V`). The cursor shape never
   changes.
6. No all-caps labels anywhere (headers, help sections, mode words): the
   user asked for lowercase.
5. Red (Love) only for errors and things that need you (a nearly full context
   counts). One accent (Iris) for "you type here" (prompt caret, `:`). Bold
   only for names with focus.

### Cursor flicker in tmux

Each tcell frame hides the cursor, draws, shows it again, and sends the
cursor shape again (also when it did not change), inside synchronized output
(`?2026`). The user's tmux client (`xterm-ghostty`) has no `sync` feature, so
tmux passes each step on to Ghostty at once, and the cursor can shiver.

Compared in tmux on the user's machine: Claude Code also hides and shows the
cursor per frame, but never sends the cursor shape (0 shape codes while
typing), and it draws few frames. Neovim sends the shape only when the mode
changes. So neoharness now keeps one shape (the terminal default) in every
mode; tcell still sends that same code each frame. Status: changed, but the
user has not confirmed that the shiver is gone.

The full fix is on the user's side (not applied; it is their dotfile):

```
set -as terminal-features 'xterm-ghostty:sync'     # ~/.tmux.conf, then restart tmux fully
```

Other options offered, not done: draw fewer frames while a turn runs; patch
tcell to send the shape only when it changes.

## 6. Data on disk

```
~/.neoharness/hub.sock                                             local API socket (0600)
~/.neoharness/hub.log                                              hub log
~/.neoharness/workspaces/<workspace>/workspace.json                name, pin, created, deleted
~/.neoharness/workspaces/<workspace>/workspace/                    coordinator folder if the workspace is not pinned
~/.neoharness/workspaces/<workspace>/threads/<name>/thread.json    see below (file is 0600: it holds the MCP token)
~/.neoharness/workspaces/<workspace>/threads/<name>/events.jsonl   transcript, append-only
~/.neoharness/projects -> workspaces                               symlink left by the data move
```

`thread.json` fields: name, role, location, sessionId, started, token,
resolved, asking, paused, progress, progressAt, queue, inFlight, rules,
agentTurns, turns, commands (last slash command list), model, context,
contextWindow, cost (total), modelChoice and effort (from `/model` and
`/effort`), summary (left by `/compact` for the next message), shell (`!`
commands waiting for the next message), since,
createdAt, updatedAt. The git branch is not stored; the hub reads it.

Transcript event kinds: message, text, tool, turn, error, permission, answer,
ask, progress, note, clear, shell (twice per command, same id: start, then
output and exit).

If neoharness closes during a turn, the transcript says "neoharness stopped
during this turn". If it was killed (no chance to write that), the next start
writes "this turn was lost". Either way the thread is Idle; send the message
again and the Claude session continues.

**Rename projects → workspaces.** On start, `workspace.Migrate` moves
`~/.neoharness/projects` to `~/.neoharness/workspaces` once, renames each
`project.json` to `workspace.json`, and leaves a `projects` symlink. The
symlink is there because an unpinned coordinator runs inside the data folder,
and its old path must keep working. If both folders exist, start stops with an
error. On the user's machine this move was done by hand on 2026-11-09 with
the same steps; a backup is at `~/.neoharness-backup-20261109-160616.tar.gz`
(delete it when happy).

## 7. Configuration

| Variable | Use |
|---|---|
| `NEOHARNESS_HOME` | root folder instead of `~/.neoharness` (tests use it) |
| `NEOHARNESS_MCP_PORT` | MCP port, default 47821 |
| `NEOHARNESS_CLAUDE_COMMAND` | local claude command, e.g. `authwrap agent run claude` or `claude --model haiku` |
| `NEOHARNESS_REMOTE_CLAUDE_COMMAND` | claude command on remote hosts (default `claude`) |

The hub runs in the TUI's process, so it has the shell's full environment
(PATH, `SSH_AUTH_SOCK`, credentials). Keymaps and theme cannot be configured
yet. Per-thread model and effort are set with `/model` and `/effort`.

## 8. How it was tested

- `go vet ./...` and `go test -race ./...`: all pass (99 tests). Hub, MCP,
  API, and TUI tests need no model and no real terminal (fake agent, fake
  client, tcell simulation screen). TUI tests also check real cell colors and
  the cursor position.
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
- End to end, added on 2026-11-09 (afternoon):
  - `claude -p` expands `@file` by itself (it answered from the file with
    Read, Bash, Grep and Glob turned off);
  - the built-in slash commands, one by one (table in section 3);
  - `/clear`: remember "pelican", `/clear`, ask → "NONE"; the chat emptied;
  - `/compact`: summary turn, new session, the new session still answered
    "pelican"; the summary went only into the first message;
  - `/model sonnet`: the next two turns ran `claude-sonnet-5-5`;
  - `/context`: its report showed in the chat;
  - resuming a session from another folder works (needed by `:folder`);
  - `:folder` and the workspace delete on a scratch home (data on disk
    checked);
  - deleting a thread with `<C-d>` on a copy of the user's data: its folder
    was removed;
  - the data move on a copy of the user's data, then on the real data.

### How to test end to end without touching real data

Run a scratch hub with its own home and MCP port, inside a throwaway tmux
session, and drive it with `tmux send-keys`:

```sh
T=$(mktemp -d); mkdir -p $T/home $T/dir
tmux new-session -d -s nhe2e -x 140 -y 40
tmux set -t nhe2e remain-on-exit on          # keeps an error on screen
tmux respawn-pane -k -t nhe2e "env NEOHARNESS_HOME=$T/home NEOHARNESS_MCP_PORT=47999 NEOHARNESS_CLAUDE_COMMAND='claude --model haiku' ./neoharness"
test -S $T/home/hub.sock || echo "NOT the scratch hub — stop here"
```

To use a copy of the user's data, `cp -R ~/.neoharness/workspaces $T/home/`
first, and check that no thread has a queue (`jq '.queue|length'` on each
`thread.json`), or it starts a real turn.

Pitfalls seen in this work:
- If a hub answers on the real socket, a TUI with the real home uses it.
  Always check the scratch socket exists before you type.
- Wait about 0.5 s after `Escape` before the next key, or tcell reads
  Alt+key and the next command is typed into the prompt and sent.
- After typing a `/` or `@` word in Insert, `<Esc>` closes the popup and stays
  in Insert. Do not send `i` after it.
- `:new` leaves you in Insert mode.
- Use `send-keys -l` for text with special characters.
- Do not reuse a tmux session name at once after `kill-session`; the new
  session can die.
- `claude -p` started by hand waits 3 s for stdin; use `< /dev/null`.
- The user runs neoharness as `./neoharness` from the repo too. Before you
  stop a `neoharness` process, check its port and socket (`lsof -p`); only
  stop the scratch one.
- **Not tested**: a real remote ssh host. The ssh command, quoting, tunnel,
  remote file list and remote branch have unit tests only.

## 9. Known limits and open items

1. **Remote mode works on the user's remote host** (`remote-ssh`),
   tested end to end on 2026-11-09: a remote thread called `whoami` through
   the tunnel, with two neoharness instances, before and after the hub
   owner quit. Three bugs were found and fixed:
   - The tunnel said "ssh exited": the user's ssh config shares one
     connection (ControlMaster), so `ssh -N -R` gave the forward to the master
     and exited at once, and the forward stayed bound on the host. The tunnel
     now runs with `ControlMaster=no`, `ControlPath=none`.
   - `claude` was not found: on the remote host it is in `~/.local/bin`, which only
     the interactive `.zshrc` adds (and that rc starts tmux). The remote
     script now adds `$HOME/.local/bin` to PATH (`remote.go`).
   - Quitting the instance that runs the hub hung, and left an orphan tunnel:
     `ssh` on the user's PATH is a bash wrapper
     (`~/.aw/pex_resources/scripts/binaries/ssh`) that runs `/usr/bin/ssh` as
     a child. The tunnel now runs in its own process group, the whole group is
     killed, and `WaitDelay` bounds the wait (`tunnel.go`; also `files.go`).
   Remote turns get the same flags as local ones (system prompt, MCP config,
   permission tool).
2. Cancelling a remote turn kills the local ssh process. The remote claude
   should stop when its output pipe closes (SIGPIPE). This is not verified.
3. Shift+Enter is the same as Enter in most terminals; use `<C-j>` for a new
   line. `<Esc>` followed very fast by another key reads as Alt+key (tcell).
4. The quit guard counts only Running threads. A thread that waits on a
   permission card also has a turn in flight, and `:qa` does not protect it.
5. Data in `~/.harness/` (from before the first rename) is not moved. Old
   `HARNESS_*` variables are not read.
6. **The `/` list is empty in a folder where no thread has finished a turn on
   the new build.** Offered, not done: the hub starts Claude briefly, reads
   the init record, and stops it before the model is called (about 1–2 s; it
   may leave a session file in `~/.claude`).
7. The status line updates at the end of a turn, not during it. A branch
   switched by hand shows after the thread's next turn.
8. **No restore for a deleted workspace** (edit `workspace.json`). Thread
   delete is hard (no undo); workspace delete is soft. The user asked for
   soft delete "for now".
9. `<leader>pv` rebuilds the layout: opening the workspace again shows the
   coordinator alone; splits are not kept.
10. The cursor shiver in tmux: see section 5. Ask the user whether it is gone.
11. Not built yet: configurable keymaps and theme, a picker for "allow always"
    rules, other agent backends (the agent interface is ready for them),
    more than one folder per workspace (Claude has `--add-dir`; the user
    chose one folder for now).
12. `:threads` is now the same as `<leader>t` (the pane). The threads list
    has no fuzzy search any more; use `/` in it, or `<leader><leader>`.
13. **Auto mode**: the user's `claude` accepts `--permission-mode auto`
    (choices: acceptEdits, auto, bypassPermissions, manual, dontAsk, plan).
    The hub passes no mode (default + permission card). The user said "no
    action for now". If added, first test how auto mode works with
    `--permission-prompt-tool` in `claude -p`.
14. `<leader>t` does not show how long a thread has run. Offered, not done.
15. The workspace `e2e` (pinned to `/tmp/nh-e2e-proj`) is left from an old
    test. Delete it with `<C-d>` in `<leader>w` when the user agrees.

## 10. Repository

- GitHub: https://github.com/MahammadAgayev/neoharness (renamed from
  `harness`; GitHub redirects the old URL). `origin` points to the new URL.
- Local folder: `~/Documents/src/neoharness`.
- Branch `neoharness-hub-tui` (from `main` at `68d5cc9`). `main` itself is not
  changed; merge when ready.
- Commit `fd86dd7`: projects, threads, and the Vim-like TUI in one process.
- **Not committed**: everything in section 11 (31 files, about +2,500 / −650
  lines, including the move of `internal/project` to `internal/workspace`).
  Ask the user before you commit: one commit, or one per feature.
- The approved design plan is at
  `~/.claude/plans/this-was-already-kinda-lively-hanrahan.md`. Changes made
  after it: no pets or emojis, the coordinator names threads, the screen is
  more minimal (no sparkline, no key hints), the API is split into `hubapi` +
  `hubserver` with a `model` package, the hub moved into the TUI process (no
  tmux, no daemon), the old loop was removed, the layout became "one chat,
  split on demand", and everything in section 11.

## 11. Changes in the second session (2026-11-09, afternoon; not committed)

In the order they were made. Details are in the sections above.

1. **`/` and `@` completion in Insert**: popup, command list from Claude's
   init record, files from `git ls-files` (local or ssh), threads. `@name` of
   a thread adds a pointer to `read_thread`. New MCP tool `read_thread` with a
   required `tail`.
2. **Thread delete**: `<C-d>` in a picker, `y` to confirm, hard delete.
3. **Cursor**: one shape in every mode (was a bar in Insert).
4. **Keys from the user's Neovim**: `<leader>y`/`Y` yank; permissions moved
   to `<leader>a`/`A` and later `<leader>x` (was `<leader>y`/`Y`/`n`, which
   could allow a tool by Neovim habit); `<C-d>`/`<C-u>`/`n`/`N` recenter;
   scrolloff 8; vertical splits open right.
5. **Rename projects → workspaces** everywhere: code, package, API, MCP tool
   (`workspace_status`), prompts, TUI, data folder (with the move). `<leader>w`
   opens the workspaces picker; "go to the next waiting thread" was removed.
6. **Status line**: branch, model, context %, cost, colored in the focused
   window.
7. **Slash commands**: result-only output shows; `/clear`, `/model`,
   `/effort`, `/compact` handled by the hub.
8. **Prompt** at least 3 lines, at most 12.
9. **Workspace folder** change (`:folder`) and soft workspace delete.
10. **Toasts**, folded resolved threads in `<leader>t`, `<leader>pv` and
    `<leader>pc`.
13. **`!` shell mode** (section 3, slash command table). Tested end to end:
    a local `!ls && echo $0 && false` (ran in zsh, showed exit 1, and Claude
    answered from the output on the next message), and `!hostname` in a
    thread on the user's remote host.
11. Fixes: the completion list reloads when a turn ends; result text is not
    shown twice.
12. **Threads pane** replaces the `<leader>t` picker (section 5), and **send
    now** with `<C-x><C-s>` (section 3). A stopped turn's tool line no longer
    spins: a chat settles when its thread goes Idle or Resolved. Tested end to
    end on a scratch hub with haiku: send now before any output (both
    messages went again), and while a permission card was open (only the new
    message went; Claude still knew the stopped request; no file written; no
    `claude` process left); the pane, `:open` into it, grouping, the fold, and
    `<leader>pc`.

Things found on the user's machine in this session:
- An old-style hub from before the hub moved into the TUI was still running
  (`neoharness hub` in tmux session `neoharness/hub`, since 12:34). It held
  the MCP port and served the old code. It was stopped with SIGTERM when it
  had no turn running. This cannot happen with the current code: quitting the
  TUI stops its hub.
- The user's tmux has no `sync` feature (section 5).
