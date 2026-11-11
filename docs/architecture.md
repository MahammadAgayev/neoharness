# Architecture

```
 neoharness (TUI) ──unix socket, HTTP + SSE──▶ hub (same process)
                                               ├─ state of all workspaces and threads (only writer)
                                               ├─ one turn at a time per thread → claude -p (local or ssh)
                                               ├─ MCP server http://127.0.0.1:47821/mcp (token = which thread)
                                               └─ one ssh reverse tunnel per remote host
```

## The hub

- The hub is the only part that runs Claude and writes state. It starts with
  the TUI and stops with it. The TUI keeps no state of its own.
- A second TUI uses the first one's hub through the socket. When the first
  closes, the next one starts its own hub.
- Closing stops running turns. SIGHUP and SIGTERM stop them first too, so no
  `claude` process is left.
- Reopening loses nothing. Each thread keeps its Claude session UUID on
  disk, and the next turn continues it with `--resume`.

## Turns

Each turn is one `claude -p` call with `--output-format stream-json`,
`--session-id` on the first turn and `--resume` after, `--mcp-config`,
`--permission-prompt-tool mcp__neoharness__permission_prompt`,
`--permission-mode auto` and `--append-system-prompt`. There is no
long-lived Claude process: Claude keeps the conversation, and the hub keeps
the UUID.

- Messages that arrive while a thread is busy wait in a queue. All of them
  go into the next turn as one prompt.
- A thread's report does not wake the coordinator. The coordinator's next
  turn starts with a note on the threads that ended since its last turn.
- Send now stops the running turn, so the queue goes in the next turn at
  once.
- From each turn the adapter reads the slash commands, the model, the
  context size and the cost. The status line shows them.
- When a conversation outgrows the model's context, every turn of it fails.
  The hub then starts a new conversation from the thread's own transcript
  (the task and its end), and sends the failed message again.
- Under `-p`, `/clear`, `/compact`, `/model`, `/effort` and `!` do not work
  as in the interactive CLI, so the hub does them itself.

## MCP tools

The server is named `neoharness`. The bearer token tells which thread calls,
so no thread can act for another thread or another workspace.

| Who | Tools |
|---|---|
| all sessions | `whoami`, `workspace_status`, `send_message`, `report_progress`, `ask_user`, `read_thread`, `write_artifact`, `read_artifact` |
| coordinator only | `open_thread`, `resolve_thread` |
| internal | `permission_prompt` |

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

A thread's state is not stored. The hub calculates it from the stored
fields each time, so it cannot drift.
