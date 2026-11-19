# Usage

## Workspaces

```
:new <name> [folder]    create a workspace and open it; no folder opens a folder picker
:folder [folder]        change the folder; the coordinator moves and keeps its conversation
:setlocal coordinator=<agent>   this workspace's coordinator runs that agent; threads=<agent> for its threads
:setlocal reopen_compact=ask    a thread the coordinator reopens past compactwarn: compact first, ask you, or off
:setlocal auto_compact=90       past this context %, a session compacts before its next turn; 0 never
:setlocal textwidth=90          the widest column of chats and artifacts; 0, the default, is the full width
:setlocal threads&      back to the config; threads? shows it; no argument shows all
:workspace [name]       switch; no name opens the picker (also <leader>w)
:workspaces             the workspaces screen (also <leader>pv)
:restore [name]         bring back a deleted workspace; no name lists them
```

- The workspaces screen shows each workspace's name, folder and open
  threads, most urgent first: `◆ 1 waiting · ✗ 1 failed · ● 2 running ·
  ◌ 1 background · ○ 3 idle`. Waiting is gold and failed is red; the rest is dim. The
  coordinator counts only when it waits, runs or has a notice for you, and
  resolved threads do not count. With nothing open, the row says when the
  workspace was last used (`5m ago`, `2h ago`, `3d ago`). Rows keep their
  order.
- A workspace opens with the coordinator on the left and the threads pane on
  the right. `<CR>`, `:b` or `:open` from the list opens the thread in the
  list's place, so the coordinator and the thread share the screen half and
  half. `<C-o>` there brings the list back.
  `<leader>t` goes to the list, or brings it back in a column of its own.
- Each pane has a status line at its foot, a bar as in Vim: its name and
  state on the left; for a chat, its queue, branch, model and context on the
  right. The pane with focus has a brighter bar (`StatusLine`), its name in
  bold, and the branch, model and context in their colors; the others have
  a quieter bar (`StatusLineNC`) and dim text. The last
  row of the screen is the command line, with the workspace, `◆ N waiting`
  (the count of the waits list) and how many threads run on its right.
- The screen holds still. A running thread's `●` turns to `◉` and back
  every 0.8 s, the live tool row spins 4 times a second, and times change
  once a minute (`now`, `4m`, `1h05m`). A reply streams in as the agent
  writes it, drawn at most 8 times a second. Nothing else moves on its own:
  the `◌` of background work holds still. The
  cursor is a block in Normal and Visual mode and a line in Insert, as in
  Vim, and it does not blink.
- The folder picker lists "not pinned", the folder you started in, and every
  folder up to 6 levels below it. Dot folders, `Library` and `node_modules`
  are skipped.
- `<Tab>` completes folder paths in `:new`, `:folder` and `:open`.
- To delete a workspace, press `x` on it in the workspaces screen, or
  `<C-d>` on it in the workspaces picker, then `y`. This stops its turns and hides it. The data stays on disk, so
  `:restore` brings it back.

## Threads

The coordinator usually opens threads. It has no tools to read files, run
commands or change code: it manages, and threads do the work. Threads cannot
message it, so its chat holds only your talk with it; the turns in which the
hub wakes it about threads stay out of the chat. You can open a thread
yourself:

```
:open <name> [folder|host:dir]    open a thread, locally or on an ssh host
:resolve  :reopen                 close a thread, or bring it back with its session
:stop [name]                      stop the running turn of a thread
```

| State | Meaning |
|---|---|
| idle | nothing to do |
| running | a turn runs, or messages wait in the queue |
| background | its turn ended, but work it started runs on, such as a shell or a subagent |
| waiting | a permission request, a question to you, or the loop guard paused it |
| failed | its turn ended in an error, and nothing came after it; the next message clears it |
| resolved | closed; `:reopen`, or a message from you or the coordinator, brings it back |

