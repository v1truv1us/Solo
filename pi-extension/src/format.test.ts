import assert from "node:assert/strict";
import { afterEach, describe, test } from "node:test";
import { mkdtemp, mkdir, rm } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";

import { formatSoloCliError, formatWidgetLines, getWidgetRefreshAt, resolveSoloExecutionCwd, shouldAutoInit } from "./format.js";

const tempPaths: string[] = [];

async function makeTempDir(): Promise<string> {
  const dir = await mkdtemp(join(tmpdir(), "pi-solo-format-test-"));
  tempPaths.push(dir);
  return dir;
}

afterEach(async () => {
  while (tempPaths.length > 0) {
    const dir = tempPaths.pop();
    if (dir) await rm(dir, { recursive: true, force: true });
  }
});

describe("resolveSoloExecutionCwd", () => {
  test("returns nearest git repo root for nested directories", async () => {
    const root = await makeTempDir();
    await mkdir(join(root, ".git"));
    const nested = join(root, "packages", "pi-extension", "src");
    await mkdir(nested, { recursive: true });

    const result = await resolveSoloExecutionCwd(nested);

    assert.equal(result.cwd, root);
    assert.equal(result.repoRoot, root);
    assert.equal(result.hasGitRepo, true);
  });

  test("falls back to original cwd when not inside a git repo", async () => {
    const cwd = await makeTempDir();

    const result = await resolveSoloExecutionCwd(cwd);

    assert.equal(result.cwd, cwd);
    assert.equal(result.repoRoot, undefined);
    assert.equal(result.hasGitRepo, false);
  });
});

describe("shouldAutoInit", () => {
  test("returns true only for git repos without a Solo database", () => {
    assert.equal(shouldAutoInit({ cwd: "/repo", repoRoot: "/repo", hasGitRepo: true, hasSoloDb: false }), true);
    assert.equal(shouldAutoInit({ cwd: "/repo", repoRoot: "/repo", hasGitRepo: true, hasSoloDb: true }), false);
    assert.equal(shouldAutoInit({ cwd: "/tmp", hasGitRepo: false, hasSoloDb: false }), false);
  });
});

describe("formatWidgetLines", () => {
  const recentWindowMs = 10 * 60 * 1000;
  const now = Date.parse("2026-05-10T14:00:00.000Z");

  test("renders completed tasks when expanded", () => {
    const lines = formatWidgetLines([
      { id: "T-3", title: "In progress", status: "active" },
      { id: "T-2", title: "Ready to pick up", status: "ready" },
      { id: "T-1", title: "Already done", status: "completed" },
    ], { issues: ["db_integrity_failed"] }, { expanded: true, now });

    assert.deepEqual(lines, [
      "▾ Solo: 1 active | 1 completed | 1 ready ⚠ 1 issue(s)",
      "◐ T-3 In progress",
      "☑ T-1 Already done",
      "☐ T-2 Ready to pick up",
    ]);
  });

  test("collapses stale completed tasks but keeps recent completions visible", () => {
    const recentCompletedAt = new Date(now - 60_000).toISOString();
    const staleCompletedAt = new Date(now - recentWindowMs - 60_000).toISOString();

    const lines = formatWidgetLines([
      { id: "T-4", title: "Freshly done", status: "completed", updated_at: recentCompletedAt },
      { id: "T-3", title: "In progress", status: "active" },
      { id: "T-2", title: "Ready to pick up", status: "ready" },
      { id: "T-1", title: "Old finished", status: "completed", updated_at: staleCompletedAt },
    ], undefined, { now });

    assert.deepEqual(lines, [
      "▸ Solo: 1 active | 2 completed | 1 ready",
      "✅ T-4 Freshly done",
      "◐ T-3 In progress",
      "☐ T-2 Ready to pick up",
      "… 1 completed task hidden — F8 to expand",
    ]);
  });

  test("reports when a recent completion should refresh the widget", () => {
    const recentCompletedAt = now - 5 * 60 * 1000;
    const refreshAt = getWidgetRefreshAt([
      { id: "T-1", title: "Just finished", status: "completed", updated_at: new Date(recentCompletedAt).toISOString() },
      { id: "T-2", title: "Ready to go", status: "ready" },
      { id: "T-3", title: "Old finished", status: "completed", updated_at: new Date(now - recentWindowMs - 60_000).toISOString() },
    ], now);

    assert.equal(refreshAt, recentCompletedAt + recentWindowMs);
  });
});

describe("formatSoloCliError", () => {
  test("shows code and message from structured Solo JSON errors", () => {
    const text = formatSoloCliError(`{
  "error": {
    "code": "NOT_A_REPO",
    "message": "No .git found walking up from current directory."
  },
  "ok": false
}`);

    assert.match(text, /NOT_A_REPO/);
    assert.match(text, /No \.git found walking up from current directory\./);
  });

  test("falls back to trimmed raw output for non-JSON errors", () => {
    assert.equal(formatSoloCliError("plain failure\n\n"), "plain failure");
  });
});
