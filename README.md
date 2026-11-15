# fx Desktop

[简体中文](README.zh-CN.md)

Community desktop client for [fx](https://fx.sh/) — a native [MyGo](https://github.com/egoist/mygo) app that connects to the **fx** agent over ACP. Conversations, tool output and permissions are rendered natively; noninteractive fx commands run behind native forms, and only interactive authentication (`fx login`, `fx login vercel --api-key` / setup, `teams`) uses the in-window console.

This is not an official Vercel Labs release.

**Platforms:** macOS 13+ and Linux, matching the fx CLI.

## Features

- **Native conversation UI** — streaming Markdown replies, expandable tool results, copy controls, cancellation and in-place model switching
- **ACP sessions and history** — browse and search saved fx sessions, then resume them in a project window; tools and history stay owned by fx
- **Composer** — reference workspace files with `@` and attach images when the connected agent supports them
- **Inline permissions** — requests wait for an explicit choice; operational notices stay outside the conversation
- **Quick Ask** — a native form for `fx ask` (model, effort, access, system prompt, images, resume, provider order, context limits, extra directories)
- **Workspace tools** — native Account, MCP, Folders, Details, Sessions, Launch, Models, Rules, Skills, Slack and GitHub pages
- **Live sessions** — list running fx sessions and their subagents with unique refs; an omitted count is shown as unknown, and entries that cannot be read are reported instead of silently dropped
- **Honest diffs** — timeline diffs mark truncated changes as `partial` and uncompared previews as `preview`, so an exact +/− count is never invented
- **Model and launch controls** — provider order, effort, fast/Ultra, context limits, additional directories and a live model picker
- **Allowlist editor** — add, remove and reset user or workspace fx permission rules in the fx state directory's `settings.json`
- **Files tab** — browse the workspace in the right rail, expand folders in place, open with the default application, or right-click a file to open it in the text editor
- **Changes panel** — live `git status` / `git diff` tree and a quick commit box
- **Everyday shell** — command palette, recent projects, open-folder, restore-last-project, reveal-in-Finder
- **fx management** — install or upgrade fx, run `fx doctor`, open the fx state directory
- **Preferences** — default project folder, theme, terminal font, tray and update behavior
- **System integration** — menu-bar tray, close-to-tray, single instance and `fx-desktop://` deep links
- **Themes** — System, Light and Dark, previewed live and applied everywhere when saved

## Requirements

- Go 1.27.3+
- MyGo v0.7.1, pinned in `go.mod`; verify with `go tool mygo doctor`
- Linux: GTK 3 and WebKitGTK 4.1 for the MyGo runtime
- Optional: an existing [fx](https://fx.sh/docs/getting-started/installation) install, or use the in-app installer

## Development

```bash
go tool mygo dev
```

Open a project directly:

```bash
go tool mygo dev /path/to/project
```

Use another fx binary:

```bash
FX_DESKTOP_FX_PATH=/path/to/fx go tool mygo dev
```

## Build

```bash
go tool mygo build
```

The packaged app and installer land in `outputs/` (gitignored). For a local build without the DMG:

```bash
go tool mygo build -skip-dmg
```

## Testing

```bash
go test ./...
```

Tests isolate `HOME` and `PATH` through `internal/testutil`; installer tests use `httptest` and never call GitHub.

## Reliability and limits

- File-tree refreshes are serialized per window and cancelled when the window closes; profile and skill scans are bounded to fixed byte and line limits, cut previews at a valid UTF-8 boundary, and stop when the user refreshes or closes.
- An invalid `FX_HOME` (relative path, filesystem root, home folder, or an existing file) is reported instead of silently used; a folder that does not exist yet is accepted for creation.
- Profile scanning resolves symlinks: links inside the state directory are followed, links that escape it are rejected, and FIFOs or other special files are skipped with a visible note.
- `fx doctor` accepts its exit-1 "findings" report; stdout and stderr stay separate, and truncated command output is an explicit error rather than a silently shortened result.
- Quitting waits for the final usage-stats flush, and repeated quit requests cannot bypass waiting for it.

## Project layout

```text
main.go
internal/app/          application coordination, windows and sessions
internal/app/chrome/   native UI built with MyGo
internal/app/presenter/ presentation adapters
internal/acp/          ACP JSON-RPC client and process lifecycle
internal/conversation/ conversation models and text
internal/history/      saved-session history
internal/management/   fx management operations
internal/workspace/    workspace files, mentions and changes
internal/fxcli/        fx command execution
internal/fxbin/        fx binary install and upgrade
internal/fxhome/       fx state-directory root validation
internal/fxlive/       live-session and subagent JSON
internal/fxprofile/    agent profile and prompt scanning
internal/fxsessions/   saved-session inspection and removal
internal/projects/     recent projects and persistence
internal/prefs/        application preferences
internal/configdir/    platform config paths
internal/sessionstats/ per-conversation usage records
resources/icon.png     app icon used by the MyGo packager
```

## Deep link

```text
fx-desktop://open?path=/Users/you/myproject
```

## License

[MIT](LICENSE) © 2026 Lu Weiqing