- The threads pane groups threads by state, each group with its count
  (`waiting · 1`): what waits for you and what failed on top, then what
  runs, then background work, then what is idle. Resolved threads fold into `▸ N resolved`. A
  row is the state's glyph, the name, and on the right how long the thread
  has been in its state (`now`, `4m`, `2h`, `6d`); a waiting thread has a second row
  with what it waits for, such as `permission · Bash  psql -c …`. Any other
  thread with a todo list has a second row with its progress and the item
  in hand, `3/7 · wire the parser`, or `7/7 done`. `K` on a
  row shows the thread's last reply on the command line.
- A thread in the background state shows `◌` and how much runs, `2 in
  background`, on its row and its status line. Its turn end does not
  notify you. When the work ends, the agent starts a turn by itself to
  read the result, and the end of that turn notifies you as any turn end
  does. A message to the thread runs at once; it does not wait for the
  work. A hub restart ends the work, so an update waits for it.
- A row that changes group moves only when your cursor is not in the
  threads pane or on the command line, so nothing moves under you; then it
  shows a light background for 1.5 s.
- To delete a thread, press `<C-d>` on it in a picker, then `y`. This cannot
  be undone. The coordinator cannot be deleted.
- A thread runs at most 20 turns in a row that you did not start. Then it
  waits for you.

### Writing to a session

- `i a o A I` writes in the prompt at the end of a chat. `<CR>` sends,
  `<C-j>` makes a new line, `<Esc>` goes to Normal mode.
- The prompt is a part of the chat buffer. Vim motions, changes, undo and
  Visual mode work in it.
- A message from the coordinator longer than 3 rows, such as a thread's
  brief, folds to one row, `▎ from coordinator · brief ▸`. `za` on it
  opens it.
- Messages sent while a thread is busy wait in a queue and show dim at the
  end of the chat. `<CR>` on one sends it now: the running turn stops, and
  the queue goes in the next turn at once.
- A question waits in a card above the prompt, and the thread waits for
  you. It comes at the end of its turn, after the reply that explains it,
  and holds one short decision, with two to four numbered options when the
  answer is one of a few. Asking does not hold the thread's turn: it ends,
  and the question stays, over a restart too. You answer by writing: your
  next message is the answer, in a new turn, and it may carry a picture.
  An option's number alone, such as `2`, answers with that option: the
  thread reads `2 · assign`. `<leader>x` skips it: the thread hears that
  you skipped it and goes on without the answer.
  Once answered, the question shows as a block in the chat. The
  coordinator's and the other threads' messages wait behind it, dim; your
  answer goes first, with them, and `<CR>` on one sends them without an
  answer, and the question closes.
- When you come back after 2 minutes or more away, a card at the top
  right says how all the work stands, in every workspace, in one line by
  the attention agent (or as counts without it, `◆ 2 waiting · ✓ 3 to
  read`), and then the next three things to do, the top of the waits list,
  each with its brief: `◆ api-tests  read-only · suggest: allow`. `]q`
  goes to the first, as a come-back starts the walk at the top again. The
  card closes at your next key; `:messages` keeps the status.
- When you come back after 2 minutes or more away, a chat shows its recap
  as dim text just above the todo line and the prompt, `※ recap: …`: where
  the session's work stands, in its own words, as in Claude Code. Away is
  the terminal without focus, or no key for that long when the terminal
  sends no focus events. `<Esc>` or `q` closes it, and so does a message to
  the session; it comes back
  only with a newer recap. A running turn hides it. `:recap` shows the
  recap of the chat in the window again, even after you closed it.
- While the thread has a todo list, one line above the prompt
  shows it: `▸ 3 of 7 · ` and the item in hand. `za` or `<CR>` on the line,
  or `<leader>T`, unfolds it into every item: done ones checked and struck
  through, the one in hand `●`, the next ones `○`. A done list stays,
  `▸ 7 of 7`, until a later turn starts a new one. A done task in a reply or an artifact (`- [x]`) is struck
  through too.
