# Skill Manager

## Commands

This is a Go 1.27 MyGo desktop application. Use the module-provided MyGo tool:

```sh
# Full test suite and standard Go checks
go test ./...
go vet ./...

# One test or one package
go test ./internal/skill -run '^TestScanRelationsAndRepos$'
go test ./internal/app -run '^TestOverviewScanRelinkAndUndo$'

# MyGo-specific UI lifetime checks
go tool mygo vet .

# Run or package the macOS app
go tool mygo dev
go tool mygo build
```

Format changed Go files with `gofmt` before testing.

## Architecture

- `main.go` constructs `app.App` with the user's home directory, creates the MyGo window, and calls `App.Attach` after the window exists.
- `internal/skill` is the read-only catalog pipeline. `Discover` checks fixed known agent paths beneath home; `Scan` reads immediate children of roots and configured repository directories, with `.system` as an explicit read-only exception; `analyze` classifies each entry against the shared pool and produces summaries, actionable issues, and a per-root dependency graph (`Snapshot.Graphs`). The filesystem is the source of truth.
- List scans read only bounded `SKILL.md` front matter and explicit `requires`/`dependencies`/`depends_on` fields. Full `SKILL.md` bodies load on detail selection; body mention inference runs only when opening the dependency graph. Nested skills and unknown agent folders are outside the scan boundary.
- `internal/manage` is the only layer that mutates skill directories or Claude invocation preferences. It replaces or retargets symlinks, moves removed directories into an undo record, and restores operations only when the target still matches the recorded state. Invocation preferences use `~/.claude/settings.json` `skillOverrides`; they never edit shared `SKILL.md` files, and undo restores only the changed preference key.
- `internal/repair` plans and applies fixes through `manage`. `Fix.Safe` marks fixes that lose nothing and may run in a batch; fixes that replace a copy with different content are never batched silently. The UI's per-entry buttons and one-click repair both come from `repair.For`/`repair.Plan`, so add new fix kinds there.
- `internal/store` persists display settings (`settings.json`: sidebar items hidden from the 设置 dialog, left-hand categories 浏览/Agent/自带; open with ⌘,), repository scan paths and optional display names at `~/.config/skill-manager/repos.json` and display-only catalog data at `~/.cache/skill-manager/catalog.json`. Repository management in `internal/app/repos.go` changes these records only, never repository files; path changes invalidate scans of the old scope, while name-only changes do not rescan. The catalog stores roots, entries, scan warnings, time, and scope; derived summaries and graphs are rebuilt without reading skills. A reopened app shows it before a background scan, with mutations disabled until fresh data arrives.
- `skill.BuildEffective` (`effective.go`) answers what one agent loads in one working directory: layers project, global, shared pool, built-in, first layer wins. The order is a design assumption, not each agent's documented behavior; verify before trusting it for an agent other than Claude. It reads only the project folder (`ProjectRoot` + `ScanDir`) on top of the finished snapshot. `internal/app/effective.go` draws it as the 生效视图 page (list/detail and matrix).
- `internal/app` owns UI state and coordinates scanning, confirmation dialogs, actions, and undo. `app.go` owns state and side effects; `view.go` renders pages (the home page is 问题; there is no overview page); `graphview.go` draws the dependency graph; `icons.go` holds the SVG glyphs and each agent's glyph and color (`lookOf`, keyed by the agent's dot folder under home).

## Repository conventions

- A skill is a direct child directory containing `SKILL.md`. Scan entries by `Lstat` so real directories, valid symlinks, and broken symlinks remain distinguishable. Metadata comes from bounded `SKILL.md` front matter. List scans never hash bodies or attachments. `TreeHash` is used only for an explicitly requested comparison of two copies.
- Keep the shared pool at `~/.agents/skills` authoritative. Agent entries are classified as linked, unverified copies, local, external links, broken links, or empty directories. Same/stale copy status and replacement require an explicit full comparison, followed by a fresh pair check on apply. Built-in and `.system` skills are display-only and must not be modified.
- Preserve shallow boundaries: home discovery enumerates known paths only; repository scans read the directory itself or immediate skill children, cap results at 500, and do not recurse. A missing-frontmatter document may have no description until its detail is loaded.
- All filesystem mutations must go through `internal/manage`. They must operate on a direct child of the applicable skills root, use relative symlinks, persist an undo `Record` under `~/.config/skill-manager/undo`, and validate the on-disk state before restoration. Do not delete skill directories permanently. Normal deletion uses `repair.RemovalFor` and fresh revalidation through `repair.ApplyRemoval`, including the existing repair batch path. Block removal when explicit same-root dependencies refer to it. Directly scanned agent links that traverse a shared-pool entry block the plain removal, but `repair.Removal.Unlinkable` lets the UI offer a choice through `repair.ApplyRemovalLinks`: remove those links first as part of one undoable step (rolled back if a later step fails) or keep them as broken links; the batch repair path never takes that choice; metadata revalidation checks root/entry/link/document stamps, not all resource contents; a linked agent entry removes only its link. Claude invocation writes are limited to the user settings file and must preserve unrelated settings.
- Start scans through `App.startScan`; it uses a generation counter to discard stale asynchronous results and routes UI changes through `App.post`/`Window.Update`. Keep direct construction testable: `App.New` may load the display-only startup cache but does not scan. Derived view caches expire with the snapshot; page/query/filter invalidate rows. Invocation-only changes and undo refresh only the small Claude settings file, not the skill tree. Native confirmation work and undo run in the background; UI state changes return through `App.post`, and scan requests during a mutation are coalesced. Tests run without a window using injectable directory-picker, reveal, and clipboard functions.
- Keep user-facing UI text in Chinese to match the existing interface. New destructive or replacement actions require the existing confirmation-dialog flow and should return `manage.Record` values so the toast-based undo path remains available.
- Tests use temporary homes and real filesystem fixtures (`SKILL.md` directories and symlinks). Use `ui.NewTester` for app interactions; do not make standard tests depend on the real home directory. `TestLiveHome` is opt-in via `SKILL_MANAGER_LIVE=1`. `tt.Image()` returns the rendered frame, so a throwaway test can `png.Encode` it to check layout visually.
- In `ui.Tester`, elements are found by their text, so an icon-bearing `ui.Button(c, "").Label(x)` must contain a `ui.Text` child equal to `x`.
