# Architecture

```
 neoharness (TUI) ──unix socket, HTTP + SSE──▶ hub (background process)
                                               ├─ state of all workspaces and threads (only writer)
                                               ├─ one turn at a time per thread → a kept claude -p process (local or ssh)
                                               ├─ MCP server http://127.0.0.1:47821/mcp (token = which thread)
                                               └─ one ssh reverse tunnel per remote host
```

## The hub

- The hub is the only part that runs Claude and writes state. The TUI keeps
  no state of its own.
- The hub is a process of its own, as the tmux server is. When no hub
  answers on the socket, neoharness starts `neoharness hub serve` in a new
  session and waits for it. Every TUI shares that hub, and turns run on after
  they close.
- `neoharness hub stop` asks the hub over the socket to stop. It stops the
  running turns, sends a last `stopped` update and exits; SIGHUP and SIGTERM
  do the same. A TUI that gets `stopped` waits for a hub and does not start
  one. A TUI that loses the hub without it starts a new one.
- Reopening loses nothing. Each thread keeps its Claude session UUID on
  disk, and the next turn continues it with `--resume`. A message from the
  person or the coordinator reopens a resolved thread; another thread's is
  refused. When the coordinator reopens one past `compactwarn`,
  `reopen_compact` compacts first, holds the message as a question, or
  does nothing. Past `auto_compact`, the next turn of any session is a
  `/compact` first; the hub decides it in `kick`, so it runs only between
  turns.

## Turns

A conversation runs in one `claude -p` process with `--input-format
stream-json` and `--output-format stream-json`, `--session-id` on the first
start and `--resume` after, `--mcp-config`, `--permission-prompt-tool
mcp__neoharness__permission_prompt`, `--permission-mode auto` and
`--append-system-prompt`. Each turn is one line on its stdin, and ends with
the `result` record. Claude keeps the conversation, and the hub keeps the
UUID.

- The process stays for the next turn, so a turn does not pay for starting
  claude, its hooks and MCP servers. It ends after 5 minutes without a turn,
  when the model, effort, folder or system prompt changes, and when
  neoharness stops. The next turn starts a new one with `--resume`.
- The hub gives every turn the system prompt as it is now. Claude keeps a
  conversation's first prompt on resume, so the adapter adds
  `--system-prompt-snapshot off`: a workspace that lives for months gets the
  current prompt after a restart, with its conversation. A claude too old to
  know the flag runs without it.
- Stopping a turn asks claude to interrupt it, and the process stays. A
  turn that does not end in 3 seconds is killed.
- The coordinator runs with `--tools ""` and `--strict-mcp-config`: only the
  neoharness tools. It manages and cannot do a thread's work, and its context
  stays small.

- Messages that arrive while a thread is busy wait in a queue. All of them
  go into the next turn as one prompt.
- `ask_user` works as a permission request: the hub holds the call open,
  the thread waits, and the person's next message is the answer, which
  goes back as the tool's result in the same turn. A deny skips it. The
  time it waits, as for a permission, does not count toward the turn
  limit. The adapter turns off Claude's own question tool, which no one
  can answer under `-p`.
- When the turn ends before the answer, the question stays with the
  thread, and the others' messages wait too, so the thread does not move
  on from it. The person's answer goes first in the next turn, with them;
  send now with nothing written sends them without one.
- The person's message can hold images: png, jpeg, gif or webp, at most 5
  per message and 3.75 MB each (5 MB as base64). The hub reads the type from
  the bytes and keeps each image in the thread's `images/` folder. The text
  names them `[image 1]`, `[image 2]`; when one turn reads several messages,
  the hub numbers them again for the whole turn. The Claude adapter sends
  them as image content blocks before the text, on the session's stdin, so
  a remote thread gets them through ssh.
- Every coordinator turn starts with the board: each open thread, its state,
  the question or permission it waits for, the error of a failed last turn,
  its recap, and its tasks (`3/5 done, now: …`). A turn end is not progress
  by itself: a thread can end a turn only to answer a message. So the hub
  keeps the thread's news instead, in `thread.json`: a new recap, todo
  items done, artifacts written, a failed turn, and, for a turn that ran
  tools and wrote no recap, the start of its reply. The board shows the news
  once, as a `New:` line under the thread, then clears it; a restart keeps
  it. With `notify = "coordinator"`, news also wakes the coordinator, 15
  seconds later so that news close together makes one turn, and again when
  the hub starts if news waits. A turn with no news wakes nothing. The
  coordinator decides with `notify` what reaches the person; these turns do
  not count toward the pause. With `notify = "threads"` (the default), only
  the person's messages wake it, and its next board has the news.