- `u` with an empty prompt is Esc in Claude Code: your last message comes
  back to the prompt to edit, while the agent has not answered it yet. A
  queued one leaves the queue; a running turn stops, and the message
  leaves the chat. If the agent had begun on it, its next turn hears that
  you took it back.
- `<C-c>` in Normal mode stops the running turn of the chat in the window,
  or of the thread on the threads list's row, as Vim's interrupt key does.
  `x` on the chat's transcript and `:stop [name]` do the same. The running
  glyph stops and the chat says `Interrupted.`; the agent keeps the work it
  did, and your next message starts a new turn. A permission request is
  denied and a question closes. Messages queued behind the turn wait,
  paused, until you write or `<CR>` on one. `<C-c>` in Insert does nothing,
  so a `<C-c>`, or two, typed to leave Insert never stops a turn; `<Esc>`
  never stops one.
- `:image` puts the picture on the clipboard in the prompt, after the
  cursor, as `[image 1]`; `:image <file>` takes a file. A terminal cannot
  paste a picture, so this does it. The picture goes to the agent with the
  message, as in Claude Code; delete the token and it does not go. Up to 5
  per message, png, jpeg, gif or webp; a big one is made smaller first, to
  1568 pixels on its long side. On macOS the clipboard is read with
  `osascript`, on Linux with `wl-paste` or `xclip`. `u` that takes a message
  back brings its pictures back too. In the app, paste it (`⌘V`).
- `/` at the start completes slash commands. `@` completes threads and files.
- `!<command>` at the start runs a shell command in the thread's folder. The
  output goes to Claude with your next message.
- `/clear`, `/compact`, `/model <name>` and `/effort <level>` work per
  thread.

### Permissions

Turns run in Claude's auto permission mode. When Claude asks for approval, a
card shows above the prompt: a bar in gold, the tool, and the full command,
up to 8 rows. It has no key hints; these are the keys:

```
<leader>a   :allow           allow once
<leader>A   :always          allow always, for what the card shows after "always:"
<leader>x   :deny [reason]   deny
```

"Always" covers only what the card names, such as `git push`, a folder or
a domain. A request that is risky every time, such as a force push, a
chained command or `rm`, offers no "always".

## Notifications

Toasts float at the top right, the newest on top, at most 3: a box with
the glyph and the thread, and its text under them. Each names the
workspace too when it is not the open one; `:messages` keeps them all.
A toast that does not need you waits while you are in Insert or on the
command line, and shows when you leave it. What needs you shows at once,
except a thread of the open workspace while the threads pane is open: its
row already says it.
What needs you, in every workspace, is also in one list, as Vim's
quickfix list, in the order to go through it: permission requests and
questions, which block a turn, then failed turns and notices that need
you, then reviews, each oldest first. A review (`✓`) is a thread whose
news you have not read: a turn that ended or a notice. A place to go
(`→`) is a thread or a coordinator where the work goes on only when you
move, with what to do there, such as "build ended · give it the docs
task": the attention agent adds them, and with nothing else in the list
the coordinator of the workspace that moved last is there, so a next
thing is always ready. The command line counts what blocks on you as
`◆ N waiting`; reviews and places to go are not counted. `]q`
and `[q`, or `:cn` and `:cp`, go to the next or previous one and open its
thread at its card, in any workspace. `<C-o>` goes back to the workspace
the jump left, as it was, until you move in the window it landed in; the
pickers and `:workspace` jump the same way. `:waits`, `:copen` or
`<leader>pq` opens the list as a picker, the focus list: only what you
can go to, each wait with how long it has waited and its brief, wrapped
whole beside its name, or under it when the terminal is narrow. It
follows the hub while it is open: a row moves only when something comes,
goes or changes there, never because you move,
and one that goes stays, muted, until the list closes. `<CR>` goes to a
wait, `<C-d>` dismisses a notice, a failure or a place to go, or reads a
review; typing
filters. `:waits!` shows it oldest first.
The attention agent (see [configuration](configuration.md)) orders the list
by your focus: what is under Now first, what is under Leave alone last.
After a jump the command line says why the stop is there, and a
permission or question card ends with its brief, muted: what is at stake
and what the agent suggests. A review, a failure, a notice or a place to
go has no card,
so its brief shows alone above the prompt, until the thread runs again. An item leaves the list only when
it is done: answered, skipped, stopped, or the thread runs again; a review
when you read the thread. A thread on screen for 2 seconds tells the hub
you saw it; that quiets what it said since, but never what needs you.

