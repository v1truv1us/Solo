# Solo v1.0 Release Plan

**Repo:** v1truv1us/Solo
**Current Version:** v0.1.1
**Target:** v1.0.0
**Date:** 2026-04-25

---

## Current State Assessment

Solo is a Go CLI (~3,600 LOC across 16 source files) that provides local SQLite-backed orchestration for coding agents. It builds cleanly, has 2 passing test files, GoReleaser + Homebrew tap configured, and CI/release pipelines in place. The project has thorough documentation (12+ markdown docs, a SPEC.pdf, Grafana dashboard).

**However**, there are 3 open issues — two of which are **blocker-level bugs** that make core workflows unreliable, and one spec-parity tracking issue with 3 sub-items. Test coverage is at 24.4% with the main CLI entrypoint at 0%. Several documented commands don't exist yet.

### Version History
- v0.1.0 — initial release
- v0.1.1 — (current) minor fixes

---

## What Works ✅

- **Build system** — `make build` compiles cleanly with FTS5 tags, CGO_ENABLED=0, cross-platform via GoReleaser
- **SQLite core** — WAL mode, FK pragmas, schema migrations, FTS5 search all functional
- **Task CRUD** — create, list, status transitions, priority, labels, dependencies, search
- **Session lifecycle** — start/end sessions with worker attribution (has PID bug — see below)
- **Handoffs** — structured agent-to-agent transfer with summary + remaining work
- **Git worktrees** — creation, listing, cleanup (has cleanup bug — see below)
- **Crash recovery** — zombie scanner reclaims dead sessions (overly aggressive — see below)
- **Dashboard** — read-only web UI + Prometheus `/metrics` endpoint + Grafana JSON export
- **Skill bundle** — `solo init --install-skill` drops agent guidance in `.solo/skills`
- **Context bundles** — structured state assembly for agent consumption
- **CI/CD** — GitHub Actions: vet + test + staticcheck on push/PR; GoReleaser on tag
- **Distribution** — GoReleaser builds Linux/macOS/Windows (amd64+arm64), Homebrew tap configured
- **Documentation** — extensive: PRD, architecture, data model, CLI reference, security model, testing strategy, agent integration guide, contributing guide

---

## What's Broken / Missing ❌

### Blocker Bugs (must fix for v1.0)

| # | Issue | Severity | Description |
|---|-------|----------|-------------|
| 1 | #7 — PID crash recovery loop | **Blocker** | `session start` stores CLI's own PID by default. Next Solo command's zombie scan sees PID as dead, auto-recovers session as `crash_detected`. Makes `session start` → `session end` flow unreliable for any non-long-lived caller. **Fix:** default `agent_pid` to NULL; require explicit `--pid` for crash recovery. |
| 2 | #8 — Cleaned worktree rows block restart | **Blocker** | `worktree cleanup` sets `status='cleaned'` but keeps the row. `session start` hits unique constraint on `worktrees.path` → `WORKTREE_EXISTS`. Retrying a task after cleanup is impossible without manual DB surgery. **Fix:** delete cleaned rows during cleanup, or upsert/reuse cleaned rows on session start. |

### Spec-Parity Gaps (#6 — tracked as 3 sub-items)

| # | Gap | Priority | Description |
|---|-----|----------|-------------|
| 3 | Response envelope | **High** | Docs specify `{ok, data}` / `{ok, error}` envelope. CLI returns raw maps on success, `{"error":...}` on failure. No consistent contract. |
| 4 | Status/priority semantics | **High** | Docs define `draft→ready→active→completed|failed|blocked` + `low|medium|high|critical`. Implementation uses `open|triaged|in_progress|in_review|done|cancelled` + numeric `1..5`. Must converge one way. |
| 5 | Missing commands | **Medium** | `solo task tree` (dependency tree view) and `solo audit list/show` are documented but unimplemented. CLI returns "unknown subcommand". |

### Quality Gaps

| Area | Issue | Priority |
|------|-------|----------|
| Test coverage | 24.4% overall, 0% on `cmd/solo`, 29.8% on `internal/solo` | **High** |
| Version badge | README shows `v0.1.0` but latest tag is `v0.1.1` | **Low** |
| SPEC.pdf | Unclear if PDF is kept in sync with markdown docs | **Low** |

---

## Feature Scope for v1.0

### Include (v1.0 scope)

