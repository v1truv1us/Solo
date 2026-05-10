import { StringEnum } from "@earendil-works/pi-ai";
import { type ExtensionAPI, type ExtensionContext, getAgentDir } from "@earendil-works/pi-coding-agent";
import { type Static, Type } from "typebox";

import {
  runSolo,
  formatSoloCliError,
  formatTaskList,
  formatTaskOneLine,
  formatTaskDetail,
  formatHealth,
  formatWidgetLines,
  getWidgetRefreshAt,
  resolveSoloExecutionCwd,
  shouldAutoInit,
} from "./format.js";
import {
  type SoloErrorRecord,
  appendErrorRecord,
  buildErrorRecord,
  categorizeError,
  formatErrorDetails,
  formatErrorSummary,
  getErrorLogPath,
  parseErrorCode,
} from "./error-log.js";

// --- Tool parameter schema ---
const SOLO_TOOL_PARAMS = Type.Object({
  action: StringEnum([
    "init",
    "task_list",
    "task_show",
    "task_create",
    "task_ready",
    "task_update",
    "task_context",
    "session_start",
    "session_end",
    "handoff",
    "worktree_list",
    "recover",
    "health",
    "search",
  ] as const),

  // task_list
  status: Type.Optional(StringEnum(["draft", "ready", "active", "completed", "failed", "blocked", "cancelled"] as const)),
  available: Type.Optional(Type.Boolean({ description: "Only show available (unlocked, ready) tasks" })),
  limit: Type.Optional(Type.Number({ description: "Max tasks to return (default 20)" })),

  // task_show, task_create, session_start, session_end, task_ready, handoff
  taskId: Type.Optional(Type.String({ description: "Task ID (e.g. T-12)" })),
  version: Type.Optional(Type.Number({ description: "Task version for OCC (from task_show or task_list)" })),

  // task_create
  title: Type.Optional(Type.String({ description: "Task title for task_create" })),
  description: Type.Optional(Type.String({ description: "Task description" })),
  priority: Type.Optional(StringEnum(["low", "medium", "high", "critical"] as const)),
  tags: Type.Optional(Type.String({ description: "Comma-separated tags/labels" })),

  // session_start
  worker: Type.Optional(Type.String({ description: "Worker name for session_start (default: pi)" })),

  // session_end
  result: Type.Optional(StringEnum(["completed", "failed", "interrupted", "abandoned"] as const)),
  notes: Type.Optional(Type.String({ description: "Notes for session_end" })),

  // handoff
  summary: Type.Optional(Type.String({ description: "Summary of work done" })),
  remainingWork: Type.Optional(Type.String({ description: "What still needs doing" })),
  toWorker: Type.Optional(Type.String({ description: "Next agent to hand off to" })),

  // search
  query: Type.Optional(Type.String({ description: "Search query" })),
});

type SoloToolInput = Static<typeof SOLO_TOOL_PARAMS>;

// --- State ---
interface SoloState {
  lastHealth: Record<string, unknown> | null;
  lastTasks: Record<string, unknown>[];
  lastError: string | null;
  lastRepoRoot: string | null;
  initialized: boolean;
  widgetExpanded: boolean;
  widgetRefreshTimer: ReturnType<typeof setTimeout> | null;
  widgetContext: ExtensionContext | null;
}

