# MultiTUI

Launch coding agents in tmux panes, tiled in a single terminal window.
Go and Bubble Tea provide the launcher and session browser; tmux runs each
agent's existing terminal UI and handles its input.

Download prebuilt macOS and Linux binaries from the
[GitHub releases](https://github.com/KThomasMurrayL/MultiTUI/releases).

```sh
mt claude 4
mt codex 2 --layout even-horizontal --session feat-x
mt opencode --cwd ~/project --no-attach
```

## Build and install

Requires macOS or Linux, tmux **3.0 or newer**, and the agents you intend to use
installed and authenticated. Building requires **Go 1.24 or newer**.

```sh
# macOS
brew install go tmux

# Build from this directory
make build
./bin/mt --help
./bin/mt claude 4

# Optional installation into a user-owned directory
make install PREFIX="$HOME/.local"
```

On Linux, install tmux through your distribution's package manager and Go from
[the official downloads](https://go.dev/dl/). Add your installation's `bin`
directory to `PATH`. Release binaries are built with `CGO_ENABLED=0` and need
no Go installation at runtime; tmux and your agents remain separate prerequisites.

## Usage

| Command | Behavior |
| --- | --- |
| `mt <agent> [count]` | Launch agents; omitted count means one pane |
| `mt` | Bubble Tea agent picker, count stepper, and layout selector |
| `mt ls` | Interactive session browser in a terminal; plain table when piped |
| `mt ls --plain` | Always print the session table |
| `mt attach <session>` | Attach outside tmux; switch clients inside tmux |
| `mt kill <session>` | Kill that managed session and its agents |
| `mt kill all` | Kill every MultiTUI-managed session on this tmux server |
| `mt --version` | Print version |

Flags can appear before or after positional arguments:

| Flag | Default |
| --- | --- |
| `--layout tiled\|even-horizontal\|even-vertical\|main-vertical` | Config default, initially `tiled` |
| `--session <name>` | `mt-<agent>-<UTC timestamp>` |
| `--cwd <directory>` | Current working directory, shared by all panes |
| `--no-attach` | False; if set, create detached and print only its name |
| `--config <path>` | `$XDG_CONFIG_HOME/mt/config.yaml` or `~/.config/mt/config.yaml` |

Explicit session names are preserved, so `--session feat-x` creates `feat-x`.
Names must start with a letter or digit and use letters, digits, `_`, or `-`.
`all` is reserved for management. Auto-generated names start with `mt-`.
Every created session gets the `@mt-managed=1` tmux option; listing and killing
use that tag, so unrelated sessions with an `mt-` prefix are left alone.
Management uses the current tmux server (including the server selected by
`$TMUX` when already inside tmux).

Count must be a positive integer. Even one agent runs in a tmux session. More
than eight panes produces a warning. Practical capacity depends on terminal
size and layout: 2–8 agents are usually more usable than many tiny panes. If
splitting fails, the newly created session is cleaned up. Use a larger terminal
or fewer agents. A detached launch uses a 120×40 window if no terminal size is
available; tmux resizes it when a client attaches.

## Controls

Launcher: Tab or up/down selects a field, left/right or `+`/`-` changes its
value, Enter launches, `?` opens help, and Esc or `q` cancels. The session browser
uses up/down to select, Enter to attach, `r` to refresh, `x` then `y` to kill,
and `?` for help.

Once attached, use tmux's keys. With its default **Ctrl-b** prefix:

- **Ctrl-b, then arrow**: move between panes.
- **Ctrl-b, then z**: zoom/unzoom the current pane.
- **Ctrl-b, then d**: detach and keep agents running.
- **Ctrl-b, then [**: enter scrollback/copy mode.

Your existing tmux configuration and prefix still apply. Agent exit normally
returns to the pane's shell. Agents receive the environment inherited by `mt`,
plus any preset overrides. Each pane starts `$SHELL` (or `/bin/sh` when unset).
All panes share the same directory; coordinate edits to shared files. Per-agent
worktrees, saved layouts, and synchronized input are deferred to v2.

## Configuration

The configuration file is optional. The three built-in presets are `claude`,
`codex`, and `opencode`. Custom entries add to them; entries with existing names
replace that preset. See [config.example.yaml](config.example.yaml).

```yaml
agents:
  claude: {cmd: claude}
  codex: {cmd: codex}
  opencode: {cmd: opencode}
  review:
    cmd: codex
    args: ["--model", "your-model"]
    env:
      MODE: review
defaults:
  layout: tiled
  kill_on_detach: false
```

`cmd` is an executable name or path; put each argument in `args`. Relative
executable paths and relative `PATH` entries resolve from `--cwd`. A preset's
`env.PATH` controls executable lookup if supplied. Values are quoted literally,
including spaces, quotes, and shell metacharacters. Shell expressions in `cmd`
are not evaluated; an intentional wrapper can be an executable script, or use
`cmd: /bin/sh` with `args: ["-c", "your script"]`. Control characters in command
or environment values are rejected because launch commands go through tmux
`send-keys`. Unknown fields, invalid layouts, duplicate YAML keys, and multiple
YAML documents fail with a clear error.

When `kill_on_detach` is true, a session-local attach hook enables tmux's native
cleanup after its last client detaches, disconnects, or switches away, including
while `mt` is no longer running. A session
created with `--no-attach` remains available until a client attaches and then
detaches. The default keeps sessions alive. `mt kill` immediately destroys the
session and every process running in its panes.

## Verification

```sh
make check           # go vet and unit/CLI tests with the race detector
make integration     # real tmux: isolated test sockets, launch, cleanup, detach
make fmt             # standard Go formatting
```

Unit tests exercise command quoting, strict configuration, CLI dispatch,
per-pane launch, version checks, managed-session filtering, cleanup, and UI
state transitions. Integration tests require tmux and use unique server sockets
with an empty tmux config. The [GitHub Actions template](ci/github-actions.yml)
runs both suites on macOS and Linux and builds all four release targets. To
enable it, copy that file to `.github/workflows/ci.yml` using a GitHub login with
workflow permission; the publishing login used for the initial release does
not have that permission.

## Distribution

```sh
make release VERSION=0.1.0
```

This verifies the source and builds tarballs for macOS/Linux, amd64/arm64, plus
SHA-256 checksums in `dist/`. Each archive includes `mt`, this README, and the
example configuration. Extract the archive for your OS/architecture and place
`mt` in a directory on `PATH`.

For Homebrew distribution, generate a formula from the actual tarballs:

```sh
python3 scripts/homebrew.py --repo KThomasMurrayL/MultiTUI --version 0.1.0
```

Upload the tarballs and checksums to the repository's `v0.1.0` release, then place the generated `dist/mt.rb`
in your Homebrew tap's `Formula/` directory. The formula selects the correct
platform binary and depends on tmux. Release creation, uploads, and tap
publication are explicit maintainer steps; the scripts only create local files.
