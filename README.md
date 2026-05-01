# agentflash

Real-time visualizer of filesystem activity in a watched directory, with
optional correlation to Claude Code tool calls. macOS only.

The browser UI shows a "seismograph tape" of every file read/write that
happens under the watched directory, plus a live tree on the left that
auto-updates as files appear or disappear. When wired up to Claude Code
hooks, the top bar also shows what Claude is currently doing (current
goal, tool, subagent, idle/working/waiting state) with a dedicated
green tick lane on the canvas.

## How it works

Three signal sources feed one WebSocket-driven UI:

```
fs_usage  (sudo)        ──┐
FSEvents  (no sudo)     ──┼─►  hub  ─►  WS  ─►  browser canvas + tree
Claude Code hooks (HTTP) ─┘
```

- **fs_usage** is the only thing on macOS that can observe `read()`
  syscalls, so the privileged "tap" subprocess parses its output.
- **FSEvents** drives the file tree's live update and provides write
  events that fs_usage misses (e.g. zsh redirect builtins, which are
  hidden from fs_usage by SIP).
- **Claude Code hooks** post tool-call payloads to the user-mode HTTP
  server.

Architecturally there are two processes: a small privileged `tap` that
does nothing except parse `fs_usage`, and the unprivileged UI server
that owns HTTP/WebSocket, the file tree, and the FSEvents watcher. The
UI re-execs itself under sudo to spawn the tap; only one password
prompt per launch.

## Build & run

```sh
make build           # produces ./bin/agentflash
./bin/agentflash --dir ~/some/project
```

Open <http://127.0.0.1:7777>. You'll be prompted once for your sudo
password (used to start `fs_usage`).

## Claude Code integration

```sh
./bin/agentflash hooks --apply              # merge into ~/.claude/settings.json
./bin/agentflash hooks --apply --path .claude/settings.json   # project-scoped
./bin/agentflash hooks                      # just print, don't modify
```

The merge is idempotent — re-running replaces the existing entries
rather than duplicating them, and unrelated hooks under the same event
are preserved. Events from Claude sessions whose `cwd` isn't inside the
watched dir are silently dropped.

## Flags

| Flag | Default | Notes |
|---|---|---|
| `--dir` | _(required)_ | Directory to watch |
| `--addr` | `127.0.0.1:7777` | HTTP listen address |
| `--buffer` | `10000` | Ring buffer size for replayed history |
| `--debug` | `false` | Verbose stderr: hub stats, FSEvents, tap samples, ws connects |
| `--raw-dump <file>` | _off_ | Append every raw `fs_usage` line to a file (debug) |

Subcommands:

- `agentflash --dir <path>` — main UI (default).
- `agentflash hooks [--apply] [--path <file>]` — print or merge the
  Claude Code hooks block.
- `agentflash __tap` — internal; spawned via sudo by the UI process.

## UI cheatsheet

- **Top bar**: watched dir, Claude `goal:` / `do:` / `[subagent]` /
  state pill, refresh / stop / window selector / path filter, status
  and event counters.
- **Tree (left)**: lazy-loaded, auto-updates via FSEvents. Two buttons
  at the bottom toggle expand-all/collapse-all and hide-hidden.
- **Timeline (right)**: ticks at each file's row.
  - Blue = read
  - Red = modify (write/rename/mkdir/chmod/utimes/etc.)
  - Orange = delete _(currently disabled — tree disappearance is the
    visible signal)_
  - Green / blue (top lane) = Claude tool call (subagent → blue)
- Hover any tick for full path, op, process, and Claude details.

## Known limitations (macOS specifics)

- Requires sudo (for `fs_usage`).
- `fs_usage` cannot see syscalls from hardened-runtime binaries (e.g.
  zsh's builtin `>` redirect emits no `open()` to fs_usage). FSEvents
  fills the gap for write detection.
- `fs_usage` truncates very long paths even with `-w`.
- Process names with spaces (e.g. `Code Helper (Renderer)`) parse
  imperfectly into the trailing `process.pid` field.

## Layout

```
agentflash/
├── main.go                  # subcommand dispatch
├── internal/
│   ├── event/               # shared Event + ClaudeInfo wire format
│   ├── tap/                 # fs_usage parser + filter loop
│   ├── treewatch/           # FSEvents wrapper (rjeczalik/notify)
│   └── ui/                  # HTTP, WS, hub, hooks endpoint
└── web/                     # embedded index.html / app.js / style.css
```
