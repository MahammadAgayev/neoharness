# neoharness

Work with many Claude Code sessions at the same time, in one terminal, with
Vim keys.

- A **workspace** is one piece of work. You can pin it to a folder.
- The **coordinator** is the Claude session of a workspace. It acts only
  when you write to it. It opens threads, gives them work, and knows their
  status.
- A **thread** is a separate Claude session that does one task, in a local
  folder or on another machine over ssh.
- Threads report progress, send messages to each other, read each other's
  transcripts, and write **artifacts**: markdown documents for you, which you
  can read and edit in place.

neoharness is one program, like Neovim. The hub that runs the sessions lives
in the TUI process: no daemon, no tmux. Agents talk to it through an MCP
server that it adds to each session, so you install nothing in your Claude
setup.

## Install

You need Go 1.26 and `claude` on your PATH.

```sh
git clone https://github.com/MahammadAgayev/neoharness
cd neoharness
./install.sh            # builds to ~/.local/bin; BIN=/other/dir ./install.sh to change
```

## Start

```sh
neoharness
```

```
:new demo ~/src/demo    create a workspace pinned to a folder, and open it
:new demo               the same, but pick the folder in a fuzzy picker
i                       write to the coordinator; <CR> sends, <Esc> goes to Normal
<leader>t               threads pane on the right (<leader> is <Space>)
<leader>pa              pick an artifact
:help                   every key and command
```

Ask the coordinator for work ("split this refactor into three threads"). It
opens threads, and they show in the pane. `<CR>` on a thread opens it. You can
write to any thread directly.

## Docs

- [Usage](docs/usage.md): workspaces, threads, artifacts, keys, commands
- [Configuration](docs/configuration.md): `init.lua`, environment variables
- [Architecture](docs/architecture.md): the hub, turns, MCP tools,
  permissions, remote threads, data on disk
- [Development](docs/development.md): code map, tests, a scratch hub for
  end-to-end runs
