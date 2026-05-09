import { resolve, dirname } from "node:path";
import { appendFile, mkdir } from "node:fs/promises";

export interface SoloErrorRecord {
  id: string;
  timestamp: string;
  category: SoloErrorCategory;
  message: string;
  action: string;
  taskId?: string;
  exitCode?: number;
  stdoutPreview?: string;
  stderrPreview?: string;
  suggestedFix?: string;
}

export type SoloErrorCategory =
  | "cli_not_found"
  | "cli_exec_failed"
  | "cli_invalid_json"
  | "not_initialized"
  | "task_locked"
  | "version_conflict"
  | "session_failed"
  | "health_degraded"
  | "widget_refresh_failed"
  | "unknown";

const ERROR_CODES_TO_CATEGORIES: Record<string, SoloErrorCategory> = {
  TASK_LOCKED: "task_locked",
  HANDOFF_LOCKED: "task_locked",
  VERSION_CONFLICT: "version_conflict",
  TASK_NOT_READY: "session_failed",
  NOT_A_REPO: "not_initialized",
  NOT_INITIALIZED: "not_initialized",
  WORKTREE_ERROR: "session_failed",
  SQLITE_BUSY: "cli_exec_failed",
};

const ERROR_FIXES: Record<SoloErrorCategory, string> = {
  cli_not_found: "Install Solo: brew install v1truv1us/tap/solo",
  cli_exec_failed: "Run `solo health --json` to diagnose. Check .solo/solo.db integrity.",
  cli_invalid_json: "Solo CLI returned unexpected output. Check `solo --version` and update if stale.",
  not_initialized: "Run `solo init` in the repo root before using task commands.",
  task_locked: "Another agent holds this task. Use `solo task list --available --json` to find unlocked tasks.",
  version_conflict: "Task was modified concurrently. Re-read with `solo task show` and retry with the new version.",
  session_failed: "Session could not start. Check `solo health` and `solo recover --all`.",
  health_degraded: "Solo health check reported issues. Run `solo health --json` for details.",
  widget_refresh_failed: "Widget could not read task state. Solo CLI may be misconfigured.",
  unknown: "Check `solo health --json` and review ~/.pi/agent/solo-errors.jsonl.",
};

export function getErrorLogPath(agentDir: string): string {
  return resolve(agentDir, "solo-errors.jsonl");
}

export function createErrorId(): string {
  const hex = Array.from(crypto.getRandomValues(new Uint8Array(4)))
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
  return `SERR-${hex}`;
}

export function categorizeError(errorCode?: string, exitCode?: number): SoloErrorCategory {
  if (errorCode && errorCode in ERROR_CODES_TO_CATEGORIES) {
    return ERROR_CODES_TO_CATEGORIES[errorCode];
  }
  if (exitCode === 127) return "cli_not_found";
  return "unknown";
}

export function buildErrorRecord(
  category: SoloErrorCategory,
  message: string,
  fields: Partial<SoloErrorRecord> = {},
): SoloErrorRecord {
  return {
    id: createErrorId(),
    timestamp: new Date().toISOString(),
    category,
    message,
    action: fields.action ?? "unknown",
    suggestedFix: ERROR_FIXES[category],
    ...fields,
  };
}

export async function appendErrorRecord(logPath: string, record: SoloErrorRecord): Promise<void> {
  await mkdir(dirname(logPath), { recursive: true });
  const line = JSON.stringify(record) + "\n";
  await appendFile(logPath, line, "utf-8");
}

export function parseErrorCode(stdout: string, stderr: string): string | undefined {
  // Solo CLI returns JSON errors with a `code` field
  const text = stdout.trim() || stderr.trim();
  if (!text) return undefined;
  try {
    const parsed = JSON.parse(text);
    return parsed?.error?.code ?? undefined;
  } catch {
    return undefined;
  }
}

export function formatErrorSummary(record: SoloErrorRecord): string {
  const tag = record.taskId ? ` [${record.taskId}]` : "";
  return `${record.id} ${record.category}${tag} ${record.message}`;
}

export function formatErrorDetails(record: SoloErrorRecord): string {
  const lines = [
    `Error:    ${record.id}`,
    `Category: ${record.category}`,
    `Time:     ${record.timestamp}`,
    `Message:  ${record.message}`,
  ];
  if (record.action) lines.push(`Action:   ${record.action}`);
  if (record.taskId) lines.push(`Task:     ${record.taskId}`);
  if (record.exitCode !== undefined) lines.push(`Exit:     ${record.exitCode}`);
  if (record.suggestedFix) lines.push(`Fix:      ${record.suggestedFix}`);
  if (record.stdoutPreview) lines.push(`Stdout:   ${record.stdoutPreview.slice(0, 200)}`);
  if (record.stderrPreview) lines.push(`Stderr:   ${record.stderrPreview.slice(0, 200)}`);
  return lines.join("\n");
}
