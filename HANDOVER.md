# neoharness — handover

Date: 2026-11-09 (evening). State: all code works and all tests pass (`go
vet ./...`, `go test -race ./...`: 114 test functions, 116 with subtests).
Work up to commit `4b5e448` is committed. The third session's work (section
11.3: artifacts, layout, frame buffer, auto mode) is **not committed yet**.
Ask the user before you commit.

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
  each other's transcripts, send messages to each other, and write
  **artifacts** (markdown documents for the person). You can write to every
  thread directly.

The agents talk to neoharness through an MCP server that the hub adds to every
Claude session. Nothing must be installed in the user's Claude setup.

neoharness is one program, like Neovim. The hub that runs the sessions lives
inside the TUI process. There is no daemon and no tmux.

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
<leader>pa              artifacts; <CR> on a ◇ line or an artifact name opens one too
:help                   every key and command
```

- A workspace opens with the coordinator on the left and the threads pane on
  the right.
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

Requirements: Go 1.26, `claude` on PATH (tested with Claude Code 2.1.311).
The hub logs to `~/.neoharness/hub.log`. For remote threads: an ssh host alias
with key auth, and `claude` installed and logged in on that host.

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
- **More than one instance**: a second TUI uses the first one's hub through
  the socket. When the first closes, another starts its own hub (and its own
  tunnels). Tested with two instances and a remote thread. Closing the hub
  owner stops its running turns; send again from the other instance.
- Each **turn** is one `claude -p` call:
  `--output-format stream-json --verbose`, then `--session-id <uuid>` on the
  first turn and `--resume <uuid>` after. Also `--mcp-config <json>`,
  `--permission-prompt-tool mcp__neoharness__permission_prompt`,
  `--permission-mode auto`, `--append-system-prompt <role>`, and `--model` /
  `--effort` when the thread has a choice. Claude keeps the conversation; the
  hub keeps the UUID. No long-lived Claude process. If a turn was stopped
  after Claude made the session but before it replied, Claude refuses the UUID
  ("already in use"); the adapter then retries with `--resume`
  (`internal/agent/claude/claude.go`, `Run`).
- From each turn the adapter also reads: the slash command list and model
  (init record), the context size (last assistant message's usage), the
  context window and cost (result record). The hub saves them per thread.
- Messages that arrive while a thread is busy wait in a queue. All queued
  messages go into the next turn as one prompt. Messages from others start
  with `Message from <name>:`. A `/compact` request is always a turn of its
  own.
- **Send now** (`<C-x><C-s>` in Insert, `hub.SendNow`, `"now": true` on the
  messages route): the message is queued and the running turn is stopped, so
  the queue goes in the next turn at once. Claude keeps the stopped turn's
  work in its session. If the turn had no output yet, Claude never saw its
  messages, so they go again with the new one. The chat shows "Interrupted."
  (a note, not an error). Under `-p` text cannot go into a running turn, so
  this is how Claude Code's send-now maps here.
- **Remote**: per host, the hub keeps `ssh -N -o ControlMaster=no -o
  ControlPath=none -R 47821:127.0.0.1:47821 <host>` open, in its own process
  group. A tunnel that drops is opened again every 5 s, up to 12 times
  (`tunnel.reopen`). A turn runs `ssh <host> -- exec "$SHELL" -lc 'export
  PATH="$HOME/.local/bin:$PATH"; cd <dir> && exec claude …'`. On the remote
  host the MCP URL is the same, through the tunnel. File lists, the git
  branch and `!` commands of a remote thread use the same ssh command
  (`claude.SSHCommand`, `cmd/neoharness/files.go`).

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

Every turn runs with `--permission-mode auto` (the user asked for it):
Claude decides what is safe by itself. Tested: a file write in the folder and
an explicit `rm -rf ~/<missing folder>` both ran without a card. The card
below is for what auto mode chooses to ask; that path was not seen in tests.

The hub does not skip permission checks. When Claude asks for approval, it
calls the MCP tool `permission_prompt`. The hub holds that call open, sets
the thread to Waiting For Input, and the TUI shows a card. Answers: allow,
allow always, deny (with an optional reason). Keys: `<leader>a` / `<leader>A`
/ `<leader>x`, or `:allow` / `:always` / `:deny [reason]`.

