# fx Desktop

Community desktop client for [fx](https://fx.sh/) — a native [MyGo](https://github.com/egoist/mygo) app that connects to the **fx** agent over ACP. Conversations, tool output and permissions are rendered natively; noninteractive fx commands run behind native forms, and only interactive authentication (`fx login`, `setup`, `teams`) uses the in-window console.

This is not an official Vercel Labs release.

**Platforms:** macOS and Linux, matching the fx CLI.

## Features

- **Native conversation UI** — streaming Markdown replies, expandable tool results, copy controls, cancellation and in-place model switching
- **Real ACP sessions** — saved sessions from fx, resumed in the current project window; tools and history stay owned by fx
- **Inline permissions** — requests wait for an explicit choice; operational notices stay outside the conversation
- **Quick Ask** — a native form for `fx ask` (model, effort, access, system prompt, images, resume, provider order, context limits, extra directories)
- **Workspace tools** — native Account, MCP, Folders, Details, Sessions, Launch, Models, Rules, Skills, Slack and GitHub pages
- **Model and launch controls** — provider order, effort, fast/Ultra, context limits, additional directories and a live model picker
- **Allowlist editor** — add, remove and reset fx permission rules in `~/.fx/settings.json`
- **Files tab** — browse the workspace in the right rail, expand folders in place, open with the default application, or right-click a file to open it in the text editor
- **Changes panel** — live `git status` / `git diff` tree and a quick commit box
- **Everyday shell** — command palette, recent projects, open-folder, restore-last-project, reveal-in-Finder
- **fx management** — install or upgrade fx, run `fx doctor`, open `~/.fx`
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

## Project layout

```text
main.go
internal/app/          coordinator, sessions, actions, fx integration
internal/app/chrome/   native UI built with MyGo
internal/acp/          ACP JSON-RPC client
internal/fxbin/        fx binary install and upgrade
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

MIT
