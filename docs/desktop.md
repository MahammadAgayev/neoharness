# Desktop app

The desktop app is neoharness in a macOS window. It talks to the same hub as
the TUI, so both show the same workspaces and threads at the same time, and a
turn goes on when either one closes.

It is an Electron app in `desktop/`: React and TypeScript, CodeMirror with Vim
for the prompt, and ProseMirror for the artifact page.

## Run it

```sh
cd desktop
npm install
npm run dev        # the app, with reload on change
npm run build      # out/
npm run dist       # dist/neoharness-darwin-arm64/neoharness.app
```

To install, run `./install_all.sh` from the repository root: it installs
the binary as `install.sh` does, builds the app and copies it to
`~/Applications` (`APPS=` changes the folder), then restarts the hub on the
new binary, which stops the running turns. Or copy `neoharness.app` to
`/Applications` or `~/Applications` yourself.
`npm run dist` signs the app ad hoc, which is enough on the Mac that built
it. It is not signed with an Apple developer ID nor notarized, so on another
Mac the first open needs a right click and Open.
`build/icon.svg` is the icon; `build/icon.icns` is made from it with `sips`
and `iconutil`.

The app starts the hub when none answers, as `neoharness` does. It runs
`neoharness hub start` in a login shell, so the hub gets your PATH and
environment even when the app opens from Finder. `NEOHARNESS_BIN` names the
binary instead; `NEOHARNESS_HOME` picks another home, as for the TUI.

After an update the hub still runs the old binary. On connect the app
compares the hub's version with `neoharness version`. If they differ, it
restarts the hub on the new one: at once when no turn runs, else when the
turns end, so an update cuts no turn. `:hub restart` (or Restart the hub in
search) does it by hand, after a yes when turns run.

## What is the same as the TUI

Everything the TUI does is here, with the same keys: workspaces, the
coordinator and the threads list, the chat with its queue, permission cards,
questions, `!` commands, `/` and `@` completion, artifacts with merge on save,
splits, toasts, marks (`ma`, `'a`, `''`), and the ex commands (`:new`, `:open`, `:resolve`, `:setlocal`,
`:restore`, `:allow`, `:w`, `:e!`, …). `:help` lists them all. `<Tab>`
completes the folder of `:new`, `:open` and `:folder` when it starts with
`~/` or `/`; the app has no working folder for a relative one.

A question waits as a card in the chat, as a permission request does:
write your answer below, or Skip (`<leader>x`). Once answered it is a
plain block.

The recap card shows as in the TUI, just above the prompt, when you come
back after 2 minutes or more out of the window, or with no key or click
for that long. `Esc`, `q` or `×` closes it until a newer recap, and so
does a message to the session. `:recap` shows it again.

The todo list shows above the prompt as in the TUI: one line,
`▸ 3 of 7 · ` and the item in hand, while it has open items. `za` or
`<CR>` on it, a click, or `<leader>T` unfolds it; done items are struck
through, there, in `:todo`, and as tasks in replies and artifacts.

The threads list groups threads by state with a count, as in the TUI. A
row shows the thread's name and, on the right, how long it has run or
waited, or when it last changed (`5m`, `6d`, then the date). Under the
name, a waiting thread shows what it waits for and an idle one the start
of its last reply; with nothing to say, the row is the name alone.

The app does not read `init.lua`; the hub settings in it apply to both.
What the TUI takes from `init.lua` for itself is in Settings instead.

## Settings

`<leader>s`, `⌘,` or `:settings` opens them. A change applies at once and is kept.

