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
stream-json`, `--output-format stream-json`, `--include-partial-messages`,
`--session-id` on the first start and `--resume` after, `--mcp-config`,
`--permission-prompt-tool
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

- The reply streams as the model writes it. The adapter sends the text so
  far of the message it writes now; the hub sends the newest of it at most
  every 50 ms as a `delta` update, with `text`. A delta is never in the
  transcript: the `text` event that follows replaces it, and a turn that
  ends without one sends an empty delta, which drops it. An agent that
  cannot stream sends none, and the reply comes whole.
- Messages that arrive while a thread is busy wait in a queue. All of them
  go into the next turn as one prompt.
- `ask` does not wait: it keeps the question with the thread (an `asked`
  entry in the journal) and the agent ends its turn. The thread waits for
  the person, and the others' messages wait too, so the thread does not
  move on from it. The person's next message is the answer and goes first
  in the next turn, with them; send now with nothing written sends them
  without one. A deny skips it: the thread gets the person's message that
  they skipped it. The adapter turns off Claude's own question tool, which
  no one can answer under `-p`.
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
  keeps the threads' news instead, in the workspace's journal (below): a
  new recap, todo items done, artifacts written, a failed turn, and, for a
  turn that ran tools and wrote no recap, the start of its reply. The board
  shows what came after the workspace's `boardSeq` once, as a `New:` line
  under the thread, then moves `boardSeq` to the end; a restart keeps it.
  With `notify = "coordinator"`, news also wakes the coordinator, 15
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
  person writes or sends now. It closes a question of the turn: nothing
  waits for the person.
- To an agent, a stop cancels the turn's context: the agent asks its CLI
  to interrupt the turn, keeps the conversation, and returns once the turn
  stopped. Claude gets an `interrupt` control request on its stdin, so a
  remote turn stops through ssh too.
- A turn can leave work running in the background: Claude's background
  shells and subagents. The adapter counts it from claude's
  `background_tasks_changed` records and gives the count with the turn's
  result. The thread is then `background`, not idle, and the hub keeps
  reading the session: when the work ends, claude starts a turn by itself
  to read its result. That turn shows and ends as any other, and the
  process is not ended for idleness while work runs. A message goes in at
  once, as claude takes one while its work runs.
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
| all sessions | `whoami`, `workspace_status`, `send_message` (to threads only), `ask`, `wake_me`, `write_artifact`, `read_artifact`, `recap` |
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

## Journal

The transcript is what a session said and did; the journal is what
happened to it that matters outside its chat (`hub/journal.go`). Each
workspace has one, `journal.jsonl`, append-only. An entry is `{seq, at,
thread, kind, id, text, tools, needsYou, level}`, and `seq` rises across
the hub; `level` is the attention level, set once, as for events.
The kinds: `opened`, `started` (a turn), `turn` (it ended well: the reply's
first line and how many tools it ran), `failed`, `recap`, `done` (a todo
item), `artifact`, `asked`, `permission`, `decided`, `notice`, `wrote` (the
person wrote to the session), `dismissed` (a skip, or `x` on a wait),
`seen`, `resolved`, `reopened`.

Nobody empties it; each reader keeps its place. The coordinator's board
reads after `boardSeq` (in `workspace.json`), and the attention agent after
the person last saw each session (`seen`). An entry goes once both have
read it, or its session is gone, and it is 7 days old; a journal keeps at
most 2000 entries. At start the hub moves what came before the journal into
it once: the old `news.jsonl`, and the news a `thread.json` kept.

## Attention

The hub decides how much of the person's attention each event asks for,
once, so the TUI and the app draw the same thing. A client chooses how to
draw a level, never which level an event has.

| Level | Events |
|---|---|
| 0 quiet | text, tool calls, progress, messages, notes |
| 1 ambient | a new artifact; a thread's turn end when `notify = "coordinator"` |
| 2 notice | a turn end, unless it left work running in the background (then 0); a notice from the coordinator |
| 3 needs you | a permission, a question, an error, a notice that needs the person |

- Each event carries its `level`. A thread's view carries the highest level
  still open on it: 3 while something waits on the person, else the highest
  level 1 or 2 since it was last seen.
- `POST /workspaces/{w}/threads/{t}/seen {at}` says the person saw the
  thread. A client sends it after the thread was on screen, in a window in
  front, for `model.SeenDwell` (2 s) without a break. It quiets levels 1 and
  2 that came before `at`, in every client. It never clears level 3.
- The waits list, a thread's level and its review come from the journal
  (`needsOf`): an entry that needs the person opens a wait, and a later one
  closes it. A permission closes when it is `decided`, answered or not. A
  question closes when the person writes or skips it. A failure closes
  when a turn starts, and a notice when the person writes to the
  coordinator or dismisses it; `resolved` closes everything. `seen` marks
  what came before it as read. At start the hub makes the journal agree
  with the threads: a permission is `decided` as lost, since its turn died
  with the hub.
- The waits list is what needs the person in every workspace, in the order
  to go through it: pending permissions and questions, which block a turn;
  then failed turns and notices that need the person; then reviews; each
  oldest first. A review (`kind: review`, `id: review`) is a resting thread
  with unseen news at level 2, a turn end or a notice, and nothing else
  waiting on it. It does not raise the thread to level 3, clients do not
  count it as waiting, and seen or a dismiss takes it off.
