import { useEffect, useState } from "react";
import type { FormEvent } from "react";
import { apiFetch, isApiError } from "../api/client";
import type { LogSearchResult, NodePage, TaskRecord } from "../api/client";
import { EmptyState } from "../components/EmptyState";
import { StatusBadge } from "../components/StatusBadge";
import type { BadgeVariant } from "../components/StatusBadge";
import { useI18n } from "../i18n/LocaleProvider";

// Windows Event Log channels (Faz 05, Security deliberately excluded).
const WIN_CHANNELS = ["Application", "System", "Setup"] as const;

const sevVariant: Record<string, BadgeVariant> = {
  emerg: "error",
  alert: "error",
  crit: "error",
  err: "error",
  warning: "warn",
  notice: "info",
  info: "info",
  debug: "neutral",
  critical: "error",
  error: "error",
  unknown: "warn",
};

type TaskResult = { body?: unknown; output?: string };

export function LogsPage() {
  const { t, locale } = useI18n();
  const [nodes, setNodes] = useState<NodePage["nodes"]>([]);
  const [nodeId, setNodeId] = useState("");
  const [source, setSource] = useState<"journald" | "win-eventlog">("journald");
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [entries, setEntries] = useState<LogSearchResult["entries"] | null>(null);
  const [searchSource, setSearchSource] = useState<string>("");
  const [searching, setSearching] = useState(false);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const list = await apiFetch<NodePage>("/api/v1/nodes?limit=200");
        if (cancelled) return;
        setNodes(list.nodes);
        setError(null);
      } catch (err) {
        if (!cancelled) setError(isApiError(err) ? err.message : t("common.error.generic"));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [t]);

  // Poll the created task until it settles; then parse log entries.
  async function pollTask(taskId: string) {
    for (let i = 0; i < 40; i++) {
      await new Promise((res) => setTimeout(res, 1000));
      const task = await apiFetch<TaskRecord>(`/api/v1/tasks/${taskId}`);
      if (task.status === "succeeded" || task.status === "failed" || task.status === "cancelled" || task.status === "expired") {
        return task;
      }
    }
    return null;
  }

  async function runSearch(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    setNotice(null);
    setEntries(null);
    setSearchSource("");
    if (!nodeId) {
      setError(t("logs.nodeRequired"));
      return;
    }
    const form = new FormData(e.currentTarget);
    setSearching(true);
    try {
      let operation = "logs.search";
      const payload: Record<string, unknown> = {};
      if (source === "journald") {
        const unit = String(form.get("unit") ?? "").trim();
        const priority = String(form.get("priority") ?? "0");
        const since = String(form.get("since") ?? "");
        const until = String(form.get("until") ?? "");
        const contains = String(form.get("contains") ?? "");
        const limit = String(form.get("limit") ?? "200");
        if (unit) payload.unit = unit.endsWith(".service") ? unit : `${unit}.service`;
        if (Number(priority) > 0) payload.priority = Number(priority);
        if (since) payload.since = new Date(since).toISOString();
        if (until) payload.until = new Date(until).toISOString();
        if (contains) payload.contains = contains;
        if (Number(limit) !== 200) payload.limit = Number(limit);
      } else {
        operation = "windows.eventlog.read";
        payload.channel = String(form.get("channel") ?? "System");
        const level = String(form.get("winlevel") ?? "0");
        if (Number(level) > 0) payload.level = Number(level);
        payload.count = 200;
      }
      const created = await apiFetch<TaskRecord>("/api/v1/tasks", {
        method: "POST",
        body: JSON.stringify({ operation, node_id: nodeId, payload }),
      });
      setNotice(t("logs.taskCreated", { id: created.id }));
      const task = await pollTask(created.id);
      if (!task) {
        setError(t("logs.taskTimeout"));
        return;
      }
      if (task.status !== "succeeded") {
        setError(
          t("logs.taskFailed", {
            status: task.status,
            detail: task.error_category ? ` (${task.error_category})` : "",
          }),
        );
        return;
      }
      const body = (task.result ?? {}) as { entries?: LogSearchResult["entries"]; events?: unknown[]; source?: string };
      let list: LogSearchResult["entries"] = [];
      if (Array.isArray(body.entries)) {
        list = body.entries;
        setSearchSource(body.source ?? "journald");
      } else if (Array.isArray(body.events)) {
        // windows.eventlog.read → shared honest rendering.
        setSearchSource("wevtutil");
        list = (body.events as Array<Record<string, unknown>>).map((ev) => ({
          timestamp: String(ev.time_created ?? ""),
          severity: Number(ev.level ?? 0),
          severity_name: String(ev.level_name ?? "unknown"),
          unit: String(ev.provider ?? ""),
          identifier: String(ev.channel ?? ""),
          message: String(ev.message ?? ""),
        }));
      }
      setEntries(list);
      setNotice(t("logs.done", { n: String(list.length) }));
    } catch (err) {
      setError(isApiError(err) ? err.message : t("common.error.generic"));
    } finally {
      setSearching(false);
    }
  }

  const selectedNode = nodes.find((n) => n.id === nodeId);

  return (
    <div className="flex flex-col gap-4 p-6">
      <div>
        <p className="text-[11px] text-ink-soft">{t("nav.group.operations")}</p>
        <h1 className="text-xl font-semibold text-ink">{t("nav.item.logs")}</h1>
      </div>

      {error && (
        <p role="alert" className="rounded-lg bg-err-soft px-3 py-2 text-xs text-err">
          {error}
        </p>
      )}
      {notice && !error && (
        <p role="status" className="rounded-lg bg-ok-soft px-3 py-2 text-xs text-ok">
          {notice}
        </p>
      )}

      <form onSubmit={runSearch} className="rounded-xl border border-border bg-panel p-4" aria-label={t("logs.search")}>
        <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
          <select
            value={nodeId}
            onChange={(e) => {
              setNodeId(e.target.value);
              setEntries(null);
            }}
            required
            className="rounded-lg border border-border bg-surface px-2.5 py-1.5 text-xs text-ink focus-visible:outline-2 focus-visible:outline-accent"
            aria-label={t("logs.node")}
          >
            <option value="">{t("logs.selectNode")}</option>
            {nodes.map((n) => (
              <option key={n.id} value={n.id}>
                {n.display_name}
              </option>
            ))}
          </select>
          <select
            value={source}
            onChange={(e) => setSource(e.target.value as "journald" | "win-eventlog")}
            className="rounded-lg border border-border bg-surface px-2.5 py-1.5 text-xs text-ink focus-visible:outline-2 focus-visible:outline-accent"
            aria-label={t("logs.source")}
          >
            <option value="journald">{t("logs.sourceJournald")}</option>
            <option value="win-eventlog">{t("logs.sourceWindows")}</option>
          </select>
          {source === "journald" ? (
            <>
              <input
                name="unit"
                placeholder={t("logs.unit")}
                aria-label={t("logs.unit")}
                className="rounded-lg border border-border bg-surface px-2.5 py-1.5 text-xs text-ink focus-visible:outline-2 focus-visible:outline-accent"
              />
              <select name="priority" className="rounded-lg border border-border bg-surface px-2.5 py-1.5 text-xs text-ink focus-visible:outline-2 focus-visible:outline-accent" aria-label={t("logs.priority")}>
                <option value="0">{t("logs.priorityAny")}</option>
                <option value="3">{t("logs.priorityErr")}</option>
                <option value="4">{t("logs.priorityWarn")}</option>
                <option value="6">{t("logs.priorityInfo")}</option>
              </select>
              <input name="since" type="datetime-local" className="rounded-lg border border-border bg-surface px-2.5 py-1.5 text-xs text-ink focus-visible:outline-2 focus-visible:outline-accent" aria-label={t("logs.since")} />
              <input name="until" type="datetime-local" className="rounded-lg border border-border bg-surface px-2.5 py-1.5 text-xs text-ink focus-visible:outline-2 focus-visible:outline-accent" aria-label={t("logs.until")} />
              <input name="contains" maxLength={256} placeholder={t("logs.contains")} aria-label={t("logs.contains")} className="rounded-lg border border-border bg-surface px-2.5 py-1.5 text-xs text-ink focus-visible:outline-2 focus-visible:outline-accent" />
              <input name="limit" type="number" min="1" max="500" defaultValue={200} className="rounded-lg border border-border bg-surface px-2.5 py-1.5 text-xs text-ink focus-visible:outline-2 focus-visible:outline-accent" aria-label={t("logs.limit")} />
            </>
          ) : (
            <>
              <select name="channel" className="rounded-lg border border-border bg-surface px-2.5 py-1.5 text-xs text-ink focus-visible:outline-2 focus-visible:outline-accent" aria-label={t("logs.channel")}>
                {WIN_CHANNELS.map((c) => (
                  <option key={c} value={c}>
                    {c}
                  </option>
                ))}
              </select>
              <select name="winlevel" className="rounded-lg border border-border bg-surface px-2.5 py-1.5 text-xs text-ink focus-visible:outline-2 focus-visible:outline-accent" aria-label={t("logs.winLevel")}>
                <option value="0">{t("logs.priorityAny")}</option>
                <option value="1">Critical</option>
                <option value="2">Error</option>
                <option value="3">Warning</option>
                <option value="4">Information</option>
              </select>
            </>
          )}
          <button
            type="submit"
            disabled={searching}
            className="rounded-lg bg-accent px-3 py-1.5 text-xs font-semibold text-white hover:opacity-90 focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-50"
          >
            {searching ? t("logs.searching") : t("logs.search")}
          </button>
        </div>
        <p className="mt-2 text-[11px] text-ink-soft">{t("logs.redactionNote")}</p>
      </form>

      <section aria-labelledby="logs-results" className="rounded-xl border border-border bg-panel p-4">
        <h2 id="logs-results" className="text-sm font-semibold text-ink">
          {t("logs.resultsTitle")}
          {searchSource && <span className="ml-2 text-[11px] font-normal text-ink-soft">{searchSource}</span>}
        </h2>
        {searching && <p className="mt-2 text-xs text-ink-soft">{t("logs.searching")}</p>}
        {entries === null && !searching ? (
          <div className="mt-3">
            <EmptyState title={t("logs.emptyTitle")} description={t("logs.emptyHint")} />
          </div>
        ) : entries !== null && entries.length === 0 ? (
          <p className="mt-2 text-xs text-ink-soft">{t("logs.noResults")}</p>
        ) : entries !== null ? (
          <div className="mt-3 overflow-x-auto">
            <table className="w-full text-left text-xs">
              <thead>
                <tr className="border-b border-border text-ink-soft">
                  <th scope="col" className="py-1.5 pr-3 font-medium">
                    {t("logs.colTime")}
                  </th>
                  <th scope="col" className="py-1.5 pr-3 font-medium">
                    {t("logs.colSeverity")}
                  </th>
                  <th scope="col" className="py-1.5 pr-3 font-medium">
                    {t("logs.colUnit")}
                  </th>
                  <th scope="col" className="py-1.5 font-medium">
                    {t("logs.colMessage")}
                  </th>
                </tr>
              </thead>
              <tbody>
                {entries.map((e, i) => (
                  <tr key={i} className="border-b border-border/50 align-top">
                    <td className="whitespace-nowrap py-1.5 pr-3 text-ink-soft">
                      {e.timestamp ? new Date(e.timestamp).toLocaleString(locale === "tr" ? "tr-TR" : "en-US") : "—"}
                    </td>
                    <td className="py-1.5 pr-3">
                      <StatusBadge variant={sevVariant[e.severity_name] ?? "warn"}>{e.severity_name || "unknown"}</StatusBadge>
                    </td>
                    <td className="py-1.5 pr-3 text-ink-soft">{e.unit || e.identifier || "—"}</td>
                    <td className="py-1.5 text-ink">
                      <span className="break-all">{e.message}</span>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : null}
        {selectedNode && entries !== null && (
          <p className="mt-2 text-[11px] text-ink-soft">
            {t("logs.correlationHint", { node: selectedNode.display_name })}
          </p>
        )}
      </section>
    </div>
  );
}

// TaskResult re-export keeps the type contract visible for tests.
export type { TaskResult };