- **Theme**: System follows the macOS appearance; Moon (the TUI's), Night
  and Dawn (light). `:colorscheme` sets it too.
- **Text size**, the **sidebar**, **Vim keys**, and the **compact warning**:
  the context % at which the prompt says `/compact`.
- **Auto compact**: the context % at which a session of the open workspace
  compacts by itself before its next turn; 0 is off. The hub does the
  compacting, so this is the workspace's `:setlocal auto_compact`; it is set
  on Enter or when you leave the field. `auto_compact` in `init.lua` sets
  it for every workspace.
- **Leader**: click it and press a key; `<Space>` by default.
- **Key hints**: after a key that starts others, as `<leader>`, `<C-w>` or
  `g`, what can follow shows at once, as which-key does in Neovim. The keys
  wait while the hints show; `<Esc>` drops them. Off, they time out after a
  second, as in Vim.
- **Your keys**: click the keys field and press the keys, as `<leader>z`
  or `⌘⇧L`; Backspace takes one back. Then pick a command, an ex command
  (`:colorscheme rose-pine-dawn`), or Nothing, which turns a default off.
  Your keys win over the defaults.

### Artifacts

An artifact opens as a page: one centered column, about 90 characters
wide, with the chat's type a size up. Headings, lists, task boxes,
tables, quotes, alerts and highlighted code show rendered, and they stay
rendered while you write, as in a word processor. No markdown shows in
Insert mode.

The keys are Vim's where they fit. Normal mode moves: `h j k l`, `w b e`,
`0 $`, `gg G`, `{ }`, `<C-d> <C-u>`, `/` and `n N`. A block (a paragraph,
a heading, a list item, a table cell) is Vim's line, so `dd`, `yy`, `p`
and `o` act on blocks, and `cw`, `ciw`, `x`, `u` and `<C-r>` work as in
Vim. `v` and `V` select; so does a mouse drag, as with Vim and a mouse,
and a click leaves Visual mode. `j` and `k` keep the cursor four lines
from the edge and scroll the text under it, as Vim's `scrolloff`. `i`,
`a` or `o` start Insert; `<Esc>` leaves it.
Normal mode types nothing. The cursor is a block in Normal and Visual mode and a
line in Insert, as in Vim and in the prompt. Without Vim (`:set novim`) the page is always
in Insert, as a text editor is.

In Insert, markdown at the start of a line makes the block: `# ` a
heading, `- ` or `1. ` a list, `> ` a quote, ` ``` ` code, `[ ] ` a task,
`---` a rule; `<BS>` right after gives back what you typed.
`**bold**`, `*italic*`, `` `code` `` and `~~struck~~` turn rich as you
close them. `<Tab>` and `<S-Tab>` indent a list item, and `<CR>` on an empty
one takes it out a level; in a table `<Tab>`
goes to the next cell and adds a row at the end, and `<CR>` goes to the
cell below; on an empty last row it leaves the table. A click on a task box
checks it and strikes its text through. A link pasted over selected words links them. `:w` or `⌘S`
saves.

An artifact named in the page is a link to it, as in the TUI: a code
span that is its name, a link to its name, or in prose a name with a
`-`, `_` or digit in it. A click opens it, or a web link, in Normal
mode; in Insert, where a click places the cursor, `⌘`-click does.
`gf` and `<CR>` on it open it too, and `<C-o>` goes back.

What is saved is markdown, as agents write and read it. A block you did
not touch is saved as the exact text it came from, with the blank lines
around it; only a block you changed is written again, in plain GFM. So a
save changes only the lines you changed, and the writer's diff and the
merge stay small. Markdown the page has no block for (a link definition)
shows as muted text and is kept as it is. `test/mdoc.test.ts` checks the
round trip on the docs of this repository: unchanged text comes back
byte for byte, and text with every block written again reads the same.
`NEOHARNESS_ARTIFACTS=<folder>` adds the artifacts in a folder.

Why ProseMirror: it keeps a document of typed blocks, so a heading stays
a heading while you write in it, and `prosemirror-markdown` and
`prosemirror-tables` give the markdown in and out and the tables. A
CodeMirror live preview shows the markdown on the lines under the cursor,
which is the raw text this page removes. Editors with their own markdown
layer (Milkdown, Tiptap) add a framework over ProseMirror and change
the text on save; here the untouched blocks must stay byte for byte.

If an agent writes a new version while you edit, a line says so, and
nothing in your edit changes. `:w` merges: the hub takes both sides when
they changed different lines. When they changed the same lines, the save
is refused and you take the changes one by one, as below.

### Docs

`<leader>d`, `⌘⇧N`, New doc in the sidebar or `:doc <name>` starts a
document you write: a plan, a spec, a list of tasks. It opens beside the
chat, ready to type. When you save, the coordinator holds it: its next turn
reads it in full, works from it, and keeps its status true as the threads
report, with your words kept. Your later edits reach it as a diff, and a
write over an edit it has not seen is refused, so neither side loses work.

When a save is refused because the agent changed the same lines, the window
shows your edit against the newer version change by change, as the TUI's
diff mode: `]c` `[c` move, `do` (or `dp`, or the Take button) takes one
change, `:w` saves the result, `:e!` takes all of the newer version.

## What the window adds

- **Search everywhere**: `⌘K`, `<leader><leader>`, or the box in the title
  bar. It searches
  threads of every workspace, artifacts, workspaces, commands, notifications
  and the text of every transcript at once. `<Tab>` goes to one kind. The
  pickers (`<leader>pt`, `<leader>pa`, `<leader>ps`, …) open it on one kind.
  Its tabs are all, threads, artifacts, text, commands, workspaces and
  notifications. `:ls` and `<leader>pb` pick a thread of this workspace;
  `gO` opens the outline of the window in front (its headings or your
  messages) on its own.
  The footer shows only what the chosen result can do. `<CR>` on a thread
  of another workspace goes to that workspace; `<C-v>` and `<C-x>` show it
  in a split beside the one you work in, its workspace name in foam on its
  status line, so you answer it without leaving.
- **macOS shortcuts** next to the Vim keys: `⌘P` threads, `⌘⇧F` search the
  transcripts, `⌘.` stop the turn, `⌘N` open a thread, `⌘S` save, `⌘W` close
  the window, `⌘\` split, `⌘1`…`⌘9` workspaces, `⌘/` help (also `<leader>h`, as `<leader>s` is settings), `⌘[` `⌘]` back and forward (`<C-o>` `<C-i>`).
  The arrows at the top left go back and forward too, as in Slack; their
  tooltips show the keys.
  Help opens as a dialog, as the settings do: Esc closes it, j and k scroll it.
- **Todo list** (`:todo [name]`): the todo list of the thread in the
  window, or of another, in the window as in the TUI. It follows the work,
  and `<C-o>` goes back.
- **Cost** (`<leader>$`, `:cost`, or Cost in the sidebar): what the
  workspace cost, in all and today, the last 14 days as bars, by thread and
  by model, and the turns that cost the most; a click opens the thread. Tab
  shows every workspace (`:cost all`), by workspace. Cost shows nowhere
  else: not in the sidebar, not under the prompt. The hub keeps a log of
  each paid turn for it; the totals count the turns from before the log too.
- **`/model` and `/effort`** with no name open a picker, as in Claude Code;
  a click on the model under the prompt opens it too. The models, their
  versions and descriptions are Claude Code's own: the hub asks `claude`
  once and keeps the answer. The effort levels are those of the model the
  thread runs. The choice holds for the thread from its next turn.
- **Window keys from Insert**: `<C-w>` followed by a window key (`h j k l w c
  s v o q =`) leaves Insert and acts on the window, so a doc you write in is
  no trap. `<C-w>` alone, or followed by any other key, still deletes a word.
  `<C-o>` and `<C-i>` are the app's back and forward in Insert too, over
  Vim's one Normal command, and `:w` saves from Visual mode too.
- **A sidebar** of workspaces, each with what waits or runs in it. `⌘B`,
  `:set nosidebar` or the button at the top left folds it to a rail of
  icons, as Chrome's vertical tabs: the mouse on the rail shows it whole,
  over the windows. Drag its edge to resize it; a double-click on the edge
  resets it. The work sits on a raised panel beside it, and
  icons are Lucide's line icons (ISC). The Rosé Pine text and accents are
  drawn darker on paper and brighter at night, until each reads at a set
  contrast on every background: text 12:1 (15:1 on paper), secondary
  6.5:1, muted text and accents 4.5:1 (`lib/themes.ts`, tested). The TUI
  uses the same night values. The paper and the hues stay Rosé Pine's.
  A reply reads in one ink: headings show their level by size, and inline
  code is the text's color on a plain chip. Prose is about 90 characters
  wide, the chat text two sizes up from the labels, paragraphs a line
  apart, and every size follows the text size.
- **The mouse**: the round send button in the prompt (the accent once there is text), buttons on permission cards, a click on a thread, an
  artifact or a queued message, drag the line between columns, the × at
  the end of a window's status line closes it (`⌘W`). A right
  click on a thread or a block gives its actions (open in a split,
  resolve, stop, copy, delete); hints show on hover. A reply has Copy
  under it on the left, which copies it as markdown; a code block has its
  own Copy on hover.
- **Images**: paste a picture (`⌘V`) into the prompt, and it goes to the
  agent with the message, as in Claude Code. The prompt shows it as a
  small `[image 1]`; delete the token and the image does not go. Up to 5
  per message, png, jpeg, gif or webp. A big one is made smaller first, to
  1568 pixels on its long side, the size the model works at. Taking the
  message back (`u`) brings its images back too. The TUI does the same with
  `:image`, as a terminal cannot paste a picture.
- **Color schemes**: `:colorscheme system` follows the macOS appearance;
  `rose-pine-moon` (the TUI's), `rose-pine` and `rose-pine-dawn` (light).
- **macOS notifications**, quiet: only what needs you (a permission, a
  question, an error, a coordinator notice that needs you), and only when
  the window is not in front. The Dock badge counts the threads that wait.

## Vim or not

`:set novim` makes the editors work as in Claude Code:

| Keys | What |
|---|---|
| `<CR>` | send |
| `⇧<CR>` `Ctrl J` | new line |
| `<Esc>` | as `u`: stop the turn and take your last message back; with none, go to the transcript |
| `Ctrl S` (`Ctrl X Ctrl S`) | stash the draft; it comes back after you send |
| `<CR>` on your message | it comes back to the prompt to edit and send again |

`:set vim` brings Vim back. Settings stay between runs.

## Polish checklist

What the app takes from Linear, and the test that shows each one. `npm run
e2e` writes the screenshots named here to `test-results/`.

| Quality | In the app | Test (screenshot) |
|---|---|---|
| One search for everything | `⌘K`: threads of every workspace, artifacts, commands, text | app: search everywhere finds them (04); showcase (32, 33) |
| Every action has a key you can find | `:help`, the commands tab of `⌘K`, keys as hints on hover, in tooltips and in `:help`, not on buttons | app (06); showcase (35); right click and hint (29) |
| Work without the mouse | Vim keys, or keys as in Claude Code; marks; select and yank | cards: `<leader>a` allows; without Vim; `v` and `y`; marks (27) |
| The mouse works too | Clicks on toasts, blocks and artifacts; right-click menus | cards: a toast click opens the thread; an artifact click; right click on a thread |
| Quiet | A toast or notification only for what needs you; it goes when you look | cards: permission toast goes when the thread shows (20) |
| No modal for the work | Permissions and questions are cards in the chat | cards: permission card (21), question (22) |
| Settings in one place, applied at once | `<leader>s`, `⌘,` | cards: settings (28) |
| Light and dark | System, Moon, Night, Dawn | app: Dawn (05); cards: settings |
| Care with your edits | A refused save resolves change by change | cards: artifact conflict (25) |
| Clear failure | A stopped hub says so and offers to start it | cards: hub stopped (26) |
| Completion where you type | `/` and `@` in the prompt, `<Tab>` for commands and folders | cards: completion (23), folder `<Tab>` |

## Tests

```sh
npm test           # unit tests: transcript, toasts, keys, search, themes, image tokens, the markdown round trip
npm run e2e        # the app on a scratch hub and on a fake hub, with screenshots in test-results/
NEOHARNESS_APP=dist/neoharness-darwin-arm64/neoharness.app/Contents/MacOS/neoharness npx playwright test cards   # the packaged app
NEOHARNESS_LIVE=1 npx playwright test live   # real turns with haiku: a reply, and an artifact the agent rewrites while you edit it
NEOHARNESS_DEMO=1 npx playwright test demo   # records docs/demo/desktop.gif on the fake hub; needs ffmpeg
npm run perf       # the frame budget: each scenario on the fake hub with the CPU slowed 4×; test-results/perf.json
```

`npm run perf` measures what the person feels: frames while a long chat
streams, scrolls and moves, the rail folds, and keys are typed in the prompt
and the page. At 120 Hz a frame is 8.3 ms, so the budget is at most one frame
over 17 ms and no long task per scenario, and a key shows within 16 ms. It
runs with the CPU slowed 4× (`PERF_THROTTLE=1` for full speed), as on an
older or busy Mac. A scenario over budget fails, except those listed in
`known` in `test/perf.perf.ts` with the change that brings them in. Run it
with nothing else loading the Mac, as other work moves the numbers.

The end-to-end tests make their own `NEOHARNESS_HOME` and MCP port and stop
their hub after. They never touch `~/.neoharness`. `test/fakehub.ts` is the
hub API with no model behind it: the card tests push permission requests,
questions, artifacts and a stop through it. Another `NEOHARNESS_HOME` also
gets its own app settings. The tests show the window without focus, so you
can watch a run and keep typing elsewhere; they send no notifications.
`NEOHARNESS_DESKTOP_TEST=hidden npm run e2e` runs with no window and no Dock
icon.

## Rules

- The app sees only the neutral fields of the hub API, as the TUI does.
  `src/shared/model.ts` follows `api/model`; change both together.
- The page has no Node: it names a hub call (`src/shared/api.ts`) and the
  main process makes the request. Agent markdown is sanitized before it is
  shown.
- `desktop/.npmrc` pins the public npm registry. Check that
  `package-lock.json` names no other registry before you commit it.
- Styles are one file per part of the window in `src/renderer/styles/`,
  imported in cascade order by `index.css`. They use the tokens of
  `tokens.css` (space, radius, hairline, motion, color roles) and the
  scheme's colors from `lib/themes.ts` (`--bg`, `--fg`, `--fg-2`, `--fg-3`,
  …), never a raw color.
