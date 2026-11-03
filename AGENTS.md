# Agent repository guidance

## Files and Git

- Keep source code, tests, `go.mod`, `go.sum`, app resources, and project documentation under version control.
- Do not commit local build output or caches (`/build/`, `/outputs/`, `/.mygo/`, `/.tmp/`), OS metadata, compiled test binaries, platform build objects, local agent state (`/.workbuddy/`), or the local working architecture notes (`/ARCHITECTURE.md`). See `.gitignore` for the exact rules.
- `ARCHITECTURE.md`, when present locally, records architecture and outstanding acceptance work. Read it when relevant, but do not commit it or link to it from published documentation.
- `/internal/outputs/` contains UI captures rather than application resources. New captures are ignored. Six existing redesign screenshots are already tracked: `.gitignore` does not untrack them. Preserve them unless the user explicitly requests their removal from Git; check references and intended use first.
- Do not delete ignored or untracked files merely because they are not intended for Git. Inspect `git status --short` and preserve unrelated work before staging or committing; review the staged diff and never force-add ignored files without a specific reason.
- Ignore rules are not a security boundary. Check staged files for secrets and personal data before publishing.