- With an attention agent (`attention` in `setup()`), the hub orders the
  list by it (`hub/next.go`). When the waits, an open thread's line on
  the board (its todo progress aside), or the focus change, it waits
  `AttentionDelay` (4 s) for the burst to end, then runs one turn: a new
  conversation with `NoTools`, the focus unless it is set aside, the
  waits numbered from 1, and every open thread as the coordinator's board
  shows it, with the news since the person last saw each one. The reply is a JSON object,
  `{"status": [...], "next": [{"i", "why", "brief"}]}`. Ordered waits come
  first, in its order, with `why` and `brief`; the rest follow in the hub's
  order. `status` is up to 4 lines on how all the work stands; the hub
  keeps it as `model.Status`, `GET /status` returns it, and a `status`
  update carries it when it changes. Clients show it when the person comes
  back. A brief is kept while its
  wait is the same, so the agent writes it once. Its own order changes
  nothing it reads, so it does not wake itself. A failed turn or a reply
  with no object keeps the order there was.
- With a focus, and `focus_edit` not `off`, the reply may hold `"edit":
  {"text", "why"}`: the whole focus as the agent would have it. Under
  `ask` it is the focus's `suggestion`, and a wait of `kind: focus`, `id:
  suggestion`, in `workspace: "~"` and `thread: "focus"`, after the
  reviews. The person's save of the focus ends it; `DELETE
  /workspaces/~/threads/focus/waits/suggestion`
  dismisses it, and the same text is not suggested again. Under `allow` it
  is saved at once as a version by `attention`. `GET /waits` returns it, and a `waits` update carries the whole
  list each time it changes (no `waits` field is an empty list). An item
  leaves only when it is resolved: a permission is answered, a question
  answered or skipped, a turn stopped, a thread resolved or deleted, a
  failed thread runs again, the person writes to the coordinator that sent
  the notice. `DELETE /workspaces/{w}/threads/{t}/waits/{id}` dismisses a
  notice or a failure, or reads a review; a permission or a question is
  answered instead.
- Failures, notices, questions and seen times are kept in `thread.json`, so
  the list survives a restart. A pending permission does not: its turn
  ends with the hub.

## Permissions

When Claude asks for approval, it calls `permission_prompt`. The hub holds
the call open, sets the thread to waiting, and the TUI shows a card. The
turn's 4-hour limit stops while a permission waits.

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
opens it again when it drops, for as long as the hub runs: first after 5 s,
then up to once a minute. `hub.log` says when a tunnel drops and when it is
open again. A turn runs `claude` over ssh in the thread's
folder. File lists, the git branch and `!` commands use the same ssh
command.

## Data on disk

```
~/.neoharness/hub.sock                                             local API socket (0600)
~/.neoharness/hub.log                                              hub log
~/.neoharness/workspaces/<workspace>/workspace.json                name, folder, deleted, the board's place in the journal
~/.neoharness/workspaces/<workspace>/threads/<name>/thread.json    state, queue, session (0600: it holds the MCP token)
~/.neoharness/workspaces/<workspace>/threads/<name>/events.jsonl   transcript, append-only
~/.neoharness/workspaces/<workspace>/threads/<name>/images/        images the person sent, named by content
~/.neoharness/workspaces/<workspace>/artifacts/<name>.md / .json   artifact text / metadata
~/.neoharness/workspaces/<workspace>/costs.jsonl                   one line per paid turn: when, thread, model, cost
~/.neoharness/workspaces/<workspace>/journal.jsonl                 the journal: what happened to its sessions
~/.neoharness/artifacts/focus.md / .json                           the person's focus: text / metadata
~/.neoharness/focus.json                                           the focus set aside, and the edit suggested to it
```

The focus is the one document of the hub, in no workspace. The API reaches
it as the artifact `focus` of the workspace `~`, which is no valid
workspace name, so a client edits and merges it as any artifact. It reads
as `model.FocusTemplate` at version 0 until the person saves it; nothing
else can be written under `~`. Its summary carries `off`, which `POST /focus
{off}` sets, and an update for it has `workspace: "~"`.

`GET /cost?workspace=<name>` adds up what a workspace cost; with no name,
every workspace. The totals per thread count every turn. The days, the
models and the turns that cost the most come from the cost log, which
began later, so they start at its first line (`since`).

An artifact belongs to the session that wrote it last. The person's edit
reaches that session as a diff before its next message. A save from version
0 to a new name starts a document the person writes: the coordinator holds
it and reads it in full on its next turn.

A save from an older version merges by blocks. A block is a run of lines
between blank lines, with fenced code and lists kept whole. Changes to
different blocks both stay, and a block neither side touched comes out byte
for byte. Where both changed one block, its lines merge as a merge tool
does; changes to the same lines are refused. `POST
/workspaces/{w}/artifacts/{a}/merge {text, base, baseText}` gives the merge
a save would get, and writes nothing: `merged` keeps the edit's side where
it conflicts, and each conflict gives its line ranges, `[first, end)` from
0, in the base, the edit (`ours`), the newest version (`theirs`) and
`merged`. A client maps the ranges to its own blocks.

A thread's state is not stored. The hub calculates it from the stored
fields each time, so it cannot drift. `failed` comes after resolved,
waiting and running: the last turn failed and nothing came after it. It
stays until the thread runs again or the person dismisses the failure.
