# Roadmap

---

## v1.0 — Local Coordination Layer

**Status: In development**

The core product. Everything documented in this repository.

### Goals
- Stable CLI surface with JSON output contract
- SQLite-backed task, session, reservation, handoff, worktree management
- Automatic crash recovery via zombie scanner
- Git worktree isolation
- Context bundle assembly
- Full audit log
- Dependency-aware task readiness

### Definition of Done
- All CLI commands implemented and tested
- Concurrent agent safety verified under load
- Full documentation set complete
- Zero-dependency single binary

---

## v1.1 — Polish and Observability

**Status: Planned**

Quality-of-life improvements after initial v1 usage.

### Candidates
- `solo audit log` — queryable audit event viewer
- `solo task history T-142` — full narrative view of what happened to a task
- Improved human-readable output (non-JSON mode)
- Shell completions (bash, zsh, fish)
- `solo doctor` — diagnose common issues
- Configurable zombie recovery TTL (in addition to PID-based detection)

---

## v1.2 — Worktree Improvements

**Status: Planned**

Better git integration.

### Candidates
- `solo worktree merge T-142` — guided merge flow from worktree to main branch
- Automatic stash of uncommitted changes before worktree cleanup
- Worktree status summary (staged files, uncommitted changes)
- Support for worktrees on existing branches (not just new branches)

---

## Future Possibilities

These are ideas that may be worth exploring post-v1, but are not committed or planned.

### Multi-machine Coordination

Allow multiple developers or machines to share a Solo task database.

**Challenges:** Requires conflict resolution, network transport, and a decision about whether to use a central server or peer-to-peer sync. This fundamentally changes Solo's "local only" character and would likely be a separate product or major version.

### Remote Backup

Periodically back up `.solo/solo.db` to a remote store (S3, Git repository, etc.).

**Challenges:** Requires credentials, network access, and a decision about what data is sensitive.

### Agent Scheduling

Solo could suggest which available task an agent should pick up next, based on priority, dependencies, and agent capabilities.

**Challenges:** Requires encoding assumptions about agent capabilities. This risks making Solo opinionated about orchestration, which conflicts with the "ledger, not orchestrator" principle.

### Visual Dashboard

✅ **Delivered (read-only milestone):** `solo dashboard --addr :8081` now provides a lightweight local web UI, deterministic JSON snapshot endpoint (`/api/dashboard`), and Prometheus-compatible metrics (`/metrics`) for Grafana.

**Follow-up candidates:** richer filtering, dependency graph view, and optional Infinity datasource JSON endpoint variants.

### Plugin System

An extension mechanism for custom task types, custom context bundle fields, or integration with external systems.

**Challenges:** Plugin systems are hard to design well and create long-term maintenance burden. Would require significant demand to justify.

---

## What Will Not Be Added

These are explicit non-goals. They will not be added to Solo regardless of demand, because they conflict with the core design principles.

| Feature | Reason |
|---|---|
| Cloud backend | Violates "local first" principle |
| Authentication / access control | Single-user tool; auth adds complexity without value |
| Agent auto-scheduling | Violates "ledger, not orchestrator" principle |
| Built-in agent integrations (Claude SDK, etc.) | Violates "agent agnostic" principle |
| Network daemon | Violates "no daemon" principle |
| Non-local state store (Postgres, etc.) | Violates "local first" and "minimal dependencies" |

If you need these features, Solo is intentionally designed to be a building block. Build the layer above it.

---

## Contributing to the Roadmap

If you have a feature request or believe something should be prioritized differently, open a GitHub issue. Include:

- The problem you're trying to solve
- Why Solo is the right place to solve it
- Whether it can be implemented without violating the design principles
