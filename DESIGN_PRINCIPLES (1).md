# Design Principles

These principles govern every decision in Solo's design. When two approaches seem equally valid, these principles break the tie.

---

## 1. Local First

Solo works entirely on the local machine.

**What this means:**
- No network calls, ever
- No cloud backend
- No accounts or authentication
- No telemetry or usage reporting
- Works offline, on planes, in secure environments

**Why:**
AI-assisted development happens at the developer's machine. The coordination layer should live there too. Adding a network dependency adds latency, availability risk, and privacy concerns that are simply unnecessary for a single-developer tool.

---

## 2. No Daemon

Solo has no background process.

**What this means:**
- Nothing to start or stop
- Nothing listening on a port
- Nothing that can crash while you're working
- Nothing that needs to be added to startup scripts

Every CLI invocation opens the database, does its work, and exits. The zombie recovery scan runs at the start of each invocation, so the system stays consistent without a watchdog.

**Why:**
Daemons fail in complicated ways — stale sockets, permission issues, version mismatches between daemon and CLI, crashes that go unnoticed. By eliminating the daemon entirely, Solo eliminates this entire category of operational failure.

---

## 3. Deterministic Behavior

The same inputs always produce the same outputs.

**What this means:**
- No random behavior
- No ambient state that affects results (beyond the database)
- Concurrent operations are safe and produce consistent final state
- Error messages are stable and machine-readable

**Why:**
Solo is used by agents, not just humans. Agents need to be able to predict what a command will do, parse the output reliably, and handle errors programmatically. Non-determinism breaks automation.

---

## 4. Work Is Never Lost

Solo never deletes work automatically.

**What this means:**
- Crashed sessions are recovered, not discarded
- Completed worktrees are preserved until explicitly cleaned up
- All mutations are recorded in the audit log permanently
- Soft deletes only — no physical row deletion

**Why:**
AI agents produce real work product — code, analysis, plans. That work is valuable even when a session ends unexpectedly. Solo's job is to preserve state, not to make housekeeping decisions on behalf of the developer.

---

## 5. Agent Agnostic

Solo works with any agent that can run shell commands.

**What this means:**
- No agent-specific SDKs or plugins required
- CLI + JSON is the only interface
- Agent identity is a free-form string, not an enum
- No assumptions about agent capabilities or behavior

**Why:**
The AI tooling landscape changes rapidly. An interface that depends on specific agent implementations becomes obsolete quickly. A CLI + JSON interface works today and will work with tools that don't exist yet.

---

## 6. JSON First

Every command produces structured, stable JSON output.

**What this means:**
- `--json` flag on every command
- Output schema is versioned and stable
- Error responses are also JSON, with machine-readable codes
- Human-readable output is secondary to machine-readable output

**Why:**
Solo's primary consumers are agents, not humans. Agents parse JSON, not prose. Making JSON the primary output format — rather than an afterthought — means agents can rely on it.

---

## 7. Explicit Over Implicit

Solo does what you tell it to. It does not make assumptions.

**What this means:**
- No auto-magic task assignment
- No implicit session creation
- No background cleanup without a trigger
- Worktrees are not deleted until explicitly requested

**Why:**
Implicit behavior creates confusion in automated workflows. An agent that calls `solo task list` should be confident that nothing else happened as a side effect. Explicitness makes Solo's behavior auditable and predictable.

---

## 8. Ledger, Not Orchestrator

Solo records what happened. It does not decide what should happen next.

**What this means:**
- Solo never starts agents
- Solo never assigns tasks to agents
- Solo never makes decisions about scheduling or prioritization
- Solo does not have opinions about which agent should do what

**Why:**
Orchestration logic belongs in the layer above Solo — in the developer's workflow, in an agent's planning phase, or in a higher-level tool. Solo's job is to provide a reliable state store that orchestration can be built on top of. If Solo tried to do orchestration, it would need to encode assumptions about agent capabilities, task complexity, and developer intent — all of which are outside its scope.

---

## 9. Fail Loudly

Errors are always visible and always structured.

**What this means:**
- Failed operations exit with non-zero status
- Every error includes a machine-readable code (e.g. `RESERVATION_CONFLICT`)
- Every error includes a human-readable message
- Partial success is not silently swallowed

**Why:**
Silent failures in coordination systems cause exactly the problems Solo is designed to prevent — duplicated work, lost context, corrupted state. An agent that receives a clear `RESERVATION_CONFLICT` error can retry or escalate. An agent that receives a silent success when the operation actually failed cannot.

---

## 10. Minimal Dependencies

Solo's binary should be self-contained and dependency-light.

**What this means:**
- Go standard library preferred
- SQLite via a pure-Go or CGo driver (single dependency)
- No external services or runtime dependencies
- Single binary, no installation beyond copying the file

**Why:**
Developer tooling that requires complex installation or maintenance gets abandoned. A single binary that works out of the box gets used.
