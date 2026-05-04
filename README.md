# agentflash

Real-time visualizer of filesystem activity in a watched directory, with
optional correlation to Claude Code tool calls. Runs on macOS and Linux.

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

- **fs_usage** (macOS) / **fanotify** (Linux) is the kernel-level
  read-event source. The privileged "tap" subprocess wraps it.
- **FSEvents** (macOS) / **inotify** (Linux), via `rjeczalik/notify`,
  drives the file tree's live update and provides write events the
  syscall tracer may miss (e.g. zsh redirect builtins on macOS, which
  SIP hides from fs_usage).
- **Claude Code hooks** post tool-call payloads to the user-mode HTTP
  server.

Architecturally there are two processes: a small privileged `tap`
subprocess (spawned via `sudo`) that does nothing except stream kernel
events, and the unprivileged UI server that owns HTTP/WebSocket, the
file tree, and the FSEvents watcher. Only the tap runs as root; the UI
process itself remains unprivileged throughout. One sudo password
prompt covers the whole session.

## Requirements

### macOS

| Requirement | Notes |
|---|---|
| macOS 12 Monterey or later | Older releases may work but are untested |
| `sudo` / root | `fs_usage` requires root |
| Go 1.25+ | Build-time only; the shipped binary has no runtime dependency |

### Linux

| Requirement | Notes |
|---|---|
| Kernel ≥ 4.x | fanotify has been in-kernel since 2.6.36; FD-based `FAN_CLASS_NOTIF` needs ≥ 3.8 |
| Kernel ≥ 5.1 recommended | Enables `FAN_MARK_FILESYSTEM` (whole-filesystem mark); older kernels fall back to per-mount automatically |
| `CAP_SYS_ADMIN` (`sudo`) | Required for `fanotify_init` regardless of kernel version |
| `/proc` filesystem mounted | Used to resolve process names from `/proc/<pid>/comm` and file paths from `/proc/self/fd/<n>` |
| Go 1.25+ | Build-time only |

No extra packages are needed — fanotify is part of the kernel and we call it directly via `golang.org/x/sys/unix`.

### Network / container environments

- **NFS mounts**: fanotify only observes I/O that the _local_ kernel initiates. Writes made by remote NFS clients bypass the local VFS event path entirely and are invisible. The same applies to inotify (used for the file tree). agentflash prints a warning at startup if the watched directory is on an NFS, CIFS/SMB, or FUSE filesystem. It will still track activity from local processes on that mount.
- **Docker / Podman bind mounts**: work normally — the host kernel sees all I/O through its own VFS.
- **Kubernetes / remote dev environments**: if the project directory is a network-backed volume (NFS, CIFS, or a CSI driver backed by one), the NFS caveat above applies.
- **WSL2**: the Linux fanotify tap works inside WSL2, but files accessed through the `/mnt/c/…` Windows interop path are on a FUSE-backed mount (`9p`) and subject to the same remote-write visibility limitation.

## Install

