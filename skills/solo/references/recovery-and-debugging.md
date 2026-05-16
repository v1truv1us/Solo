# Recovery and debugging

Fix stale state and inspect what broke.

## Trust

Solo recovers some failures automatically.

At the start of every CLI invocation, it scans for dead session pids and expired reservations. Recovered tasks move back to `ready`.

## Check

Start with fast read-only commands.

```bash
solo health --json
solo task show T-12 --json
solo session list --task T-12 --json
solo handoff list --task T-12 --json
solo worktree inspect T-12 --json
solo audit list --task T-12 --json
```

These commands usually tell you whether the problem is a lock, a bad worktree, or stale task state.

## Recover

Run the global recovery pass first.

```bash
solo recover --all --json
```

Use this when a process crashed, a reservation expired, or a task looks stuck after an agent died.

## Reset

Recover one task directly when it is still blocked.

```bash
solo task show T-12 --json
solo task recover T-12 --version <task.version> --json
```

`task recover` requires the latest task version and resets eligible tasks back to `ready`.

## Clean

Remove inactive worktrees after recovery.

```bash
solo worktree list --json
solo worktree cleanup --json
solo worktree cleanup T-12 --json
solo worktree cleanup --force --json
```

Dirty inactive worktrees are skipped unless you pass `--force`.

## Inspect

Use worktree inspection for git-side problems.

```bash
solo worktree inspect T-12 --json
```

The response includes `git_status`, `uncommitted_files`, `ahead_commits`, and `behind_commits`.

## Handle

Use the error code to choose the next move.

- `TASK_LOCKED`: another live reservation exists, so wait or recover.
- `TASK_NOT_READY`: the task is still `draft`, `blocked`, `completed`, or `failed`.
- `HANDOFF_LOCKED`: the latest pending handoff targets a different worker.
- `VERSION_CONFLICT`: re-read the task and retry with the fresh version.
- `WORKTREE_ERROR`: inspect git state, base ref state, and worktree cleanup needs.
- `SQLITE_BUSY`: retry after a short delay.
- `NOT_A_REPO`: run inside a Git repo with `.git`.

## Debug

Use these patterns for common cases.

Stuck lock after a crash:

```bash
solo recover --all --json
solo task list --available --json
```

Task still active after manual interruption:

```bash
solo task show T-12 --json
solo task recover T-12 --version <task.version> --json
```

Missing or dirty worktree:

```bash
solo worktree inspect T-12 --json
solo session list --task T-12 --json
solo worktree cleanup T-12 --json
```

Unexpected task history:

```bash
solo audit list --task T-12 --json
solo audit show <event-id> --json
```

Remember that `solo audit show` needs a numeric id.

## Remember

Recovery does not edit your code for you.

Uncommitted files inside a recovered worktree can still exist on disk. Inspect the worktree before deleting it if you may need those changes.
