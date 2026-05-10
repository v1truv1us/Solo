# Solo pi-extension work

- [x] Add safe auto-init support so the extension can bootstrap Solo in a git repo.
- [x] Upgrade the Solo widget to a checkbox-style task view that marks completed tasks.
- [x] Convert `pi-extension/` into a publishable pi package with package metadata and docs.
- [x] Switch project-local loading to package-based `.pi/settings.json` wiring.
- [x] Run focused package verification.
- [x] Make the Solo widget collapse completed tasks by default, hide stale checkmarks after the recent window, and add a toggle shortcut.

## Results

- Added silent auto-init during widget refresh plus explicit auto-init before Solo tool/command flows.
- Replaced the one-line Solo widget summary with checkbox-style task rows that visibly mark completed tasks.
- Made `pi-extension/` publishable by removing `private`, tightening package contents, and adding `pi-extension/README.md`.
- Replaced the temporary `.pi/extensions/...` shim with `.pi/settings.json` pointing at `./pi-extension`.
- Added npm-ready metadata (`repository`, `homepage`, `bugs`) plus a `publish:check` script.
- Added `pi-extension/PUBLISHING.md` with first-publish and update commands.
- Verified with `npm --prefix pi-extension run publish:check`.
- Updated the Solo widget so stale completed tasks disappear from the collapsed view, while Ctrl+Shift+S toggles a full expanded view and the widget auto-refreshes when recent completions age out.
- Verified with `npm --prefix pi-extension run typecheck`, `bun test pi-extension/src/format.test.ts`, and `npm --prefix pi-extension run pack:check`.