Download a pre-built binary from the [releases page](https://github.com/sjzasada/agentflash/releases):

```sh
# macOS — Apple Silicon (M1/M2/M3)
curl -fsSL https://github.com/sjzasada/agentflash/releases/latest/download/agentflash_latest_darwin_arm64.tar.gz | tar xz
sudo mv agentflash /usr/local/bin/

# macOS — Intel
curl -fsSL https://github.com/sjzasada/agentflash/releases/latest/download/agentflash_latest_darwin_amd64.tar.gz | tar xz
sudo mv agentflash /usr/local/bin/

# Linux — amd64
curl -fsSL https://github.com/sjzasada/agentflash/releases/latest/download/agentflash_latest_linux_amd64.tar.gz | tar xz
sudo mv agentflash /usr/local/bin/

# Linux — arm64
curl -fsSL https://github.com/sjzasada/agentflash/releases/latest/download/agentflash_latest_linux_arm64.tar.gz | tar xz
sudo mv agentflash /usr/local/bin/
```

Replace `latest` with a specific version tag (e.g. `v1.0.0`) to pin a release.
Each release includes a `checksums.txt` for SHA-256 verification.

Verify your install: `agentflash --version`

## Build & run

```sh
make build           # native build for the current OS
make build-all       # also cross-compile Linux amd64 + arm64 from a Mac
./bin/agentflash --dir ~/some/project
```

Open <http://127.0.0.1:7777>. You'll be prompted once for your sudo
password (used to start `fs_usage` on macOS, `fanotify` on Linux —
both require root).

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

### UI flags (`agentflash --dir <path> ...`)

| Flag | Default | Notes |
|---|---|---|
| `--dir` | _(required)_ | Directory to watch |
| `--addr` | `127.0.0.1:7777` | HTTP listen address |
| `--buffer` | `10000` | Ring buffer size for replayed history |
| `--auto-pause` | `false` | Pause the timeline when Claude's Stop hook fires; resumes on next prompt |
| `--debug` | `false` | Verbose stderr: hub stats, FSEvents, tap samples, ws connects |
| `--raw-dump <file>` | _off_ | Append every raw kernel-tap (`fs_usage` / `fanotify`) line to a file (debug) |

### `hooks` flags (`agentflash hooks ...`)

| Flag | Default | Notes |
|---|---|---|
| `--addr` | `127.0.0.1:7777` | Address of the running agentflash UI that hooks will POST to |
| `--apply` | `false` | Merge into the settings file instead of printing |
| `--path` | `~/.claude/settings.json` | Settings file to print or modify |

### `__tap` flags (internal)

The `__tap` subcommand is spawned by the UI under sudo; its flags are not
intended to be invoked by hand. Listed here for completeness:

| Flag | Default | Notes |
|---|---|---|
| `--dir` | _(required)_ | Directory to watch |
| `--exclude-pid` | _empty_ | Comma-separated PIDs to drop events from |
| `--exclude-name` | _empty_ | Comma-separated process names to drop events from |
| `--raw-dump <file>` | _off_ | Append every raw kernel-tap line to a file (debug) |
| `--debug` | `false` | Verbose tap diagnostics |

Subcommands:

- `agentflash --dir <path>` — main UI (default).
- `agentflash hooks [--addr <host:port>] [--apply] [--path <file>]` —
  print or merge the Claude Code hooks block.
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

## Known limitations

**macOS**

- Requires sudo (for `fs_usage`).
- `fs_usage` cannot see syscalls from hardened-runtime binaries (e.g.
  zsh's builtin `>` redirect emits no `open()` to fs_usage). FSEvents
  fills the gap for write detection.
- `fs_usage` truncates very long paths even with `-w`.
- Process names with spaces (e.g. `Code Helper (Renderer)`) parse
  imperfectly into the trailing `process.pid` field.

**Linux**

- Requires sudo (fanotify needs `CAP_SYS_ADMIN`).
- Process names from `/proc/<pid>/comm` are kernel-truncated to 15
  characters. The default exclude list uses these truncated forms.
- A short-lived process can exit between event delivery and our
  `/proc` lookup; in that case the process column is blank.
- Files deleted between event and our `/proc/self/fd` readlink show up
  with a `(deleted)` suffix on the path. Best-effort.
- **Network filesystems (NFS, CIFS/SMB, FUSE)**: fanotify and inotify
  only fire for I/O the local kernel performs. Writes from remote
  clients are invisible. agentflash prints a warning at startup when
  it detects the watch directory is on a network filesystem. Local
  processes accessing the same mount are still tracked normally.
- **`FAN_MARK_FILESYSTEM` vs per-mount**: on kernels < 5.1 or when
  the filesystem doesn't support filesystem-level marks, agentflash
  silently falls back to a per-mount mark. Coverage is identical for
  most setups; on a system where the watched directory spans multiple
  bind mounts the per-mount mark may miss events on the submounts.

## Layout

```
agentflash/
├── main.go                  # subcommand dispatch
├── internal/
│   ├── event/               # shared Event + ClaudeInfo wire format
│   ├── tap/
│   │   ├── tap.go           # OS-agnostic glue (Config, helpers)
│   │   ├── tap_darwin.go    # fs_usage subprocess + parser
│   │   ├── parse.go         # fs_usage line parser (darwin only)
│   │   ├── tap_linux.go     # fanotify Run loop
│   │   ├── fanotify_linux.go # FanotifyInit/Mark + event decoder
│   │   ├── proc_linux.go    # /proc/<pid>/comm reader
│   │   └── exclude_*.go     # per-OS noise process lists
│   ├── treewatch/           # FSEvents (darwin) / inotify (linux)
│   └── ui/                  # HTTP, WS, hub, hooks endpoint
└── web/                     # embedded index.html / app.js / style.css
```
