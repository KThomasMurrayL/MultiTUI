# MultiTUI — Plan

## Goal
Terminal tool to launch N instances of a coding agent (`codex` / `claude` / `opencode`) tiled in a single terminal window as one application.

Usage shape: `toolname <agent> <count>`, e.g. `mt claude 4`.

## Decisions (confirmed)
- **Target:** macOS + Linux, generic terminal (ANSI, portable).
- **Tiling:** Orchestrate tmux (no custom terminal emulator).
- **Stack:** Go + Bubble Tea (single static binary).
- **UX:** Positional args `toolname vibecoding_tool instance_count`.

## Architecture
Single Go binary that:
1. Resolves `<agent>` to a shell command via config.
2. Creates one detached tmux session (e.g. `mt-claude-<timestamp>`).
3. Splits into N panes, applies layout, `send-keys` the agent command in each pane.
4. Attaches (`tmux attach-session -t <session>`). One terminal window = one app with N tiled agents.

Bubble Tea is the outer UX only (launcher / session manager). Tiling, PTY, and input routing stay in tmux so full-screen agent TUIs work unchanged.

## CLI Spec
- `mt <agent> [count] [flags]` — `count` defaults to 1 if omitted.
  - e.g. `mt claude 4`, `mt codex 2 --layout even-horizontal --session feat-x`
- Flags:
  - `--layout tiled|even-horizontal|even-vertical|main-vertical` (default: `tiled`)
  - `--session <name>` (default: auto `mt-<agent>-<ts>`)
  - `--cwd <dir>` (default: current dir, shared by all panes in v1)
  - `--no-attach` (create detached, print session name)
- Management:
  - `mt ls` — list managed sessions
  - `mt attach <session>`
  - `mt kill <session|all>`

## Config (`~/.config/mt/config.yaml`)
```yaml
agents:
  claude: { cmd: "claude" }
  codex: { cmd: "codex" }
  opencode: { cmd: "opencode" }
# extensible: custom entries map name -> cmd + args + env
defaults:
  layout: tiled
  kill_on_detach: false
```

## Go Layout
- `cmd/mt/main.go` — CLI entry (cobra or stdlib flag + manual dispatch).
- `internal/config` — load/parse YAML presets, defaults.
- `internal/tmux` — exec wrapper around tmux CLI:
  - `has-tmux` / version check (>= 3.x)
  - `new-session -d -s <name> -c <cwd>`
  - `split-window` loop to reach N panes
  - `select-layout <layout>`
  - `send-keys -t <pane> "<agent cmd>" Enter`
  - `attach-session -t <name>`, `switch-client` if already inside tmux, `ls`, `kill-session`
- `internal/ui` — Bubble Tea launcher (agent picker, count stepper, layout select) when invoked with no/incomplete args, plus `ls` interactive view.

## tmux Orchestration Details
- Require tmux installed; error clearly if missing or agent binary not on PATH.
- Session naming: `--session` or auto-generated; prefix `mt-` for `ls` filtering.
- Pane creation: iterative `split-window -t <session> -c <cwd>` until pane count == N, then `select-layout tiled` (or requested layout).
- Launch: `send-keys` per pane; stagger slightly if needed.
- Attach behavior:
  - Outside tmux: `tmux attach-session -t <session>`
  - Inside tmux (`$TMUX` set): `tmux switch-client -t <session>` to avoid nesting.
- Detach (`prefix + d`) leaves session alive by default; `kill` explicitly destroys.

## Edge Cases
- `count=1`: still use tmux session (consistent `ls`/`kill`) unless `--no-tmux` added later.
- Large N exceeding screen: tmux handles; document practical limit; consider warning above 6–8.
- Missing tmux/agent: fail fast with install hint.
- All panes share one cwd in v1. Per-pane worktrees deferred to v2.

## Milestones
1. **M1 — Core:** config loader + tmux wrapper + `mt <agent> [count] --layout --session --cwd --no-attach` + attach.
2. **M2 — Manager:** `ls` / `attach` / `kill`, Bubble Tea launcher, inside-tmux handling.
3. **M3 — Polish:** Homebrew/tarball distribution, version check, help overlay, warning on large N.

## Open / To Confirm
1. Binary name (`mt` assumed).
2. Positional `count` final (vs `--count` flag).
3. Default `tiled` + shared cwd acceptable for v1.
4. `count=1` via tmux OK.
5. V2 candidates: per-pane git worktrees, saved layouts, synchronize-panes toggle.