`<leader>nc` dismisses them and `<leader>nh` shows their history.
`<leader>pm` lists what happened in every workspace, newest first: turns
that ended, questions, permission requests, failures, notices, threads
opened and resolved. It reads the hub's journal, so it holds what came
while the TUI was closed. `<CR>` opens the row's thread, in its workspace.

- By default every thread that ends a turn shows a toast with the first
  line of its reply. One from a workspace that is not open stays longer.
- Questions, permission requests and errors always come to you directly.
- The coordinator can send you a notice when something needs you. Its
  notices show in its chat as `↗ notified you: …`, and one that needs you
  stays until you open the coordinator.
- With `notify = "coordinator"` in the hub setup, the coordinator decides
  what reaches you instead. A thread that ends a turn wakes it; it sends
  corrections on its own, and notifies you when the work you asked for is
  done or needs you. Tell it how often you want to hear from it.
- When a session's context passes 80%, its status line shows
  `ctx 82% · /compact` in red: every turn sends the whole context again, so
  `/compact` then saves tokens. `neoharness.o.compactwarn` changes the
  percentage. Past 90%, the session compacts by itself before its next
  turn, and its transcript says so; `auto_compact` in the hub setup or
  `:setlocal auto_compact=<n>` changes that, and `0` turns it off.

## Artifacts

Artifacts are markdown documents that agents write for you: plans, designs,
reports. They belong to the workspace, and every session can read them.

- `<leader>pa` picks one. `<CR>` on a `◇` line or on an artifact name in a
  chat opens it too. It opens in the current window, and `<C-o>` goes back.
- The open document reloads when an agent writes a new version.
- `:e`, or `i a I A o O`, shows the markdown source for editing. `:w` saves.
  If the agent wrote since, the hub merges by blocks: changes to different
  paragraphs, lists, tables or code blocks both stay, and so do changes to
  different lines of one block. Changes to the same lines are refused:
  `:w!` saves over them, `:e!` takes the agent's version.
- The agent sees your edit as a diff in its next turn.
- `gx` opens the link under the cursor.
- `:page <name>` makes a page, unless it is there already, and links it at
  the cursor as the agents write a link: `` `name` ``.

### Your focus

The focus is your document of what you work on now, in every workspace. It
belongs to the hub, not to a workspace, and only you write it.

- `<leader>f` or `:focus` opens it, from any workspace or none. Before you
  write one it is a template: `## Now`, `## Keep moving`, `## Leave alone`.
  It edits and saves as any artifact.
- `:focus off` sets it aside and `:focus on` brings it back; the text stays.
  While it is aside, its top line says `set aside · :focus on`.
- The attention agent orders what waits for you by it, and may suggest an
  edit, such as striking through an action the waits show is done. The edit
  is the last stop of `]q` (`◇ focus`), and the focus's top line says
  `an edit waits · :diffsplit`. Either opens the focus beside the edit, in
  diff mode: `do` takes a change, `]c` `[c` move, and `:w` saves your focus
  as it is then. `<C-d>` on its row in `:waits` drops it, and the agent does
  not suggest the same edit again. `focus_edit = "allow"` saves its edits
  at once instead, and `"off"` stops them.

## Keys

`<leader>` is `<Space>`. Vim motions, operators, search, marks, macros and
`<C-w>` window keys work as in Vim.

