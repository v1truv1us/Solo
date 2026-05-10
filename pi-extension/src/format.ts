import { access } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";

import type { ExecResult, ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";

export interface SoloCliResult {
  ok: boolean;
  data: unknown;
  raw: string;
  errorCode?: string;
  cwd: string;
  repoRoot?: string;
  hasGitRepo: boolean;
  hasSoloDb: boolean;
}

export interface SoloExecutionCwd {
  cwd: string;
  repoRoot?: string;
  hasGitRepo: boolean;
  hasSoloDb: boolean;
}

export interface WidgetFormatOptions {
  expanded?: boolean;
  now?: number;
  maxVisible?: number;
}

const WIDGET_STATUS_ORDER = ["active", "completed", "ready", "draft", "blocked", "failed", "cancelled"] as const;

// How long a completed task stays visible in the collapsed widget view.
const RECENTLY_COMPLETED_MS = 10 * 60 * 1000; // 10 minutes

const WIDGET_STATUS_ICONS: Record<string, string> = {
  active: "◐",
  ready: "☐",
  draft: "☐",
  blocked: "⛔",
  failed: "⚠",
  completed: "☑",
  cancelled: "✕",
};

function truncate(str: string, max = 500): string {
  if (str.length <= max) return str;
  return str.slice(0, max) + `... (${str.length} bytes total)`;
}

async function pathExists(path: string): Promise<boolean> {
  try {
    await access(path);
    return true;
  } catch {
    return false;
  }
}

export async function resolveSoloExecutionCwd(startCwd: string): Promise<SoloExecutionCwd> {
  const originalCwd = resolve(startCwd);
  let current = originalCwd;

  for (;;) {
    const hasGitRepo = await pathExists(join(current, ".git"));
    if (hasGitRepo) {
      return {
        cwd: current,
        repoRoot: current,
        hasGitRepo: true,
        hasSoloDb: await pathExists(join(current, ".solo", "solo.db")),
      };
    }

    const parent = dirname(current);
    if (parent === current) {
      return {
        cwd: originalCwd,
        hasGitRepo: false,
        hasSoloDb: false,
      };
    }
    current = parent;
  }
}

function parseSoloJson(raw: string): { ok: boolean; data: unknown; errorCode?: string } {
  if (!raw.trim()) {
    return { ok: false, data: null };
  }
  try {
    const parsed = JSON.parse(raw);
    if (parsed?.ok === true) {
      return { ok: true, data: parsed.data };
    }
    if (parsed?.error) {
      return { ok: false, data: parsed.error, errorCode: parsed.error.code };
    }
    return { ok: true, data: parsed };
  } catch {
    return { ok: false, data: raw };
  }
}

export function formatSoloCliError(raw: string, fallback = "Unknown Solo error"): string {
  const text = raw.trim();
  if (!text) return fallback;

  try {
    const parsed = JSON.parse(text);
    const code = parsed?.error?.code;
    const message = parsed?.error?.message;
    if (code && message) return `${code}: ${message}`;
    if (message) return String(message);
  } catch {
    // Fall through to raw text formatting.
  }

  return truncate(text, 200);
}

export async function runSolo(
  pi: ExtensionAPI,
  ctx: ExtensionContext,
  args: string[],
  options?: { timeout?: number },
): Promise<{ result: SoloCliResult; exec: ExecResult }> {
  const execCwd = await resolveSoloExecutionCwd(ctx.cwd);
  const execResult = await pi.exec("solo", [...args, "--json"], {
    cwd: execCwd.cwd,
    signal: ctx.signal,
    timeout: options?.timeout ?? 15_000,
  });

  const raw = execResult.stdout.trim() || execResult.stderr.trim();
  const parsed = parseSoloJson(raw);

  return {
    result: {
      ...parsed,
      raw: truncate(raw),
      cwd: execCwd.cwd,
      repoRoot: execCwd.repoRoot,
      hasGitRepo: execCwd.hasGitRepo,
      hasSoloDb: execCwd.hasSoloDb,
    },
    exec: execResult,
  };
}

export function formatTaskOneLine(task: Record<string, unknown>): string {
  const id = task.id ?? "?";
  const status = task.status ?? "?";
  const priority = task.priority ?? "";
  const title = typeof task.title === "string" ? (task.title.length > 60 ? task.title.slice(0, 57) + "..." : task.title) : "?";
  const labels = Array.isArray(task.labels) && task.labels.length > 0 ? ` [${(task.labels as string[]).join(",")}]` : "";
  return `${id} ${status} ${priority}${labels} ${title}`;
}

export function formatTaskList(tasks: Record<string, unknown>[], total?: number): string {
  if (tasks.length === 0) return "No tasks found.";
  const header = total !== undefined && total > tasks.length ? `Showing ${tasks.length} of ${total} tasks:\n` : `${tasks.length} task(s):\n`;
  return header + tasks.map(formatTaskOneLine).join("\n");
}

export function formatTaskDetail(data: Record<string, unknown>): string {
  const task = (data.task ?? data) as Record<string, unknown>;
  const lines = [
    `Task:     ${task.id}`,
    `Title:    ${task.title}`,
    `Status:   ${task.status}`,
    `Priority: ${task.priority}`,
  ];
  if (task.description) lines.push(`Description:\n${task.description}`);
  if (Array.isArray(task.labels) && task.labels.length > 0) lines.push(`Labels:   ${(task.labels as string[]).join(", ")}`);
  if (task.parent_task) lines.push(`Parent:   ${task.parent_task}`);

  const sessionCount = data.session_count ?? task.session_count;
  if (sessionCount !== undefined) lines.push(`Sessions: ${sessionCount}`);

  const activeRes = data.active_reservation as Record<string, unknown> | null;
  if (activeRes) {
    lines.push(`Reserved: ${activeRes.worker} since ${activeRes.started_at}`);
  }

  return lines.join("\n");
}

export function formatHealth(data: Record<string, unknown>): string {
  const lines = ["Solo Health:"];
  const db = data.database as Record<string, unknown> | undefined;
  if (db) lines.push(`  DB: ${db.integrity} (${(Number(db.size_bytes) / 1024).toFixed(0)}KB)`);

  const tasks = data.tasks as Record<string, number> | undefined;
  if (tasks) {
    const parts = Object.entries(tasks).filter(([, v]) => v > 0).map(([k, v]) => `${v} ${k}`);
    lines.push(`  Tasks: ${parts.join(", ")}`);
  }

  const worktrees = data.worktrees as Record<string, unknown> | undefined;
  if (worktrees) lines.push(`  Worktrees: ${worktrees.active} active`);

  const issues = data.issues as string[] | undefined;
  if (issues && issues.length > 0) lines.push(`  ⚠ Issues: ${issues.join("; ")}`);

  lines.push(`  Machine: ${data.machine_id ?? "unknown"}`);
  return lines.join("\n");
}

export function shouldAutoInit(execCwd: SoloExecutionCwd): boolean {
  return execCwd.hasGitRepo && !execCwd.hasSoloDb;
}

function getTaskTimestamp(task: Record<string, unknown>): number | undefined {
  const value = task.updated_at ?? task.created_at;
  if (typeof value !== "string") return undefined;
  const timestamp = new Date(value).getTime();
  return Number.isNaN(timestamp) ? undefined : timestamp;
}

function isRecentlyCompleted(task: Record<string, unknown>, now: number): boolean {
  if (String(task.status ?? "") !== "completed") return false;
  const updatedTime = getTaskTimestamp(task);
  return updatedTime !== undefined && now - updatedTime < RECENTLY_COMPLETED_MS;
}

function compareWidgetTasks(a: Record<string, unknown>, b: Record<string, unknown>, now: number): number {
  const aRecent = isRecentlyCompleted(a, now);
  const bRecent = isRecentlyCompleted(b, now);
  // Recently completed tasks float to the very top.
  if (aRecent !== bRecent) return aRecent ? -1 : 1;

  const aStatus = String(a.status ?? "unknown");
  const bStatus = String(b.status ?? "unknown");
  const aOrder = WIDGET_STATUS_ORDER.indexOf(aStatus as (typeof WIDGET_STATUS_ORDER)[number]);
  const bOrder = WIDGET_STATUS_ORDER.indexOf(bStatus as (typeof WIDGET_STATUS_ORDER)[number]);
  if (aOrder !== bOrder) return (aOrder === -1 ? 99 : aOrder) - (bOrder === -1 ? 99 : bOrder);

  // Within the same status group, sort by updated_at descending (most recent first).
  const aUpdated = String(a.updated_at ?? a.created_at ?? "");
  const bUpdated = String(b.updated_at ?? b.created_at ?? "");
  if (aUpdated !== bUpdated) return bUpdated.localeCompare(aUpdated);

  return String(a.id ?? "").localeCompare(String(b.id ?? ""), undefined, { numeric: true });
}

function buildHiddenWidgetSummary(hiddenOverflow: number, hiddenCompleted: number): string | undefined {
  if (hiddenOverflow === 0 && hiddenCompleted === 0) return undefined;

  const parts: string[] = [];
  if (hiddenOverflow > 0) {
    parts.push(`${hiddenOverflow} more ${hiddenOverflow === 1 ? "task" : "tasks"}`);
  }
  if (hiddenCompleted > 0) {
    parts.push(`${hiddenCompleted} completed ${hiddenCompleted === 1 ? "task" : "tasks"}`);
  }

  return `… ${parts.join(" and ")} hidden — F8 to expand`;
}

export function getWidgetRefreshAt(tasks: Record<string, unknown>[], now = Date.now()): number | undefined {
  let nextRefreshAt: number | undefined;

  for (const task of tasks) {
    if (String(task.status ?? "") !== "completed") continue;
    const completedAt = getTaskTimestamp(task);
    if (completedAt === undefined) continue;

    const expiresAt = completedAt + RECENTLY_COMPLETED_MS;
    if (expiresAt <= now) continue;
    if (nextRefreshAt === undefined || expiresAt < nextRefreshAt) {
      nextRefreshAt = expiresAt;
    }
  }

  return nextRefreshAt;
}

export function formatWidgetLines(
  tasks: Record<string, unknown>[],
  health?: Record<string, unknown>,
  options: WidgetFormatOptions = {},
): string[] {
  const expanded = options.expanded ?? false;
  const now = options.now ?? Date.now();
  const maxVisible = options.maxVisible ?? 5;

  const counts: Record<string, number> = {};
  for (const task of tasks) {
    const status = String(task.status ?? "unknown");
    counts[status] = (counts[status] ?? 0) + 1;
  }

  const headerParts = WIDGET_STATUS_ORDER
    .map((status) => (counts[status] ? `${counts[status]} ${status}` : null))
    .filter((part): part is string => part !== null);
  let header = `${expanded ? "▾" : "▸"} Solo: ${headerParts.join(" | ") || "no tasks"}`;

  const issues = health?.issues as string[] | undefined;
  if (issues && issues.length > 0) {
    header += ` ⚠ ${issues.length} issue(s)`;
  }

  const lines = [header];
  const visiblePool = expanded
    ? [...tasks]
    : tasks.filter((task) => String(task.status ?? "") !== "completed" || isRecentlyCompleted(task, now));
  const sortedTasks = [...visiblePool].sort((a, b) => compareWidgetTasks(a, b, now));
  const visibleTasks = expanded ? sortedTasks : sortedTasks.slice(0, maxVisible);

  for (const task of visibleTasks) {
    const status = String(task.status ?? "unknown");
    const recent = isRecentlyCompleted(task, now);
    let icon = WIDGET_STATUS_ICONS[status] ?? "•";
    if (recent) icon = "✅";
    const id = String(task.id ?? "?");
    const rawTitle = typeof task.title === "string" ? task.title : "Untitled task";
    const title = rawTitle.length > 52 ? `${rawTitle.slice(0, 49)}...` : rawTitle;
    lines.push(`${icon} ${id} ${title}`);
  }

  if (!expanded) {
    const hiddenCompleted = tasks.filter((task) => String(task.status ?? "") === "completed" && !isRecentlyCompleted(task, now)).length;
    const hiddenOverflow = Math.max(0, visiblePool.length - visibleTasks.length);
    const hiddenSummary = buildHiddenWidgetSummary(hiddenOverflow, hiddenCompleted);
    if (hiddenSummary) {
      lines.push(hiddenSummary);
    }
  }

  return lines;
}