export default function soloExtension(pi: ExtensionAPI) {
  const agentDir = getAgentDir();
  const errorLogPath = getErrorLogPath(agentDir);
  const state: SoloState = {
    lastHealth: null,
    lastTasks: [],
    lastError: null,
    lastRepoRoot: null,
    initialized: false,
    widgetExpanded: false,
    widgetRefreshTimer: null,
    widgetContext: null,
  };

  // --- Error logging helper ---
  async function logSoloError(
    ctx: ExtensionContext,
    category: SoloErrorRecord["category"],
    message: string,
    fields: Partial<SoloErrorRecord> = {},
  ): Promise<SoloErrorRecord> {
    const record = buildErrorRecord(category, message, { action: fields.action ?? "unknown", ...fields });
    try {
      await appendErrorRecord(errorLogPath, record);
    } catch {
      // Don't let error logging failures cascade
    }
    return record;
  }

  function clearWidgetRefreshTimer(): void {
    if (state.widgetRefreshTimer) {
      clearTimeout(state.widgetRefreshTimer);
      state.widgetRefreshTimer = null;
    }
  }

  function scheduleWidgetRefresh(): void {
    clearWidgetRefreshTimer();
    if (!state.widgetContext || state.widgetExpanded) {
      return;
    }

    const now = Date.now();
    const nextRefreshAt = getWidgetRefreshAt(state.lastTasks, now);
    if (nextRefreshAt === undefined) {
      return;
    }

    const delay = Math.max(250, nextRefreshAt - now + 50);
    const timer = setTimeout(() => {
      state.widgetRefreshTimer = null;
      const ctx = state.widgetContext;
      if (ctx) {
        renderWidget(ctx);
      }
    }, delay);
    timer.unref?.();
    state.widgetRefreshTimer = timer;
  }

  function renderWidget(ctx: ExtensionContext): void {
    state.widgetContext = ctx;
    clearWidgetRefreshTimer();
    if (!ctx.hasUI) {
      return;
    }

    if (state.lastError) {
      const lines = [`Solo: ${state.lastError}`];
      const location = state.lastRepoRoot ?? undefined;
      if (location) lines.push(`cwd: ${location}`);
      ctx.ui.setWidget("solo", lines);
      return;
    }

    if (state.lastTasks.length > 0 || state.lastHealth) {
      ctx.ui.setWidget(
        "solo",
        formatWidgetLines(state.lastTasks, state.lastHealth ?? undefined, { expanded: state.widgetExpanded, now: Date.now() }),
      );
      scheduleWidgetRefresh();
      return;
    }

    const lines = ["Solo: no tasks found"];
    if (state.lastRepoRoot) lines.push(`repo: ${state.lastRepoRoot}`);
    ctx.ui.setWidget("solo", lines);
  }

  async function ensureSoloInitialized(ctx: ExtensionContext, options: { silent?: boolean } = {}): Promise<boolean> {
    const execCwd = await resolveSoloExecutionCwd(ctx.cwd);
    state.lastRepoRoot = execCwd.repoRoot ?? state.lastRepoRoot;
    state.initialized = execCwd.hasSoloDb;

    if (!shouldAutoInit(execCwd)) {
      return false;
    }

    const { result, exec } = await runSolo(pi, ctx, ["init"]);
    const code = parseErrorCode(exec.stdout, exec.stderr);
    if (!result.ok && code !== "ALREADY_INITIALIZED") {
      const record = await logSoloError(ctx, categorizeError(code, exec.code), "Solo auto-init failed", {
        action: "init",
        exitCode: exec.code,
        stdoutPreview: exec.stdout.slice(0, 300),
        stderrPreview: exec.stderr.slice(0, 300),
      });
      throw new Error(`Solo auto-init failed${options.silent ? "" : ` (logged as ${record.id})`}: ${result.raw}`);
    }

    state.initialized = true;
    if (!options.silent && ctx.hasUI) {
      const location = execCwd.repoRoot ?? execCwd.cwd;
      ctx.ui.notify(`Solo initialized in ${location}`, "info");
    }
    return true;
  }

  // --- Widget refresh ---
  async function refreshWidget(ctx: ExtensionContext): Promise<void> {
    try {
      state.lastError = null;

      await ensureSoloInitialized(ctx, { silent: true });

      const healthResult = await runSolo(pi, ctx, ["health"]);
      const taskResult = await runSolo(pi, ctx, ["task", "list"]);

      state.lastRepoRoot = taskResult.result.repoRoot ?? healthResult.result.repoRoot ?? null;

      if (healthResult.result.ok && healthResult.result.data) {
        state.lastHealth = healthResult.result.data as Record<string, unknown>;
      } else {
        state.lastHealth = null;
      }

      if (taskResult.result.ok && taskResult.result.data) {
        const data = taskResult.result.data as Record<string, unknown>;
        state.lastTasks = (data.tasks ?? []) as Record<string, unknown>[];
      } else {
        state.lastTasks = [];
      }

      if (!healthResult.result.ok) {
        state.lastError = `health failed — ${formatSoloCliError(healthResult.result.raw)}`;
      } else if (!taskResult.result.ok) {
        state.lastError = `task list failed — ${formatSoloCliError(taskResult.result.raw)}`;
      }

      if (state.lastError) {
        renderWidget(ctx);
        return;
      }

      renderWidget(ctx);
    } catch (error) {
      state.lastTasks = [];
      state.lastHealth = null;
      state.lastError = error instanceof Error ? error.message : "Unknown Solo widget error";
      renderWidget(ctx);
    }
  }

  function clearWidget(ctx: ExtensionContext): void {
    clearWidgetRefreshTimer();
    state.widgetContext = null;
    ctx.ui.setWidget("solo", undefined);
  }

  function toggleWidgetDetails(ctx: ExtensionContext): void {
    state.widgetExpanded = !state.widgetExpanded;
    renderWidget(ctx);
  }

  // --- Lifecycle events ---
  pi.on("session_start", async (_event, ctx) => {
    state.initialized = true;
    state.widgetExpanded = false;
    await refreshWidget(ctx);
  });

  pi.on("session_shutdown", (_event, ctx) => {
    clearWidget(ctx);
  });

  pi.registerShortcut("f8", {
    description: "Toggle Solo widget details",
    handler: async (ctx) => toggleWidgetDetails(ctx),
  });

  pi.registerShortcut("ctrl+shift+s", {
    description: "Toggle Solo widget details",
    handler: async (ctx) => toggleWidgetDetails(ctx),
  });

  // Auto-inject Solo context so the agent always knows what's available
  pi.on("before_agent_start", async (_event, _ctx) => {
    // We don't block agent start — just inject available tasks as context
    if (state.lastTasks.length === 0) return;

    const ready = state.lastTasks.filter((t) => t.status === "ready");
    const active = state.lastTasks.filter((t) => t.status === "active");
    if (ready.length === 0 && active.length === 0) return;

    const lines = ["Solo task tracker is active in this repo."];
    if (active.length > 0) {
      lines.push(`Active tasks: ${active.map((t) => `${t.id}: ${t.title}`).join("; ")}`);
    }
    if (ready.length > 0) {
      lines.push(`Ready tasks: ${ready.map((t) => `${t.id}: ${t.title}`).join("; ")}`);
    }
    lines.push("Use the solo tool to manage tasks.");

    return {
      message: {
        customType: "solo-context",
        content: lines.join("\n"),
        display: false,
      },
    };
  });

  // Refresh widget after solo tool executions
  pi.on("tool_execution_end", async (event, ctx) => {
    if (event.toolName === "solo" && !event.isError) {
      await refreshWidget(ctx);
    }
  });

  // --- Solo tool ---
  pi.registerTool({
    name: "solo",
    label: "Solo",
    description:
      "Manage tasks with Solo task tracker. Create, list, claim, complete, and hand off tasks. " +
      "Always check available tasks before starting work. End sessions explicitly when done.",
    promptSnippet: "Manage tasks with Solo — create, claim, complete, and hand off work items",
    promptGuidelines: [
      "Use the solo tool when the user asks about tasks, wants to create work items, or needs to coordinate agent work.",
      "Before starting implementation work, check solo with action=task_list and available=true to see if there are tasks to pick up.",
      "When starting a task, use action=session_start to claim it — this creates an isolated worktree for safe work.",
      "Always end sessions with action=session_end when done with a task, even on failure.",
    ],
    parameters: SOLO_TOOL_PARAMS,

    async execute(_toolCallId, input, _signal, _onUpdate, ctx): Promise<{
      content: Array<{ type: "text"; text: string }>;
      details: Record<string, unknown>;
    }> {
      try {
        if (input.action !== "init") {
          await ensureSoloInitialized(ctx);
        }
        switch (input.action) {
          case "init": {
            const { result, exec } = await runSolo(pi, ctx, ["init"]);

            if (!result.ok) {
              const code = parseErrorCode(exec.stdout, exec.stderr);
              if (code === "ALREADY_INITIALIZED") {
                return {
                  content: [{ type: "text", text: "Solo is already initialized in this repo." }],
                  details: { action: "init", initialized: true },
                };
              }
              await logSoloError(ctx, categorizeError(code), "Solo init failed", {
                action: "init",
                exitCode: exec.code,
                stdoutPreview: exec.stdout.slice(0, 300),
                stderrPreview: exec.stderr.slice(0, 300),
              });
              throw new Error(`Solo init failed: ${result.raw}`);
            }

            const data = result.data as Record<string, unknown>;
            return {
              content: [{ type: "text", text: `Solo initialized. DB: ${data.database}` }],
              details: { action: "init", ...data },
            };
          }

          case "task_list": {
            const args = ["task", "list"];
            if (input.available) args.push("--available");
            if (input.status) args.push("--status", input.status);
            if (input.limit) args.push("--limit", String(input.limit));
            const { result, exec } = await runSolo(pi, ctx, args);

            if (!result.ok) {
              const code = parseErrorCode(exec.stdout, exec.stderr);
              const cat = categorizeError(code, exec.code);
              await logSoloError(ctx, cat, "task_list failed", {
                action: "task_list",
                exitCode: exec.code,
                stdoutPreview: exec.stdout.slice(0, 300),
                stderrPreview: exec.stderr.slice(0, 300),
              });
              throw new Error(`Solo task_list failed: ${result.raw}`);
            }

            const data = result.data as Record<string, unknown>;
            const tasks = (data.tasks ?? []) as Record<string, unknown>[];
            state.lastTasks = tasks;
            return {
              content: [{ type: "text", text: formatTaskList(tasks, data.total as number | undefined) }],
              details: { action: "task_list", tasks, total: data.total },
            };
          }

          case "task_show": {
            if (!input.taskId) throw new Error("taskId required for task_show");
            const { result, exec } = await runSolo(pi, ctx, ["task", "show", input.taskId]);

            if (!result.ok) {
              await logSoloError(ctx, categorizeError(parseErrorCode(exec.stdout, exec.stderr)), `task_show failed for ${input.taskId}`, {
                action: "task_show",
                taskId: input.taskId,
                exitCode: exec.code,
                stdoutPreview: exec.stdout.slice(0, 300),
                stderrPreview: exec.stderr.slice(0, 300),
              });
              throw new Error(`Solo task_show failed: ${result.raw}`);
            }

            const data = result.data as Record<string, unknown>;
            return {
              content: [{ type: "text", text: formatTaskDetail(data) }],
              details: { action: "task_show", ...data },
            };
          }

          case "task_create": {
            if (!input.title) throw new Error("title required for task_create");
            const args = ["task", "create", "--title", input.title];
            if (input.description) args.push("--description", input.description);
            if (input.priority) args.push("--priority", input.priority);
            if (input.tags) args.push("--labels", input.tags);
            const { result, exec } = await runSolo(pi, ctx, args);

            if (!result.ok) {
              await logSoloError(ctx, categorizeError(parseErrorCode(exec.stdout, exec.stderr)), `task_create failed: ${input.title}`, {
                action: "task_create",
                exitCode: exec.code,
                stdoutPreview: exec.stdout.slice(0, 300),
                stderrPreview: exec.stderr.slice(0, 300),
              });
              throw new Error(`Solo task_create failed: ${result.raw}`);
            }

            const task = result.data as Record<string, unknown>;
            return {
              content: [{ type: "text", text: `Created ${task.id}: ${task.title} (${task.status})` }],
              details: { action: "task_create", task },
            };
          }

          case "task_ready": {
            if (!input.taskId) throw new Error("taskId required for task_ready");
            if (!input.version) throw new Error("version required for task_ready (get it from task_show or task_list)");
            const { result, exec } = await runSolo(pi, ctx, ["task", "ready", input.taskId, "--version", String(input.version)]);

            if (!result.ok) {
              await logSoloError(ctx, categorizeError(parseErrorCode(exec.stdout, exec.stderr)), `task_ready failed for ${input.taskId}`, {
                action: "task_ready",
                taskId: input.taskId,
                exitCode: exec.code,
                stdoutPreview: exec.stdout.slice(0, 300),
                stderrPreview: exec.stderr.slice(0, 300),
              });
              throw new Error(`Solo task_ready failed: ${result.raw}`);
            }

            return {
              content: [{ type: "text", text: `${input.taskId} is now ready` }],
              details: { action: "task_ready", taskId: input.taskId },
            };
          }

          case "task_update": {
            if (!input.taskId) throw new Error("taskId required for task_update");
            if (!input.version) throw new Error("version required for task_update (get it from task_show)");
            const args = ["task", "update", input.taskId, "--version", String(input.version)];
            if (input.title) args.push("--title", input.title);
            if (input.description) args.push("--description", input.description);
            if (input.priority) args.push("--priority", input.priority);
            if (input.tags) args.push("--labels", input.tags);
            const { result, exec } = await runSolo(pi, ctx, args);

            if (!result.ok) {
              await logSoloError(ctx, categorizeError(parseErrorCode(exec.stdout, exec.stderr)), `task_update failed for ${input.taskId}`, {
                action: "task_update",
                taskId: input.taskId,
                exitCode: exec.code,
                stdoutPreview: exec.stdout.slice(0, 300),
                stderrPreview: exec.stderr.slice(0, 300),
              });
              throw new Error(`Solo task_update failed: ${result.raw}`);
            }

            const task = (result.data as Record<string, unknown>).task ?? result.data;
            return {
              content: [{ type: "text", text: `Updated ${input.taskId} (v${(task as Record<string, unknown>).version})` }],
              details: { action: "task_update", task },
            };
          }

          case "task_context": {
            if (!input.taskId) throw new Error("taskId required for task_context");
            const { result, exec } = await runSolo(pi, ctx, ["task", "context", input.taskId]);

            if (!result.ok) {
              await logSoloError(ctx, categorizeError(parseErrorCode(exec.stdout, exec.stderr)), `task_context failed for ${input.taskId}`, {
                action: "task_context",
                taskId: input.taskId,
                exitCode: exec.code,
                stdoutPreview: exec.stdout.slice(0, 300),
                stderrPreview: exec.stderr.slice(0, 300),
              });
              throw new Error(`Solo task_context failed: ${result.raw}`);
            }

            const data = result.data as Record<string, unknown>;
            const task = data.task as Record<string, unknown> | undefined;
            const lines = [`Context for ${input.taskId}`];
            if (task) {
              lines.push(`  Title: ${task.title}`);
              lines.push(`  Status: ${task.status}`);
            }
            if (data.latest_handoff) {
              const h = data.latest_handoff as Record<string, unknown>;
              lines.push(`  Last handoff: ${h.summary ?? "none"}`);
              if (h.remaining_work) lines.push(`  Remaining: ${h.remaining_work}`);
            }
            const sessions = (data.recent_sessions ?? []) as Record<string, unknown>[];
            if (sessions.length > 0) {
              lines.push(`  Prior sessions: ${sessions.length}`);
            }
            if (data.worktree) lines.push(`  Worktree: ${data.worktree}`);

            return {
              content: [{ type: "text", text: lines.join("\n") }],
              details: { action: "task_context", ...data },
            };
          }

          case "session_start": {
            if (!input.taskId) throw new Error("taskId required for session_start");
            const worker = input.worker ?? "pi";
            const { result, exec } = await runSolo(pi, ctx, ["session", "start", input.taskId, "--worker", worker]);

            if (!result.ok) {
              const code = parseErrorCode(exec.stdout, exec.stderr);
              await logSoloError(ctx, categorizeError(code), `session_start failed for ${input.taskId}`, {
                action: "session_start",
                taskId: input.taskId,
                exitCode: exec.code,
                stdoutPreview: exec.stdout.slice(0, 300),
                stderrPreview: exec.stderr.slice(0, 300),
              });
              throw new Error(`Solo session_start failed: ${result.raw}`);
            }

            const data = result.data as Record<string, unknown>;
            const contextBundle = data.context as Record<string, unknown> | undefined;
            const task = (contextBundle?.task ?? (data as Record<string, unknown>).task) as Record<string, unknown> | undefined;
            const worktree = data.worktree_path as string | undefined;

            const lines = [`Session started for ${input.taskId}`];
            if (task) lines.push(`  Title: ${task.title}`);
            if (worktree) lines.push(`  Worktree: ${worktree}`);
            if (data.branch) lines.push(`  Branch: ${data.branch}`);
            if (data.expires_at) lines.push(`  Expires: ${data.expires_at}`);

            const handoff = (contextBundle?.latest_handoff ?? (data as Record<string, unknown>).latest_handoff) as Record<string, unknown> | undefined;
            if (handoff) {
              lines.push(`  Prior work: ${handoff.summary ?? "none"}`);
              if (handoff.remaining_work) lines.push(`  Remaining: ${handoff.remaining_work}`);
            }

            return {
              content: [{ type: "text", text: lines.join("\n") }],
              details: { action: "session_start", session: data },
            };
          }

          case "session_end": {
            if (!input.taskId) throw new Error("taskId required for session_end");
            if (!input.result) throw new Error("result required for session_end (completed|failed|interrupted|abandoned)");
            const args = ["session", "end", input.taskId, "--result", input.result];
            if (input.notes) args.push("--notes", input.notes);
            const { result, exec } = await runSolo(pi, ctx, args);

            if (!result.ok) {
              await logSoloError(ctx, categorizeError(parseErrorCode(exec.stdout, exec.stderr)), `session_end failed for ${input.taskId}`, {
                action: "session_end",
                taskId: input.taskId,
                exitCode: exec.code,
                stdoutPreview: exec.stdout.slice(0, 300),
                stderrPreview: exec.stderr.slice(0, 300),
              });
              throw new Error(`Solo session_end failed: ${result.raw}`);
            }

            return {
              content: [{ type: "text", text: `${input.taskId} session ended: ${input.result}` }],
              details: { action: "session_end", taskId: input.taskId, result: input.result },
            };
          }

          case "handoff": {
            if (!input.taskId) throw new Error("taskId required for handoff");
            if (!input.summary) throw new Error("summary required for handoff");
            const args = ["handoff", "create", input.taskId, "--summary", input.summary];
            if (input.remainingWork) args.push("--remaining-work", input.remainingWork);
            if (input.toWorker) args.push("--to", input.toWorker);
            const { result, exec } = await runSolo(pi, ctx, args);

            if (!result.ok) {
              await logSoloError(ctx, categorizeError(parseErrorCode(exec.stdout, exec.stderr)), `handoff failed for ${input.taskId}`, {
                action: "handoff",
                taskId: input.taskId,
                exitCode: exec.code,
                stdoutPreview: exec.stdout.slice(0, 300),
                stderrPreview: exec.stderr.slice(0, 300),
              });
              throw new Error(`Solo handoff failed: ${result.raw}`);
            }

            return {
              content: [{ type: "text", text: `Handed off ${input.taskId}${input.toWorker ? ` to ${input.toWorker}` : ""}` }],
              details: { action: "handoff", taskId: input.taskId },
            };
          }

          case "health": {
            const { result, exec } = await runSolo(pi, ctx, ["health"]);

            if (!result.ok) {
              await logSoloError(ctx, "health_degraded", "Solo health check failed", {
                action: "health",
                exitCode: exec.code,
                stdoutPreview: exec.stdout.slice(0, 300),
                stderrPreview: exec.stderr.slice(0, 300),
              });
              throw new Error(`Solo health check failed: ${result.raw}`);
            }

            const data = result.data as Record<string, unknown>;
            state.lastHealth = data;

            const issues = data.issues as string[] | undefined;
            if (issues && issues.length > 0) {
              await logSoloError(ctx, "health_degraded", `Solo health issues: ${issues.join("; ")}`, {
                action: "health",
              });
            }

            return {
              content: [{ type: "text", text: formatHealth(data) }],
              details: { action: "health", health: data },
            };
          }

          case "search": {
            if (!input.query) throw new Error("query required for search");
            const args = ["search", input.query];
            if (input.limit) args.push("--limit", String(input.limit));
            const { result, exec } = await runSolo(pi, ctx, args);

            if (!result.ok) {
              await logSoloError(ctx, categorizeError(parseErrorCode(exec.stdout, exec.stderr)), `search failed: ${input.query}`, {
                action: "search",
                exitCode: exec.code,
                stdoutPreview: exec.stdout.slice(0, 300),
                stderrPreview: exec.stderr.slice(0, 300),
              });
              throw new Error(`Solo search failed: ${result.raw}`);
            }

            const data = result.data as Record<string, unknown>;
            const tasks = (data.tasks ?? data.results ?? []) as Record<string, unknown>[];
            return {
              content: [{ type: "text", text: formatTaskList(tasks) }],
              details: { action: "search", tasks },
            };
          }

          case "worktree_list": {
            const { result, exec } = await runSolo(pi, ctx, ["worktree", "list"]);

            if (!result.ok) {
              await logSoloError(ctx, categorizeError(parseErrorCode(exec.stdout, exec.stderr)), "worktree_list failed", {
                action: "worktree_list",
                exitCode: exec.code,
                stdoutPreview: exec.stdout.slice(0, 300),
                stderrPreview: exec.stderr.slice(0, 300),
              });
              throw new Error(`Solo worktree_list failed: ${result.raw}`);
            }

            const data = result.data as Record<string, unknown>;
            const worktrees = (data.worktrees ?? []) as Record<string, unknown>[];
            if (worktrees.length === 0) {
              return {
                content: [{ type: "text", text: `No active worktrees. ${data.max ?? 5} max.` }],
                details: { action: "worktree_list", worktrees: [], max: data.max },
              };
            }
            const lines = worktrees.map((w) => `${w.task_id ?? "?"} ${w.branch_name ?? "?"} ${w.status ?? "?"} ${(w.path as string ?? "").slice(-40)}`);
            return {
              content: [{ type: "text", text: `Worktrees (${worktrees.length}/${data.max ?? 5}):\n${lines.join("\n")}` }],
              details: { action: "worktree_list", worktrees, max: data.max },
            };
          }

          case "recover": {
            const { result, exec } = await runSolo(pi, ctx, ["recover", "--all"]);

            if (!result.ok) {
              await logSoloError(ctx, categorizeError(parseErrorCode(exec.stdout, exec.stderr)), "recover failed", {
                action: "recover",
                exitCode: exec.code,
                stdoutPreview: exec.stdout.slice(0, 300),
                stderrPreview: exec.stderr.slice(0, 300),
              });
              throw new Error(`Solo recover failed: ${result.raw}`);
            }

            const data = result.data as Record<string, unknown>;
            return {
              content: [{ type: "text", text: `Recovery complete. Scanned: ${data.scanned ?? 0}, Recovered: ${data.recovered ?? 0}` }],
              details: { action: "recover", ...data },
            };
          }

          default:
            throw new Error(`Unknown solo action: ${input.action}`);
        }
      } catch (error) {
        // Catch unhandled errors (e.g. CLI not found) and log them
        if (error instanceof Error && !error.message.startsWith("Solo ")) {
          const record = await logSoloError(ctx, "unknown", error.message, { action: input.action, taskId: input.taskId });
          throw new Error(`Solo ${input.action} error (logged as ${record.id}): ${error.message}`);
        }
        throw error;
      }
    },
  });

  // --- Commands ---

  pi.registerCommand("solo", {
    description: "Show Solo status, tasks, and health",
    handler: async (_args, ctx) => {
      await ensureSoloInitialized(ctx);
      const healthResult = await runSolo(pi, ctx, ["health"]);
      if (healthResult.result.ok && healthResult.result.data) {
        ctx.ui.notify(formatHealth(healthResult.result.data as Record<string, unknown>), "info");
      } else {
        const location = healthResult.result.repoRoot ?? healthResult.result.cwd;
        ctx.ui.notify(`Solo health failed${location ? ` (${location})` : ""}: ${formatSoloCliError(healthResult.result.raw)}`, "warning");
      }

      const taskResult = await runSolo(pi, ctx, ["task", "list"]);
      if (!taskResult.result.ok || !taskResult.result.data) {
        const location = taskResult.result.repoRoot ?? taskResult.result.cwd;
        ctx.ui.notify(`Solo task list failed${location ? ` (${location})` : ""}: ${formatSoloCliError(taskResult.result.raw)}`, "error");
        return;
      }

      const data = taskResult.result.data as Record<string, unknown>;
      const tasks = (data.tasks ?? []) as Record<string, unknown>[];
      if (tasks.length > 0) {
        const lines = tasks.map(formatTaskOneLine);
        await ctx.ui.editor("Solo Tasks", lines.join("\n"));
      } else {
        ctx.ui.notify("No tasks in this repo.", "info");
      }
    },
  });

  pi.registerCommand("solo-pick", {
    description: "Claim and start the next available Solo task",
    handler: async (_args, ctx) => {
      await ensureSoloInitialized(ctx);
      const { result } = await runSolo(pi, ctx, ["task", "list", "--available"]);
      if (!result.ok || !result.data) {
        ctx.ui.notify(`Failed to list tasks: ${formatSoloCliError(result.raw)}`, "error");
        return;
      }

      const data = result.data as Record<string, unknown>;
      const tasks = (data.tasks ?? []) as Record<string, unknown>[];
      if (tasks.length === 0) {
        ctx.ui.notify("No available tasks to pick up.", "info");
        return;
      }

      const choices = tasks.map((t) => formatTaskOneLine(t));
      const selected = await ctx.ui.select("Pick a task:", choices);
      if (!selected) return;

      const idx = choices.indexOf(selected);
      const task = tasks[idx];

      const sessionResult = await runSolo(pi, ctx, ["session", "start", task.id as string, "--worker", "pi"]);
      if (!sessionResult.result.ok) {
        const record = await logSoloError(ctx, "session_failed", `Failed to start session for ${task.id}`, {
          action: "solo-pick",
          taskId: task.id as string,
          stdoutPreview: sessionResult.exec.stdout.slice(0, 300),
          stderrPreview: sessionResult.exec.stderr.slice(0, 300),
        });
        ctx.ui.notify(`Failed to claim ${task.id}: ${sessionResult.result.raw}\nLogged as ${record.id}`, "error");
        return;
      }

      const sessionData = sessionResult.result.data as Record<string, unknown>;
      const worktree = sessionData.worktree_path as string | undefined;
      ctx.ui.notify(`Claimed ${task.id}: ${task.title}${worktree ? `\nWorktree: ${worktree}` : ""}`, "info");
      await refreshWidget(ctx);
    },
  });

  pi.registerCommand("solo-done", {
    description: "Complete the current active Solo task: /solo-done [notes]",
    handler: async (args, ctx) => {
      await ensureSoloInitialized(ctx);
      // Find active task
      const { result } = await runSolo(pi, ctx, ["task", "list", "--status", "active"]);
      if (!result.ok || !result.data) {
        ctx.ui.notify(`Failed to list active tasks: ${formatSoloCliError(result.raw)}`, "warning");
        return;
      }

      const data = result.data as Record<string, unknown>;
      const tasks = (data.tasks ?? []) as Record<string, unknown>[];
      if (tasks.length === 0) {
        ctx.ui.notify("No active tasks to complete.", "info");
        return;
      }

      let target = tasks[0];
      if (tasks.length > 1) {
        const choices = tasks.map(formatTaskOneLine);
        const selected = await ctx.ui.select("Complete which task?", choices);
        if (!selected) return;
        target = tasks[choices.indexOf(selected)];
      }

      const endArgs = ["session", "end", target.id as string, "--result", "completed"];
      if (args.trim()) endArgs.push("--notes", args.trim());

      const endResult = await runSolo(pi, ctx, endArgs);
      if (!endResult.result.ok) {
        ctx.ui.notify(`Failed to complete ${target.id}: ${endResult.result.raw}`, "error");
        return;
      }

      ctx.ui.notify(`✓ Completed ${target.id}: ${target.title}`, "info");
      await refreshWidget(ctx);
    },
  });

  pi.registerCommand("solo-errors", {
    description: "Browse recent Solo errors: /solo-errors [count]",
    handler: async (args, ctx) => {
      const count = Math.min(Math.max(parseInt(args.trim(), 10) || 10, 1), 50);

      try {
        const { readFile: readFileFs } = await import("node:fs/promises");
      const content = await readFileFs(errorLogPath, "utf-8");
        const lines = content.trim().split("\n").filter((l: string) => l.trim().length > 0);
        const records = lines.slice(-count).reverse().map((line: string) => {
          try { return JSON.parse(line) as SoloErrorRecord; } catch { return null; }
        }).filter((r: SoloErrorRecord | null): r is SoloErrorRecord => r !== null);

        if (records.length === 0) {
          ctx.ui.notify("No Solo errors logged.", "info");
          return;
        }

        const choices = records.map(formatErrorSummary);
        const selected = await ctx.ui.select(`Recent Solo errors (${records.length})`, choices);
        if (!selected) return;

        const record = records[choices.indexOf(selected)];
        if (record) {
          await ctx.ui.editor(`Solo Error ${record.id}`, formatErrorDetails(record));
        }
      } catch {
        ctx.ui.notify(`No error log found at ${errorLogPath}`, "info");
      }
    },
  });
}
