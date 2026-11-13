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
  disk, and the next turn continues it with `--resume`.

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
  when the model, effort or folder changes, and when neoharness stops. The
  next turn starts a new one with `--resume`.
- Stopping a turn asks claude to interrupt it, and the process stays. A
  turn that does not end in 3 seconds is killed.
- The coordinator runs with `--tools ""` and `--strict-mcp-config`: only the
  neoharness tools. It manages and cannot do a thread's work, and its context
  stays small.

- Messages that arrive while a thread is busy wait in a queue. All of them
  go into the next turn as one prompt.
- Every coordinator turn starts with the board: each open thread, its state
  and last report, and the start of its reply if it ended a turn since the
  coordinator's last one. With `notify = "coordinator"`, a
  thread's turn also wakes the coordinator, 15 seconds later so that threads
  that end close together make one turn. It decides with `notify` what
  reaches the person; these turns do not count toward the pause. With
  `notify = "threads"` (the default), only the person's messages wake it.
- Send now stops the running turn, so the queue goes in the next turn at
  once. Stop leaves the queue waiting: the thread is paused until the
  person writes or sends now.
- From each turn the adapter reads the slash commands, the model, the
  context size and the cost. The status line shows them.
- Under `-p`, `/clear`, `/compact`, `/model`, `/effort` and `!` do not work
  as in the interactive CLI, so the hub does them itself.

## MCP tools

The server is named `neoharness`. The bearer token tells which thread calls,
so no thread can act for another thread or another workspace.

| Who | Tools |
|---|---|
| all sessions | `whoami`, `workspace_status`, `send_message`, `report_progress`, `ask_user`, `wake_me`, `read_thread`, `write_artifact`, `read_artifact` |
| coordinator only | `open_thread`, `resolve_thread`, `notify` |
| internal | `permission_prompt` |

A turn ends when Claude's reply ends, and only a message starts the next
one. `wake_me(minutes, note)` asks the hub for that message later, for work
that outlives the turn, such as a long benchmark. A thread has at most one
wake-up; it is saved with the thread, so it survives a restart. The threads
list shows `wakes at 16:52` on an idle thread that has one.

## Permissions

When Claude asks for approval, it calls `permission_prompt`. The hub holds
the call open, sets the thread to waiting, and the TUI shows a card.

- Your own Claude settings and allow-lists still apply.
- The hub's own tools are always allowed.
- "Allow always" stores a rule per thread. A Bash command with `;`, `&`,
  `|`, `` ` ``, `<`, `>`, a newline or `$(` always asks.
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
~/.neoharness/workspaces/<workspace>/artifacts/<name>.md / .json   artifact text / metadata
```

An artifact belongs to the session that wrote it last. The person's edit
reaches that session as a diff before its next message. A save from version
0 to a new name starts a document the person writes: the coordinator holds
it and reads it in full on its next turn.

A thread's state is not stored. The hub calculates it from the stored
fields each time, so it cannot drift.