- Fix both blocker bugs (#7, #8)
- Converge status/priority enums to documented contract (#6 sub-item 2)
- Implement response envelope (#6 sub-item 1)
- Implement `solo task tree` (#6 sub-item 3)
- Implement `solo audit list` / `solo audit show` (#6 sub-item 3)
- Test coverage ≥ 70% (ideally ≥ 80%)
- Update all docs to match final implementation
- Verify Homebrew tap works end-to-end
- Verify cross-platform builds (Linux/macOS/Windows, amd64/arm64)

### Defer to v1.1+

- Shell completions (bash, zsh, fish)
- `solo doctor` diagnostic command
- `solo task history` narrative view
- Configurable zombie recovery TTL
- `solo worktree merge` guided merge flow
- Improved human-readable (non-JSON) output formatting

### Explicit Non-Goals (per ROADMAP.md)

- Cloud backend, auth, agent scheduling, network daemon, non-local store

---

## Ordered Work Items

| # | Item | Estimate | Dependencies |
|---|------|----------|--------------|
| 1 | **Fix #7: Default agent_pid to NULL** — change `session start` to not store PID unless `--pid` provided; update zombie scanner to only check non-NULL PIDs | 2h | None |
| 2 | **Fix #8: Delete cleaned worktree rows** — change `worktree cleanup` to DELETE rows instead of updating status; update queries that excluded `cleaned` status | 1h | None |
| 3 | **Converge status/priority enums** — decide direction (recommend: update code to match docs — `draft/ready/active/completed/failed/blocked`, `low/medium/high/critical`). Update schema.go, tasks.go, sessions.go. Write migration path for existing DBs. | 4h | None |
| 4 | **Implement response envelope** — centralize output formatting: `{ok:true, data:...}` on success, `{ok:false, error:{code,message}}` on failure. Update all command handlers in cmd/solo/main.go. | 3h | None |
| 5 | **Implement `solo task tree`** — add subcommand that renders dependency tree. Support `--json` and human-readable modes. | 2h | Item 3 (status enums) |
| 6 | **Implement `solo audit list` / `solo audit show`** — add audit command group with filtering by task, time range, event type. JSON output. | 3h | Item 4 (envelope) |
| 7 | **Test coverage push** — add tests for cmd/solo (integration-level), expand internal/solo tests. Target ≥ 70%. | 8h | Items 1-6 |
| 8 | **Documentation pass** — update CLI_REFERENCE, DATA_MODEL, PRD, ARCHITECTURE, AGENT_INTEGRATION to match final implementation. Remove or archive SPEC.pdf if stale. Fix README version badge. | 3h | Items 1-6 |
| 9 | **End-to-end distribution verification** — tag v1.0.0-rc.1, verify GoReleaser builds all 6 targets, verify `brew install v1truv1us/tap/solo` works on macOS (both archs). Test install instructions from README. | 2h | Item 8 |

**Total estimate: ~28 hours**

---

## Release Checklist

- [x] All 3 open issues closed (#6, #7, #8)
- [x] Both blocker bugs verified fixed with manual testing
- [x] Response envelope consistent across all commands
- [x] Status/priority enums match docs
- [x] `solo task tree` implemented and tested
- [x] `solo audit list` / `solo audit show` implemented and tested
- [x] Test coverage ≥ 70% (current: 70.4%, 126 tests)
- [x] `go vet` passes clean
- [x] `staticcheck` pass clean
- [ ] All documentation updated and consistent
- [ ] README version badge updated
- [x] CHANGELOG.md created (or GoReleaser changelog verified)
- [x] Cross-platform build matrix verified (6 targets)
- [x] Homebrew tap formula updated and installable
- [x] Manual smoke test: `init → task create → session start → session end → worktree cleanup → restart`
- [ ] Manual smoke test: handoff flow (session start → handoff create → session start → session end)
- [ ] Tag v1.0.0-rc.1 → verify CI passes → tag v1.0.0
- [ ] GitHub Release notes written
- [ ] Announce (Discord, etc.)

---

## Distribution Plan

### Channels

| Channel | Status | Action for v1.0 |
|---------|--------|-----------------|
| GitHub Releases | ✅ Configured | GoReleaser auto-publishes on tag |
| Homebrew (`v1truv1us/tap/solo`) | ✅ Configured | GoReleaser auto-updates formula on tag |
| Direct download (curl) | ✅ Documented | No changes needed |

### Build Matrix

| OS | Arch | Status |
|----|------|--------|
| Linux | amd64 | ✅ |
| Linux | arm64 | ✅ |
| macOS | amd64 | ✅ |
| macOS | arm64 | ✅ |
| Windows | amd64 | ✅ |
| Windows | arm64 | ✅ |

### Pre-release Verification

1. Tag `v1.0.0-rc.1` on a test commit
2. Verify GoReleaser dry-run: `goreleaser release --snapshot --clean`
3. Verify all 6 binaries build and `solo --help` works on each
4. Verify Homebrew tap receives updated formula
5. Fix any issues, then tag `v1.0.0`

### Post-release

- Update README status section from "pre-release" to "v1.0.0 stable"
- Pin GitHub release as latest
- Verify `brew upgrade solo` works for existing users
