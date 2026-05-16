# Commands

Quick reference for the current CLI.

## Initialize

Set up Solo in the current Git repo.

```bash
solo init --json
solo init --machine-id <name> --json
solo init --install-skill --json
solo init --install-skill --skill-scope environment --json
solo init --install-skill --skill-scope agent --agent claude-code --json
```

`machine_id` is used in generated branch names like `solo/<machine-id>/<task-id>`.

## Create

Add a task to the ledger.

```bash
solo task create --title "Fix retry backoff" --json
solo task create \
  --title "Fix retry backoff" \
  --type bug \
  --priority high \
  --description "429 responses skip backoff" \
  --acceptance-criteria "Backoff applies to 429 and 5xx" \
  --definition-of-done "Tests pass" \
  --labels http,retries \
  --affected-files internal/http/retry.go \
  --deps T-3,T-4 \
  --json
```

Important flags:

- `--title` required
- `--type` defaults to `task`
- `--priority` accepts `low|medium|high|critical` or `1-5`
- `--parent` links a child task
- `--deps` adds dependency ids

New tasks start as `draft`.

## Ready

Move a draft or blocked task into the claimable queue.

```bash
solo task ready T-12 --version <task.version> --json
```

Use the latest `version` from `solo task show` or another fresh task payload.

## Update

Edit task metadata.

```bash
solo task update T-12 --description "429 responses skip backoff" --version <task.version> --json
solo task update T-12 --priority critical --labels http,retries --version <task.version> --json
```

Current `task update` fields:

- `--title`
- `--description`
- `--priority`
- `--parent`
- `--labels`
- `--affected-files`
- `--version` required

There is no generic CLI flag here for changing task status.

## List

Inspect tasks with lightweight filters.

```bash
solo task list --json
solo task list --available --json
solo task list --status ready --json
solo task list --label http --limit 10 --offset 0 --json
solo task show T-12 --json
```

Useful reads:

```bash
solo task deps T-12 --json
solo task tree T-12 --json
solo task context T-12 --json
solo task context T-12 --max-tokens 4000 --json
```

## Start

Reserve a ready task and create a worktree.

```bash
solo session start T-12 --worker claude-code --json
solo session start T-12 --worker claude-code --ttl 7200 --json
solo session start T-12 --worker claude-code --pid 12345 --json
```

This returns `session_id`, `reservation_id`, `worktree_path`, `branch`, `expires_at`, and a `context` bundle.

## End

Close an active session.

```bash
solo session end T-12 --result completed --json
solo session end T-12 --result failed --notes "Build is broken on arm64" --json
solo session end T-12 \
  --result completed \
  --notes "Backoff added" \
  --files internal/http/retry.go,internal/http/retry_test.go \
  --commits abc123,def456 \
  --json
```

Current `--result` values:

- `completed`
- `failed`
- `interrupted`
- `abandoned`

Optional `--status` is also accepted on completed sessions if you need a different final task status.

## Renew

Extend the active reservation for the current task holder.

```bash
solo reservation renew T-12 --json
```

The command checks ownership against the active session pid.

## Transfer

Hand work to another agent.

```bash
solo handoff create T-12 \
  --summary "Backoff logic added" \
  --remaining-work "Fix integration test fixture" \
  --to aider \
  --files internal/http/retry.go,internal/http/retry_test.go \
  --json
```

Useful reads:

```bash
solo handoff list --json
solo handoff list --task T-12 --json
solo handoff list --task T-12 --status pending --json
solo handoff show <handoff-uuid> --json
```

`--summary` is required. `--remaining-work` is optional in the CLI but should usually be present.

## Inspect

Look at worktree state and overall health.

```bash
solo worktree list --json
solo worktree inspect T-12 --json
solo health --json
solo audit list --task T-12 --json
solo audit list --task T-12 --limit 20 --offset 0 --json
solo audit show 42 --json
solo search "retry backoff" --json
solo search "retry backoff" --status ready --limit 5 --json
```

`solo audit show` takes a numeric event id.

## Clean

Remove inactive worktrees.

```bash
solo worktree cleanup --json
solo worktree cleanup T-12 --json
solo worktree cleanup --force --json
```

Active worktrees are skipped. Dirty inactive worktrees are skipped unless `--force` is set.

## Recover

Repair stale or abandoned state.

```bash
solo recover --all --json
solo task recover T-12 --version <task.version> --json
solo session list --task T-12 --json
solo session list --active --json
```

`solo recover --all` scans active sessions for dead pids and expired reservations.

## Serve

Run the optional read-only dashboard.

```bash
solo dashboard --addr :8081
```

Useful endpoints:

- `/`
- `/api/dashboard`
- `/metrics`

## Handle

Watch for these common errors.

- `TASK_NOT_READY`: task is not `ready`
- `TASK_LOCKED`: another active reservation exists
- `HANDOFF_LOCKED`: latest pending handoff targets a different worker
- `VERSION_CONFLICT`: refresh the task and retry with a new version
- `WORKTREE_ERROR`: inspect git state and worktree state
- `SQLITE_BUSY`: retry shortly
- `NOT_A_REPO`: run inside a Git repo
