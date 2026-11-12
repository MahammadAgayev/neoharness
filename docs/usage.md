# Usage

## Workspaces

```
:new <name> [folder]    create a workspace and open it; no folder opens a folder picker
:folder [folder]        change the folder; the coordinator moves and keeps its conversation
:workspace [name]       switch; no name opens the picker (also <leader>w)
:workspaces             the workspaces screen (also <leader>pv)
:restore [name]         bring back a deleted workspace; no name lists them
```

- A workspace opens with the coordinator on the left and the threads pane on
  the right.
- The folder picker lists "not pinned", the folder you started in, and every
  folder up to 6 levels below it. Dot folders, `Library` and `node_modules`
  are skipped.
- `<Tab>` completes folder paths in `:new`, `:folder` and `:open`.
- To delete a workspace, press `x` on it in the workspaces screen, or
  `<C-d>` on it in the workspaces picker, then `y`. This stops its turns and hides it. The data stays on disk, so
  `:restore` brings it back.

## Threads

The coordinator usually opens threads. It has no tools to read files, run
commands or change code: it manages, and threads do the work. You can open a
thread yourself:

```
:open <name> [folder|host:dir]    open a thread, locally or on an ssh host
:resolve  :reopen                 close a thread, or bring it back with its session
```

| State | Meaning |
|---|---|
| idle | nothing to do |
| running | a turn runs, or messages wait in the queue |
| waiting | a permission request, a question to you, or the loop guard paused it |
| resolved | closed; `:reopen` brings it back |

- The threads pane groups threads by state. Resolved threads fold into
  `▸ N resolved`.
- To delete a thread, press `<C-d>` on it in a picker, then `y`. This cannot
  be undone. The coordinator cannot be deleted.
- A thread runs at most 20 turns in a row that you did not start. Then it
  waits for you.

### Writing to a session

- `i a o A I` writes in the prompt at the end of a chat. `<CR>` sends,
  `<C-j>` makes a new line, `<Esc>` goes to Normal mode.
- The prompt is a part of the chat buffer. Vim motions, changes, undo and
  Visual mode work in it.
- Messages sent while a thread is busy wait in a queue and show dim at the
  end of the chat. `<CR>` on one sends it now: the running turn stops, and
  the queue goes in the next turn at once. `x` or `dd` on one takes it back.
- `x` or `dd` on the last message while its turn runs stops the turn.
- `/` at the start completes slash commands. `@` completes threads and files.
- `!<command>` at the start runs a shell command in the thread's folder. The
  output goes to Claude with your next message.
- `/clear`, `/compact`, `/model <name>` and `/effort <level>` work per
  thread.

### Permissions

Turns run in Claude's auto permission mode. When Claude asks for approval, a
card shows in the chat:

```
<leader>a   :allow           allow once
<leader>A   :always          allow always (for Bash: the first word of the command)
<leader>x   :deny [reason]   deny
```

## Notifications

Toasts show in the top right corner and name the workspace and the thread.
`<leader>nc` dismisses them and `<leader>nh` shows their history.

- By default the coordinator decides what reaches you. A thread that ends a
  turn wakes it; it sends corrections on its own, and notifies you when the
  work you asked for is done or needs you. Tell it how often you want to
  hear from it. Its notices show in its chat as `↗ notified you: …`, and
  one that needs you stays until you open the coordinator.
- Questions, permission requests and errors always come to you directly.
- `notify = "threads"` in the hub setup goes back to a toast for every
  thread's finished turn.
- When a session's context passes 80%, its status line shows
  `ctx 82% · /compact` in red: every turn sends the whole context again, so
  `/compact` then saves tokens. `neoharness.o.compactwarn` changes the
  percentage.

## Artifacts

Artifacts are markdown documents that agents write for you: plans, designs,
reports. They belong to the workspace, and every session can read them.

- `<leader>pa` picks one. `<CR>` on a `◇` line or on an artifact name in a
  chat opens it too. It opens in the current window, and `<C-o>` goes back.
- The open document reloads when an agent writes a new version.
- `:e`, or `i a I A o O`, shows the markdown source for editing. `:w` saves.
  If the agent wrote since, the hub merges by lines. Changes to the same
  lines are refused: `:w!` saves over them, `:e!` takes the agent's version.
- The agent sees your edit as a diff in its next turn.
- `gx` opens the link under the cursor.

## Keys

`<leader>` is `<Space>`. Vim motions, operators, search, marks, macros and
`<C-w>` window keys work as in Vim.

| Keys | What |
|---|---|
| `<leader><leader>` `<leader>pb` | pick a buffer (threads and artifacts included) |
| `<leader>pt` | pick a thread |
| `<leader>pa` | pick an artifact |
| `<leader>ps` | search every transcript |
| `<leader>w` | pick a workspace |
| `<leader>pv` | workspaces screen |
| `<leader>t` | threads pane |
| `<leader>c` | coordinator |
| `]m` `[m` / `]b` `[b` / `<C-^>` | next/previous message (heading in an artifact) / thread / alternate buffer |
| `gO` | outline: messages, or headings in an artifact |
| `za` `<leader>e` | open or close a turn's tool calls |
| `v V y yy` `<leader>y` | select and yank; every yank also goes to the system clipboard |
| `<C-h/j/k/l>` `<Tab>` | move between windows |
| `<leader>nc` / `<leader>nh` | dismiss notifications / their history |
| in pickers: `<CR>` `<C-v>` `<C-x>` `<C-d>` | open, split right, split above, delete (after `y`) |

The mouse works too: a click focuses a window and puts the cursor there, a
double click is `<CR>`, and the wheel moves the window under the pointer.
A click on the prompt starts writing. Hold Shift (Option in some terminals)
to select text with the terminal.

## Commands

`:b <name>`, `:ls`, `:split`, `:vsplit`, `:only`, `:threads`, `:artifacts`,
`:messages`, `:help`, `:q`, `:qa`, `:q!`, `:qa!`.

`:q` on the last window and `:qa` refuse while threads run. `:qa!` stops
them and quits. When you start again, each thread continues its Claude
session.
