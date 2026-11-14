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
- The person's message can hold images: png, jpeg, gif or webp, at most 5
  per message and 3.75 MB each (5 MB as base64). The hub reads the type from
  the bytes and keeps each image in the thread's `images/` folder. The text
  names them `[image 1]`, `[image 2]`; when one turn reads several messages,
  the hub numbers them again for the whole turn. The Claude adapter sends
  them as image content blocks before the text, on the session's stdin, so
  a remote thread gets them through ssh.
- Every coordinator turn starts with the board: each open thread, its state,
  its last report while it runs, and its recap if it ended a turn since the
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
| threads only | `recap` |
| coordinator only | `open_thread`, `resolve_thread`, `notify`, `workspace_recap` |
| internal | `permission_prompt` |

A turn ends when Claude's reply ends, and only a message starts the next
one. `wake_me(minutes, note)` asks the hub for that message later, for work
that outlives the turn, such as a long benchmark. A thread has at most one
wake-up; it is saved with the thread, so it survives a restart, and one
whose time passed while the hub was down comes at once. Go timers stop while
the computer sleeps, so the hub also checks the wall clock each minute: a
wake-up comes at most a minute after the computer wakes. The threads list
shows `wakes at 16:52` on an idle thread that has one, and never a time that
has passed.

## Recaps

A thread's recap says what its work produced, so the person learns it
without the transcript. It has two parts that stay apart, so a claim never
reads as a check:

- **The thread's words**: `recap(asked, outcome, open?)`, its last call in a
  turn in which its work moved. `open` is optional, so "nothing open" is no
  value rather than words that say so.
- **What the hub saw** that turn: the commands it ran and which failed, the
  files it edited, the artifacts it wrote, and the error if the turn failed.
  The adapter says which tool calls run a command or edit a file
  (`agent.Act`), and which failed command printed nothing
  (`agent.Event.Silent`, from Claude's bare `Exit code N`); only it knows its
  tools and their output.

The model's words are untrusted input: a different model, a long answer, no
recap, or two calls must still give a good view. So the hub, not the UIs,
shapes the recap, by rules that read no words:

- `Headline` is the outcome's first sentence, at most 120 characters, cut at
  a word. Rows show it; the full view shows `Outcome`.
- `Commands` holds what the hub's facts say matters, not the commands'
  names: each command whose last run failed, and the last 4 run after the
  last edit, which checked the work as it ended, or the last 4 of all when
  none ran after it. A command shows once, at its last run, so a failure a
  later run mended is none, and a silent failure is a no, not a failure.
  `Failed` counts the failures and `Ran` all commands. Paths in the
  thread's folder are made relative to it.
- A recap with no outcome, or none at all, takes the first line of the
  reply, marked `FromReply`. A turn that wrote none and did nothing keeps
  the old recap, and so does one that changed nothing (no edit, no
  artifact, no failure) when the old recap is the thread's own: an answer
  to "is it committed?" does not hide the recap of the work.

The recap is kept in `thread.json` and sent as `ThreadView.Recap`. The
person can ask for a fresh one (`POST …/threads/{t}/recap`, `:recap!`, the
app's "ask again"): the hub queues the ask as the person's message, at most
once while it waits, so it runs a turn only on the person's act.

The workspace recap says how the whole workspace stands, as
`WorkspaceView.Recap`. It holds only what no thread's recap says:

- **The coordinator's words**: `workspace_recap(goal, lines)`, its last call
  in a turn in which the board changed: what the workspace is for, and at
  most three lines on what the threads' work means together. Until it writes
  a goal, the goal is the first line of the person's first message to it.
  Both are kept in `workspace.json`.
  So the lines are fresh when the person looks, with `workspace_recap = true`
  (the default) the hub wakes the coordinator once, 15 seconds after the last
  running thread ends, when a thread's recap is newer than its lines. With
  `notify = "coordinator"` the threads' reports wake it already.
- **What the hub joins** from the open threads' recaps, at no cost: the files
  that two or more threads edited (each path joined back to its thread's
  folder), how many idle threads failed their last turn (`Failed`), and how
  many recaps are newer than the lines (`Newer`). The UIs take the counts
  from `WorkspaceView.Counts` and these fields, and count nothing
  themselves.

The UIs show the recaps in a box that pops up when the person comes back.
What is news is the hub's; when to show it is the UI's, as only it knows
focus and typing:

- **Seen** is one mark in the hub, so the TUI and the app agree. Closing a
  thread's box marks that thread (`thread.json`), and closing the
  workspace's box marks the workspace and so every thread
  (`workspace.json`): `POST …/threads/{t}/recap/seen`, with the coordinator
  for the workspace. `ThreadView.RecapUnseen` says the thread's recap is
  newer than both marks; `WorkspaceView.RecapUnseen` says an open thread's
  is, or the coordinator's lines are newer than the workspace's mark.
- **When**: the box shows when the person comes back to news, and only
  then: the window gets focus after `model.RecapAway` (two minutes) away,
  or the person opens a workspace not looked at for as long. A terminal
  that sends no focus events counts the first key after as long with none.
  Never while the person types (Insert, a command line) or over a
  permission or question card in the window with focus, and never with
  `recap` off. In a thread's window it shows the thread's recap when that
  is news, else the workspace's.

## Permissions

When Claude asks for approval, it calls `permission_prompt`. The hub holds
the call open, sets the thread to waiting, and the TUI shows a card.

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