| Keys | What |
|---|---|
| `<leader><leader>` `<leader>pb` | pick a buffer (threads and artifacts included) |
| `<leader>pt` | pick a thread of any workspace (not resolved ones) |
| `<leader>pl` | pick a thread of this workspace |
| `<leader>pa` | pick an artifact |
| `<leader>f` | your focus |
| `<leader>ps` | search every transcript |
| `<leader>pm` | pick a notification and open its thread |
| `<leader>pq` | the focus list: what to do next |
| `<leader>w` | pick a workspace |
| `<leader>pv` | workspaces screen |
| `<leader>t` | threads pane |
| `<leader>c` | coordinator |
| `]m` `[m` / `]b` `[b` / `<C-^>` | next/previous message (heading in an artifact) / thread / alternate buffer |
| `gO` | outline: messages, or headings in an artifact |
| `za` `<leader>e` | open or close a turn's tool calls, or a brief from the coordinator |
| `v V y yy` `<leader>y` | select and yank; every yank also goes to the system clipboard |
| `<C-v>` | select a block of columns; `c` types the same text on each line. In the threads list it opens a split |
| `<C-h/j/k/l>` `<Tab>` | move between windows |
| `<leader>nc` / `<leader>nh` | dismiss notifications / their history |
| in pickers: `<CR>` `<C-v>` `<C-x>` `<C-d>` | open, split right, split above, delete (after `y`) |

The mouse works too: a click focuses a window and puts the cursor there, a
double click is `<CR>`, and the wheel moves the window under the pointer.
A click on the prompt starts writing. Hold Shift (Option in some terminals)
to select text with the terminal.

## Commands

`:b <name>`, `:ls`, `:split`, `:vsplit`, `:only`, `:threads`, `:artifacts`,
`:focus`, `:focus on`, `:focus off`, `:messages`, `:help`, `:q`, `:qa`, `:q!`,
`:qa!`.

`:todo` shows the todo list of the thread in the window: what it plans to do,
what it does now and what is done, struck through. It follows the work as
the thread changes the list, and `<C-o>` goes back. `:todo <name>` shows another thread's. The
list belongs to the conversation, so `/clear` and `/compact` empty it. The
thread prompt asks threads to keep one for work of more than a few steps;
the coordinator keeps none. `todos = false` in `setup()` turns todo lists
off (see [configuration](configuration.md)).

`:cost` shows what the workspace cost: in all, today, the last 14 days as
bars, by thread and by model, and the turns that cost the most. `<CR>` on a
thread or a turn opens it. `:cost all` adds up every workspace. The days,
the models and the turns count from when the hub began to log each turn;
the totals count every turn. Cost shows nowhere else, not on the status
line.

`:q` on the last window and `:qa` quit. The hub and its turns run on, so a
thread that works keeps working, and the next `neoharness` shows where it
is. `:qa!` also drops unsaved artifact changes.

## The hub

The hub runs the sessions in a background process, as the tmux server does.
The first `neoharness` starts it, and every other one shares it.

```
neoharness hub start     start the hub in the background
neoharness hub stop      stop the running turns and the hub
neoharness hub restart   stop it and start a new one, e.g. after an upgrade or an init.lua change
neoharness hub status    pid, uptime, running turns, version
neoharness hub serve     run the hub in this terminal
neoharness version       the commit this neoharness was built from
```

- After `hub stop`, an open TUI says the hub stopped and waits. It does not
  start a new one; `hub start` brings it back. If the hub goes away by
  itself, the TUI starts a new one.
- After an update the hub still runs the old build. The TUI and the
  desktop app see it when they connect and restart the hub on the new
  build: at once when no turn runs, else when the turns end, so an update
  cuts no turn. `:hub restart` does it by hand, after a yes when turns run.
  `hub status` also says when the hub is from another build.
- Each thread keeps its Claude session, so a new hub continues every
  conversation.