- The coordinator's chat is the person's. Threads cannot message the
  coordinator; it learns of them from the board. A turn that only the hub's
  report about threads starts is quiet: its message, reply, tool calls and
  turn end stay out of the chat. A notice, a question or an error from it
  still shows.
- Send now stops the running turn, so the queue goes in the next turn at
  once. Stop leaves the queue waiting: the thread is paused until the
  person writes or sends now.
- From each turn the adapter reads the slash commands, the model, the
  context size and the cost. The status line shows the model and the
  context; the cost shows only in the cost view.
- Under `-p`, `/clear`, `/compact`, `/model`, `/effort` and `!` do not work
  as in the interactive CLI, so the hub does them itself.

## MCP tools

The server is named `neoharness`. The bearer token tells which thread calls,
so no thread can act for another thread or another workspace.

| Who | Tools |
|---|---|
| all sessions | `whoami`, `workspace_status`, `send_message` (to threads only), `ask_user`, `wake_me`, `write_artifact`, `read_artifact`, `recap` |
| threads only | `read_thread` |
| coordinator only | `open_thread`, `resolve_thread`, `notify` |
| internal | `permission_prompt` |

The coordinator has no `read_thread`: it decides from the board, and asks a
thread with `send_message` when the board is not enough. So it cannot read a
transcript and retell it; the person opens the thread for its details.

A turn ends when Claude's reply ends, and only a message starts the next
one. `wake_me(minutes, note)` asks the hub for that message later, for work
that outlives the turn, such as a long benchmark. A thread has at most one
wake-up; it is saved with the thread, so it survives a restart, and one
whose time passed while the hub was down comes at once. Go timers stop while
the computer sleeps, so the hub also checks the wall clock each minute: a
wake-up comes at most a minute after the computer wakes. The threads list
shows `wakes at 16:52` on an idle thread that has one, and never a time that
has passed.

`recap(text)` keeps where a session's work stands, in one or two short
sentences, as Claude Code's recap. The session calls it last in a turn in
which its work moved, so no extra model turn makes it. The hub saves it with
the thread and sends it in the thread's view; the TUI and the app show it
in a card at the top of the chat when the person comes back (see
usage.md). Which recap the person closed stays in the client.

## Permissions

When Claude asks for approval, it calls `permission_prompt`. The hub holds
the call open, sets the thread to waiting, and the TUI shows a card. The
turn's 4-hour limit stops while a permission or a question waits.

- Your own Claude settings and allow-lists still apply.
- The hub's own tools are always allowed.
- "Allow always" stores a rule per thread for the request's scope, which
  the card shows. The adapter chooses the scope: for Bash the command and,
  for tools such as git, go and npm, its subcommand (`git push`); for a
  file edit the thread's folder; for a fetch the domain; else the whole
  tool.
- Some requests have no scope, so they always ask and the card offers no
  "always": a Bash command with `;`, `&`, `|`, `` ` ``, `<`, `>`, a newline
  or `$(`; a destructive flag such as `--force`, `--hard` or `-rf`; a
  command that runs other commands or deletes, such as `sh`, `env`,
  `python`, `rm` or `curl`; a file edit outside the thread's folder.
- A pending request is denied when its turn ends.

## Remote threads

Per host, the hub keeps `ssh -N -R 47821:127.0.0.1:47821 <host>` open and
opens it again when it drops. A turn runs `claude` over ssh in the thread's
folder. File lists, the git branch and `!` commands use the same ssh
command.

## Data on disk

```
~/.neoharness/hub.sock                                             local API socket (0600)
~/.neoharness/hub.log                                              hub log
~/.neoharness/workspaces/<workspace>/workspace.json                name, folder, deleted
~/.neoharness/workspaces/<workspace>/threads/<name>/thread.json    state, queue, session (0600: it holds the MCP token)
~/.neoharness/workspaces/<workspace>/threads/<name>/events.jsonl   transcript, append-only
~/.neoharness/workspaces/<workspace>/threads/<name>/images/        images the person sent, named by content
~/.neoharness/workspaces/<workspace>/artifacts/<name>.md / .json   artifact text / metadata
~/.neoharness/workspaces/<workspace>/costs.jsonl                   one line per paid turn: when, thread, model, cost
```

`GET /cost?workspace=<name>` adds up what a workspace cost; with no name,
every workspace. The totals per thread count every turn. The days, the
models and the turns that cost the most come from the cost log, which
began later, so they start at its first line (`since`).

An artifact belongs to the session that wrote it last. The person's edit
reaches that session as a diff before its next message. A save from version
0 to a new name starts a document the person writes: the coordinator holds
it and reads it in full on its next turn.

A thread's state is not stored. The hub calculates it from the stored
fields each time, so it cannot drift.