- The user's own Claude settings and allow-lists still apply.
- The hub's own tools (`mcp__neoharness__*`) are always allowed.
- "Allow always" stores a rule per thread. For Bash it stores the first word
  of the command. A command with `;`, `&`, `|`, `` ` ``, `<`, `>`, a newline or
  `$(` always asks, so `go test; rm -rf ~` cannot pass on a `go` rule.
- A pending request is denied when its turn ends.

### MCP tools (server `neoharness`)

All sessions: `whoami`, `workspace_status` (threads and artifacts),
`send_message(to, text)`, `report_progress(summary)`, `ask_user(question)`,
`read_thread(name, tail, query?)`, `write_artifact(name, content)`,
`read_artifact(name)`.
Coordinator only: `open_thread(name, brief, location?)`, `resolve_thread(name)`.
Internal: `permission_prompt`.

- `read_thread` returns the last `tail` events of a thread as plain text,
  only those that contain `query` if given. `tail` is required (1–200) and has
  no default: a whole transcript can be larger than the caller's context.
- The bearer token alone says who calls. No tool takes a workspace argument,
  so a thread cannot act for another thread or another workspace.
- A message from `send_message` arrives as the receiver's next turn. It never
  interrupts a running turn; it waits in the queue.
- The prompts (`internal/prompts/*.md`) tell agents to put documents meant
  for the person in an artifact and reply with one line.

### Artifacts

Markdown documents an agent writes for the person (plans, designs,
reports), like claude.ai artifacts. They belong to the **workspace**: every
session can list (`workspace_status`), read and replace them; the writer is
only recorded.

- `write_artifact(name, content)` creates or replaces one; each write is a new
  version, and the whole document is sent each time. The title is the first
  `#` heading, else the name. Limit 1 MB (it travels in one MCP call, over the
  tunnel for a remote thread). The name follows the thread name rules, so it
  is safe as a file name. Agents cannot delete an artifact.
- The writer's transcript gets an `artifact` event, shown as `◇ Title ·
  artifact v2`. Every whole-word mention of an artifact name in a chat is
  drawn as a link (Foam, underlined); `cache-designer` is not a link for
  `cache-design`.
- Opening: `<CR>` on a `◇` line, or on a line with a link (the name under the
  cursor, else the first), or `<leader>pa` / `:artifacts` / `<leader><leader>`.
  It opens a **popup**: a bordered float over the layout, the document on the
  left (60%), the writer's chat on the right (40%), where you can write to it.
  While it is open, the popup is `App.root` and the layout is `App.under`
  (still drawn and live), so window commands work inside it. `q` or `<Esc>`
  in Normal, or `:q` on its last window, closes it and gives the focus back;
  `<leader>pc` and `<leader>t` close it first.
- The open document reloads when it is written again (`UpdateArtifact`).
  `y`, `/`, `]m`/`[m` (headings) work.
- **Editing** (`editor.go`): `:e` or `i a I A o O` in a document shows its
  markdown source in the same window, at the source line of the block under
  the cursor (`Line.Src`, set by `mdRenderer.block`). Lines soft-wrap without
  losing a rune (`wrapRunes`), so a row maps back to a source offset
  (`Line.Off`); the window keeps its own row and column, and every edit reads
  the window's position first (`fromWindow`) and sets it after (`toWindow`).
  Normal: motions, `x dd D p P u <C-r>`, `<CR>` moves down. Insert: the
  prompt's Iris `›` in the margin of the cursor row. The status line shows
  `[+]` while unsaved. `:w` saves (`EditArtifact`, base version checked;
  `:w!` saves over a newer one), `:wq`/`:x` save and show the document,
  `<Esc>`/`q`/`:q` show the document unless changes would be lost, `:q!`
  drops them, `:e!` takes the newest version. Closing the popup keeps the
  edit in the buffer; `:qa` refuses while an edit is unsaved. A write by the
  agent during an edit shows a Gold notice.
- **What the agent hears**: `Hub.EditArtifact` saves the person's version
  (`UserEdit`, "edited by you" in the meta line) and keeps, on the writer
  thread, the text it last saw (`Thread.Edits`). The next turn's prompt,
  whoever sends the message, starts with one note per artifact: a unified
  diff with 1 line of context, each hunk headed by its markdown heading
  (`diff.go`, `prompts.ArtifactEdited`). Saves before that add up to one
  diff. A diff over 6 KB becomes counts and section names, with a pointer to
  `read_artifact`. A `write_artifact` while an edit is unseen is refused,
  and the refusal carries the diff, so the person's edit is never silently
  overwritten. An edit starts no turn; the writer's chat gets a note.
- Rendering (`renderDocument` in `markdown.go`): a meta line (name, writer,
  version, time), text column at most 96 wide, headings by level (Iris, Foam,
  Rose, then Subtle; rules under levels 1 and 2), task boxes without bullets,
  bullets by depth (• ◦ ▪), numbers aligned on the dot, GitHub alerts
  (`> [!NOTE]`, `[!TIP]`, `[!IMPORTANT]`, `[!WARNING]`, `[!CAUTION]`) with a
  colored bar and label, code with a language label and a `│` gutter, link
  URLs after the text, and table cells that wrap instead of being cut.
- Outline rail (`drawRail` in `draw.go`): in a document window 60 or more
  columns wide, a 5-column gutter on the left shows one dash per heading
  (`───` level 1, `──` level 2, `─` deeper), centered in the height. The
  cursor's section is a heavier `━` in Subtle, the others are in the
  separator color. With fewer than 2 headings there is no rail.
- ssh: the text arrives through MCP, so nothing is fetched from the host,
  and an artifact opens when the host is down.

### Thread mentions

When you write `@name` and `name` is a thread of the workspace (the whole
word; `@api,` counts, `@api.go` does not), the hub adds one line to the
prompt: "@name is the thread name in this workspace. Read its transcript with
read_thread if you need it." It never pastes the history.

### Slash commands and `!` under `-p`

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

The code is `hub.command`, `hub.clear` and `hub.shell`. The transcript on
disk always keeps everything.

### Loop guard

A thread runs at most 20 turns in a row that you did not start (for example,
two threads that reply to each other). Then it waits for you. A message from
you resets the count.

## 4. Code map

| Package | What it does |
|---|---|
| `cmd/neoharness` | CLI. `main.go` = dispatch; `hub.go` = starts the hub in this process (API socket, MCP, log, data move), agent factory; `tui.go` = opens the TUI, uses another TUI's hub or starts one, stops it on exit or signal; `files.go` = file list, git branch and `!` commands in a thread's folder, locally or over ssh |
| `internal/model` | Plain shared types: State, Role, Location, Event, Artifact, ThreadView, WorkspaceView, Completions, Update. Leaf package |
| `internal/workspace` | Disk storage of workspaces, threads, transcripts, artifacts. Atomic writes. `Migrate` moves data from before the rename. Imports only `model` |
| `internal/hub` | The core: queues, turns, state, routing, permissions, loop guard, slash commands, `!`, artifacts, completions, update fan-out. Knows agents only through `agent.Agent`; gets file lists, branches and the shell through injected functions (`Files`, `Branch`, `Shell`) |
| `internal/mcpserver` | MCP tools → hub methods (official Go SDK, stateless HTTP) |
| `internal/hubapi` | Client for the local API (unix socket, SSE). Used by the TUI. Imports only `model` |
| `internal/hubserver` | Server for the local API. Every route is one hub call |
| `internal/tunnel` | ssh reverse tunnel per remote host: own process group, no ControlMaster, reopens after a drop |
| `internal/tui` | The terminal UI (see section 5) |
| `internal/agent` | Agent interface: one `Run` per turn. Request: `Prompt`, `SystemPrompt`, `Cwd`, `Conversation`, `Resume`, `MCPConfig`, `PermissionPromptTool`, `PermissionMode`, `Env`, `Model`, `Effort`, `OnEvent`. Result: `Text`, `Tools`, `Tokens`, `ExitCode`, `Stderr`, `Commands`, `Model`, `Context`, `ContextWindow`, `Cost` |
| `internal/agent/claude` | Claude Code adapter. `remote.go` builds the ssh command and quoting (`SSHCommand` is exported for `files.go`) |
| `internal/prompts` | Prompts: `coordinator.md`, `thread.md`, `compact.md`; `Shell` formats a `!` command for the next message |
| `internal/architecture` | Tests that enforce the import rules below |

Import rules (enforced by tests):
- `hub` imports no adapter, TUI, MCP, API server, tunnel, or MCP SDK.
- `tui` imports only `hubapi` and `model` from this module.
- `model` is a leaf; `workspace` and `hubapi` import only `model`.

Local API routes (all under `/workspaces`): list and create workspaces;
`PUT /{w}/folder`; `DELETE /{w}` (soft); `GET /{w}/artifacts` and
`GET /{w}/artifacts/{a}`; threads list and create;
`GET/POST/DELETE /{w}/threads/{t}…` for events, messages (with `now`),
resolve, reopen, answer, completions, delete; `GET /updates` (SSE).

## 5. The TUI

Neovim fundamentals, not a Neovim plugin. Built on tcell, goldmark (markdown),
chroma (code colors, style `rose-pine-moon`).

- **Everything is a buffer**: workspaces list, threads list, coordinator chat,
  thread chats, artifacts, messages (`:messages`), help (`:help`). A buffer is
  a list of blocks; each block renders and caches its own lines per width.
- **Layout**: a split tree in `layout.go`. A workspace opens with the
  coordinator on the left, focused, and the threads pane on the right, half
  and half. New
  vertical splits go right, horizontal ones above, like the user's Neovim
  (`splitright`, `nosplitbelow`). The bottom line counts running (`●`) and
  waiting (`◆`) threads.
- **Threads pane** (`App.pane`, `toPane`): `<leader>t` or `:threads` focuses
  it and shows the threads list; if it was closed, it opens again on the right
  at full height. The list is grouped by state (waiting, running, idle; the
  coordinator is not listed); resolved threads fold into `▸ N resolved`, and
  `<CR>` on that row unfolds it. `<CR>` on a thread shows it in the pane; the
  next one replaces it; `<C-^>` goes back to the list. `<C-v>` (list or
  picker) and `:open` also use the pane. `<C-x>` splits above. `<leader>pc`
  focuses the coordinator and leaves the pane as it is.
- **Artifact popup**: see section 3, Artifacts.
- **Modes**: Normal, Insert (write in the prompt at the end of a chat buffer),
  Visual (`v`, `V`), Command-line (`:` and `/`), Picker (`<leader>…`).
- **Prompt**: at least 3 lines tall, grows to 12 lines before it scrolls.
  `<CR>` sends, `<C-j>` makes a new line, `<C-x><C-s>` sends now.
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
  update at the end of each turn; cost adds up per thread. The branch is read
  at start, when a thread opens, and after each turn. A narrow window drops
  branch, then model, then cost; context stays.
- **Spinners**: a running turn's tool calls spin. Only the newest tool block
  spins; a turn's end or an error stops every spinner in that chat, and so
  does the thread going Idle or Resolved (a stopped turn sends no turn end).
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
  then `y`. It stops the turn, denies pending permissions, and removes the
  thread's folder (hard delete). Claude's own session files in `~/.claude`
  stay. The coordinator cannot be deleted.
- **Workspace folder**: one folder per workspace. `:folder [folder]` changes
  it (no folder: the same picker as `:new`). The coordinator moves there and
  keeps its conversation. Open threads stay where they are; new ones open in
  the new folder.
- **Delete a workspace**: `<C-d>` on a workspace in the workspaces picker,
  then `y`. Soft delete: it stops its turns, hides it, and sets
  `"deleted": true` in `workspace.json`. Data stays. To restore, set it back
  to `false` and restart.
- **Cursor**: drawn by neoharness as a reverse-video block in every mode
  (like the user's Neovim, `guicursor = ""`); the terminal's own cursor stays
  hidden. See "Frames and the cursor" below.
- **Look**: Rose Pine Moon colors by meaning only. The background is never
  painted, except the cursor line, the selection and the cursor cell, so
  transparent terminals stay transparent. `NO_COLOR` is supported.
- **Markdown tables** in a chat are padded to column width, with a rule under
  the header; a table wider than the window shrinks its widest column first
  and cuts cells with `…`. In an artifact, cells wrap instead.
- **Speed**: idle, the TUI draws nothing. Keys and hub events draw one frame;
  a burst draws one frame. While a turn runs, the spinner draws a frame every
  90 ms, and only changed cells go to the terminal (one cell per spinner
  frame).

Files: `app.go` (state, hub sync, screens, layout, pane, popup, artifacts,
links), `keys.go` (modes, key sequences, motions, insert, yank), `cmdline.go`
(`:` commands, completion, search, help text), `complete.go` (Insert
completion), `picker.go` (fuzzy pickers), `toast.go` (notifications),
`draw.go` (all drawing, status line), `canvas.go` (frame buffer and drawn
cursor), `buffer.go` (blocks, events, `!` and artifact lines), `layout.go`,
`text.go` (wrapping), `markdown.go` (chat and document rendering),
`theme.go`.

### Keys

The user's Neovim config is in `~/devenv/nvim` (`~/.config/nvim` links to
it). Read it before adding keys; several keys below copy it. Pickers there
are `<leader>p…` (`pf`, `pb`, `ps`), and `<leader>f` formats code.

| Keys | What |
|---|---|
| `i a o A I` / `<CR>` / `<C-j>` / `<Esc>` | write / send / new line / Normal |
| `<C-x><C-s>` (Insert) | send now: stop the running turn |
| `!<command>` (Insert, at the start) | run it in the thread's folder |
| `j k gg G h l w b 0 ^ $` | move |
| `<C-d>` `<C-u>` `n` `N` | half page, search; all recenter like `zz` (scrolloff 8) |
| `]m` `[m` / `]b` `[b` / `<C-^>` | next/previous message (heading in an artifact) / thread / alternate |
| `<CR>` on `◇` or an artifact name | open the artifact popup |
| `q` / `<Esc>` (Normal, popup open) | close the popup |
| `/` `n` `N` | search in the buffer |
| `za` `<leader>e` | open or close a turn's tool calls, or a long `!` output |
| `v V y yy` `<leader>y` `<leader>Y` | select and yank to the system clipboard (OSC 52) |
| `<C-w>h/j/k/l` `<C-h/j/k/l>` `<Tab>` | move between windows |
| `<C-w>v` `<C-w>s` `<C-w>c` `<C-w>o` `<C-w>=` | split right / above, close, only, equal |
| `<leader><leader>` / `<leader>w` / `<leader>/` | pickers: buffers (threads and artifacts included) / workspaces / search all transcripts |
| `<leader>t` | threads pane on the right |
| `<leader>pa` | artifacts picker |
| `<leader>pv` / `<leader>pc` | workspaces screen / coordinator |
| `<leader>a` `<leader>A` `<leader>x` | allow / allow always / deny a permission |
| `<leader>nc` / `<leader>nh` | dismiss toasts / their history |
| in pickers: `<CR>` `<C-v>` `<C-x>` `<C-d>` | open, open in the pane, split above, delete (after `y`) |
| in the completion popup: `<C-n>` `<C-p>` `<Tab>` `<CR>` `<Esc>` | move, take, close |

Commands: `:new <name> [folder]`, `:folder [folder]`, `:open <name>
[folder|host:dir]` (opens in the pane), `:workspace [name]`, `:workspaces`,
`:threads`, `:artifacts`, `:b <name>`, `:resolve`, `:reopen`, `:allow`,
`:always`, `:deny [reason]`, `:messages`, `:help`, `:split`, `:vsplit`,
`:only`, `:q`, `:qa` (refuse while threads run), `:q!`, `:qa!`.

### Design rules (agreed with the user; keep them)

1. Minimal: only names, states, the conversation, the permission and question
   cards, the status line and toasts are on screen. Key hints and extra data
   go in `:help`.
2. Vim motions and ex commands, not new shortcuts. Keys that copy the user's
   Neovim config are fine.
3. Start simple and unfold: a workspace opens as the threads list and the
   coordinator; a thread comes on screen only when you open it; an artifact
   opens in a popup and goes away with `q`.
4. A thing shows its own state. No mode labels: the Iris `›` shows Insert,
   the `:` line shows Command, the picker box shows Pick. Only a muted
   `visual` / `v-line` (to tell `v` from `V`). The cursor shape never
   changes.
5. Red (Love) only for errors and things that need you (a nearly full context
   counts). One accent (Iris) for "you type here" (prompt caret, `:`, picker
   and popup borders). Bold only for names with focus.
6. No all-caps labels anywhere (headers, help sections, mode words).

### Frames and the cursor

The user saw the cursor shiver while tool calls spun (tmux on Ghostty; the
user's tmux has no `sync` feature, so it passes each step of a frame to the
terminal at once). Measured with `tmux pipe-pane`: each spinner frame (11 per
second) sent about 1.6 KB, 36 KB in 2 s, and hid, showed and restyled the
cursor. Cause: `draw()` calls `Clear()` every frame, and tcell marks every
cleared cell dirty, so all text on screen went out again.

Fix (`canvas.go`): a frame buffer in front of tcell passes on only the cells
that changed since the last frame, and draws the cursor itself while the
terminal's cursor stays hidden. After: 1.6 KB in 2 s, 0 cursor shows, 0
cursor shape codes; a spinner frame is one cell. A test counts the cells
sent per frame. The tmux `sync` setting is no longer needed for this.

For comparison (measured in the second session): Claude Code also hides and
shows the cursor each frame, but never sends the cursor shape, and draws few
frames.

## 6. Data on disk

```
~/.neoharness/hub.sock                                             local API socket (0600)
~/.neoharness/hub.log                                              hub log
~/.neoharness/workspaces/<workspace>/workspace.json                name, pin, created, deleted
~/.neoharness/workspaces/<workspace>/workspace/                    coordinator folder if the workspace is not pinned
~/.neoharness/workspaces/<workspace>/threads/<name>/thread.json    see below (file is 0600: it holds the MCP token)
~/.neoharness/workspaces/<workspace>/threads/<name>/events.jsonl   transcript, append-only
~/.neoharness/workspaces/<workspace>/artifacts/<name>.md / .json   artifact text / name, title, writer, version, times
~/.neoharness/projects -> workspaces                               symlink left by the data move
```

`thread.json` fields: name, role, location, sessionId, started, token,
resolved, asking, paused, progress, progressAt, queue, inFlight, rules,
agentTurns, turns, commands (last slash command list), model, context,
contextWindow, cost (total), modelChoice and effort (from `/model` and
`/effort`), summary (left by `/compact` for the next message), shell (`!`
commands waiting for the next message), since, createdAt, updatedAt. The git
branch is not stored; the hub reads it.

Transcript event kinds: message, text, tool, turn, error, permission, answer,
ask, progress, note, clear, artifact (name, title, version), shell (twice per
command, same id: start, then output and exit).

If neoharness closes during a turn, the transcript says "neoharness stopped
during this turn". If it was killed (no chance to write that), the next start
writes "this turn was lost". Either way the thread is Idle; send the message
again and the Claude session continues.

**Rename projects → workspaces.** On start, `workspace.Migrate` moves
`~/.neoharness/projects` to `~/.neoharness/workspaces` once, renames each
`project.json` to `workspace.json`, and leaves a `projects` symlink (an
unpinned coordinator runs inside the data folder, and its old path must keep
working). If both folders exist, start stops with an error. On the user's
machine this was done on 2026-11-09; a backup is at
`~/.neoharness-backup-20261109-160616.tar.gz` (delete it when happy).

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

- `go vet ./...` and `go test -race ./...`: all pass (114 test functions).
  Hub, MCP, API, and TUI tests need no model and no real terminal (fake
  agent, fake client, tcell simulation screen). TUI tests also check real
  cell colors, the drawn cursor, and the cells sent per frame.
- End to end with the real `claude` CLI (haiku), always on a scratch hub (see
  below):
  - first session: permission card round trip; resume after a hub restart;
    quit and reopen; `:qa` guard and `:qa!` leaving no `claude` process; the
    "already in use" retry; `open_thread` and `report_progress`; live replies;
    markdown tables; the folder picker;
  - second session: `@file` expansion under `-p`; each built-in slash command;
    `/clear`, `/compact`, `/model`, `/context`; resume from another folder;
    `:folder` and workspace delete; thread delete; the data move;
  - third session:
    - send now before any output (both messages went again) and while a
      permission card was open (only the new message; Claude still knew the
      stopped request; no file written);
    - the threads pane: `:open` into it, grouping, the fold, `<leader>pc`;
    - remote on the user's remote host: `whoami` through the tunnel, with two
      instances, before and after the hub owner quit (clean takeover, no
      orphan ssh);
    - `!` locally (`!ls && echo $0 && false`: zsh, exit 1, Claude answered
      from the output) and on the remote host (`!hostname`);
    - artifacts: the coordinator wrote a design doc (every element rendered);
      a remote host thread wrote one and updated it while open (v2 showed at once);
      the popup layout;
    - frames: bytes and cursor codes per spinner frame, before and after the
      fix;
    - auto mode: a file write and an explicit `rm -rf` of a missing folder ran
      without a card.

### How to test end to end without touching real data

Run a scratch hub with its own home and MCP port, inside a throwaway tmux
session, and drive it with `tmux send-keys`:

```sh
T=$(mktemp -d); mkdir -p $T/home $T/dir
tmux new-session -d -s nhe2e -x 160 -y 45
tmux set -t nhe2e remain-on-exit on          # keeps an error on screen, and the session after :qa!
tmux respawn-pane -k -t nhe2e "env NEOHARNESS_HOME=$T/home NEOHARNESS_MCP_PORT=47999 NEOHARNESS_CLAUDE_COMMAND='claude --model haiku' ./neoharness"
test -S $T/home/hub.sock || echo "NOT the scratch hub — stop here"
```

To see what the TUI sends to the terminal: `tmux pipe-pane -t nhe2e -o "cat >
$T/out.bin"`, wait, then `tmux pipe-pane -t nhe2e`.

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
- Without `remain-on-exit`, `:qa!` ends the tmux session.
- `claude -p` started by hand waits 3 s for stdin; use `< /dev/null`.
- The user runs neoharness as `./neoharness` from the repo, on the real port
  47821. Before you stop a `neoharness` or `ssh` process, check its port and
  socket (`lsof -p`, `ps -o ppid`); only stop scratch ones.
- The user's Claude hooks block `sleep` (also `sleep` in Python); to hold a
  turn open, ask for a file write and leave the permission card open (this no
  longer asks in auto mode; use a tool call auto mode asks about).
- Shell commands here: `grep` may be aliased to `rg`; use `/usr/bin/grep` or
  `/usr/bin/find` when flags matter.

## 9. Known limits and open items

1. Cancelling a remote turn kills the local ssh process. The remote claude
   should stop when its output pipe closes (SIGPIPE). Not verified.
2. Shift+Enter is the same as Enter in most terminals; use `<C-j>` for a new
   line. `<Esc>` followed very fast by another key reads as Alt+key (tcell).
3. The quit guard counts only Running threads. A thread that waits on a
   permission card also has a turn in flight, and `:qa` does not protect it.
4. Data in `~/.harness/` (from before the first rename) is not moved. Old
   `HARNESS_*` variables are not read.
5. **The `/` list is empty in a folder where no thread has finished a turn.**
   Offered, not done: start Claude briefly to read the init record.
6. The status line updates at the end of a turn, not during it.
7. **No restore for a deleted workspace** (edit `workspace.json`). Thread
   delete is hard (no undo); workspace delete is soft ("for now").
8. `<leader>pv` rebuilds the layout: opening the workspace again shows the
   default layout; splits are not kept.
9. **Ask the user to confirm the cursor shiver is gone** with the frame buffer
   (section 5).
10. **Auto mode**: the permission card path under auto mode was not seen in
    tests. Agents may run commands without asking that the default mode would
    have asked about.
11. **Artifacts**: an update sends the whole document (no partial edit); no
    delete or rename; a name that is a common word (`plan`) makes that word a
    link in every chat; the popup's margins show the layout behind it.
12. Not built yet: configurable keymaps and theme, a picker for "allow always"
    rules, other agent backends (the agent interface is ready for them), more
    than one folder per workspace (Claude has `--add-dir`).
13. The threads list has no fuzzy search; use `/` in it, or `<leader><leader>`.
14. The threads list does not show how long a thread has run. Offered, not
    done.
15. The workspace `e2e` (pinned to `/tmp/nh-e2e-proj`) is left from an old
    test. Delete it with `<C-d>` in `<leader>w` when the user agrees.

## 10. Repository

- GitHub: https://github.com/MahammadAgayev/neoharness (renamed from
  `harness`; GitHub redirects the old URL). `origin` points to the new URL.
- Local folder: `~/Documents/src/neoharness`.
- Branch `neoharness-hub-tui` (from `main` at `68d5cc9`). `main` itself is not
  changed; merge when ready. Nothing is pushed.
- Commit `fd86dd7`: projects, threads, and the Vim-like TUI in one process
  (first session).
- Commit `4b5e448`: second session and the first part of the third (11.2 and
  11.3 items 1–6).
- **Not committed**: 11.3 items 7–14 (24 files, about +1,200 / −60 lines).
- The approved design plan is at
  `~/.claude/plans/this-was-already-kinda-lively-hanrahan.md`. Changes made
  after it: no pets or emojis, the coordinator names threads, a more minimal
  screen, the API split into `hubapi` + `hubserver` with a `model` package,
  the hub in the TUI process (no tmux, no daemon), the old loop removed, and
  everything in section 11.

## 11. Change log

### 11.1 First session (commit `fd86dd7`)

Projects (now workspaces), coordinator and threads, the hub in the TUI
process, MCP tools, permissions over MCP, remote threads over ssh, the
Vim-like TUI.

### 11.2 Second session (2026-11-09 afternoon; in `4b5e448`)

1. `/` and `@` completion in Insert; `read_thread` with a required `tail`.
2. Thread delete (`<C-d>` in a picker, then `y`).
3. Keys from the user's Neovim: `<leader>y`/`Y`; permissions on
   `<leader>a`/`A`/`x`; recentering `<C-d>`/`<C-u>`/`n`/`N`; scrolloff 8;
   vertical splits to the right.
4. Rename projects → workspaces everywhere, with the data move.
5. Status line: branch, model, context, cost.
6. Slash commands done by the hub: `/clear`, `/model`, `/effort`, `/compact`;
   result-only output shows.
7. Prompt 3 to 12 lines; workspace folder change; soft workspace delete;
   toasts; `<leader>pv`, `<leader>pc`.

### 11.3 Third session (2026-11-09 evening)

Committed in `4b5e448`:

1. Threads pane (replaced the `<leader>t` picker), grouped by state, resolved
   folded, coordinator not listed.
2. Send now (`<C-x><C-s>`).
3. Lowercase labels everywhere.
4. Remote fixes: tunnel without ControlMaster; `~/.local/bin` on the remote
   PATH; the tunnel and remote commands killed as a process group, so the hub
   owner can quit and another instance takes over.
5. `!` shell mode.
6. A stopped turn's spinner stops when the thread goes Idle or Resolved.

Not committed:

7. Artifacts: `write_artifact` / `read_artifact`, storage, prompts, the
   document renderer.
8. Artifact links in chats, `<leader>pa`, `:artifacts`.
9. A dropped tunnel opens again by itself.
10. Frame buffer and drawn cursor (`canvas.go`): the cursor shiver fix.
11. Only the newest tool block spins; a turn's end stops every spinner.
12. `--permission-mode auto` on every turn.
13. Default layout: coordinator on the left, threads pane on the right
    (it was on the left for a short time; the user did not want that).
14. Artifacts open in a popup: document left, writer's chat right.
15. Outline rail on the left of a document: a dash per heading, the
    cursor's section heavier.
16. The person edits artifacts inside neoharness; the writer gets a packed
    diff with its next message (user's choices: edit in neoharness, not
    $EDITOR; with the next message, not at once; the writer only).

Things found on the user's machine:
- An old-style hub from before the hub moved into the TUI was still running
  in the second session; it was stopped. This cannot happen with the current
  code.
- The user's tmux has no `sync` feature; `ssh` on the user's PATH is a bash
  wrapper (`~/.aw/pex_resources/scripts/binaries/ssh`); the user's ssh config
  shares connections (ControlMaster); on the remote host, `claude` is in
  `~/.local/bin` and the interactive `.zshrc` starts tmux.
