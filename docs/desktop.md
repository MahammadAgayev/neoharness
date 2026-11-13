# Desktop app

The desktop app is neoharness in a macOS window. It talks to the same hub as
the TUI, so both show the same workspaces and threads at the same time, and a
turn goes on when either one closes.

It is an Electron app in `desktop/`: React and TypeScript, CodeMirror with Vim
for the prompt and the artifact editor.

## Run it

```sh
cd desktop
npm install
npm run dev        # the app, with reload on change
npm run build      # out/
npm run dist       # dist/neoharness-darwin-arm64/neoharness.app
```

To install, copy `neoharness.app` to `/Applications` or `~/Applications`.
The app is not signed, so the first open needs a right click and Open.
`build/icon.svg` is the icon; `build/icon.icns` is made from it with `sips`
and `iconutil`.

The app starts the hub when none answers, as `neoharness` does. It runs
`neoharness hub start` in a login shell, so the hub gets your PATH and
environment even when the app opens from Finder. `NEOHARNESS_BIN` names the
binary instead; `NEOHARNESS_HOME` picks another home, as for the TUI.

## What is the same as the TUI

Everything the TUI does is here, with the same keys: workspaces, the
coordinator and the threads list, the chat with its queue, permission cards,
questions, `!` commands, `/` and `@` completion, artifacts with merge on save,
splits, toasts, marks (`ma`, `'a`, `''`), and the ex commands (`:new`, `:open`, `:resolve`, `:setlocal`,
`:restore`, `:allow`, `:w`, `:e!`, …). `:help` lists them all. `<Tab>`
completes the folder of `:new`, `:open` and `:folder` when it starts with
`~/` or `/`; the app has no working folder for a relative one.

The app does not read `init.lua`; the hub settings in it apply to both.
What the TUI takes from `init.lua` for itself is in Settings instead.

## Settings

`⌘,` or `:settings` opens them. A change applies at once and is kept.

- **Theme**: System follows the macOS appearance; Moon (the TUI's), Night
  and Dawn (light). `:colorscheme` sets it too.
- **Text size**, the **sidebar**, **Vim keys**, and the **compact warning**:
  the context % at which the status line says `/compact`.
- **Leader**: click it and press a key; `<Space>` by default.
- **Your keys**: click the keys field and press the keys, as `<leader>z`
  or `⌘⇧L`; Backspace takes one back. Then pick a command, an ex command
  (`:colorscheme rose-pine-dawn`), or Nothing, which turns a default off.
  Your keys win over the defaults.

When a save is refused because the agent changed the same lines, the window
shows your edit against the newer version change by change, as the TUI's
diff mode: `]c` `[c` move, `do` (or `dp`, or the Take button) takes one
change, `:w` saves the result, `:e!` takes all of the newer version.

## What the window adds

- **Search everywhere**: `⌘K`, or `<leader><leader>`. It searches
  threads of every workspace, artifacts, workspaces, commands, notifications
  and the text of every transcript at once. `<Tab>` goes to one kind. The
  pickers (`<leader>pt`, `<leader>pa`, `<leader>ps`, …) open it on one kind.
- **macOS shortcuts** next to the Vim keys: `⌘P` threads, `⌘⇧F` search the
  transcripts, `⌘.` stop the turn, `⌘N` open a thread, `⌘S` save, `⌘W` close
  the window, `⌘\` split, `⌘1`…`⌘9` workspaces, `⌘/` help.
- **A sidebar** of workspaces, each with what waits or runs in it. `⌘B` or
  `:set nosidebar` hides it.
- **The mouse**: buttons on permission cards, a click on a thread, an
  artifact or a queued message, drag the line between columns. A right
  click on a thread or a block gives its actions (open in a split,
  resolve, stop, copy, delete); hints show on hover.
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
| `⇧<CR>` `⌃J` | new line |
| `<Esc>` | as `u`: stop the turn and take your last message back; with none, go to the transcript |
| `⌃S` (`⌃X ⌃S`) | stash the draft; it comes back after you send |
| `<CR>` on your message | it comes back to the prompt to edit and send again |

`:set vim` brings Vim back. Settings stay between runs.

## Tests

```sh
npm test           # unit tests: transcript, toasts, keys, search, themes
npm run e2e        # the app on a scratch hub and on a fake hub, with screenshots in test-results/
NEOHARNESS_APP=dist/neoharness-darwin-arm64/neoharness.app/Contents/MacOS/neoharness npx playwright test cards   # the packaged app
NEOHARNESS_LIVE=1 npx playwright test live   # one real turn with haiku
```

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
  `src/shared/model.ts` follows `internal/model`; change both together.
- The page has no Node: it names a hub call (`src/shared/api.ts`) and the
  main process makes the request. Agent markdown is sanitized before it is
  shown.
- `desktop/.npmrc` pins the public npm registry. Check that
  `package-lock.json` names no other registry before you commit it.
