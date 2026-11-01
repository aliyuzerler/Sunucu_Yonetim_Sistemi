import { useCallback, useEffect, useState } from "react";
import type { FormEvent } from "react";
import { apiFetch, isApiError } from "../api/client";
import type { NodePage, OperationInfo, TaskPage, TaskRecord } from "../api/client";
import { EmptyState } from "../components/EmptyState";
import { StatusBadge } from "../components/StatusBadge";
import type { BadgeVariant } from "../components/StatusBadge";
import { useI18n } from "../i18n/LocaleProvider";

const statusVariant: Record<TaskRecord["status"], BadgeVariant> = {
  queued: "info",
  dispatched: "info",
  accepted: "info",
  running: "info",
  succeeded: "ok",
  failed: "error",
  cancelled: "neutral",
  expired: "warn",
  unknown: "warn",
  partial: "warn",
};

export function TasksPage() {
  const { t } = useI18n();
  const [page, setPage] = useState<TaskPage | null>(null);
  const [ops, setOps] = useState<OperationInfo[]>([]);
  const [nodes, setNodes] = useState<{ id: string; display_name: string }[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [reloadKey, setReloadKey] = useState(0);

  const reload = useCallback(() => setReloadKey((k) => k + 1), []);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const [tasks, operations, nodeList] = await Promise.all([
          apiFetch<TaskPage>("/api/v1/tasks?limit=100"),
          apiFetch<{ operations: OperationInfo[] }>("/api/v1/task-operations"),
          apiFetch<NodePage>("/api/v1/nodes?limit=200"),
        ]);
        if (cancelled) return;
        setPage(tasks);
        setOps(operations.operations);
        setNodes(nodeList.nodes.map((n) => ({ id: n.id, display_name: n.display_name })));
        setError(null);
      } catch (err) {
        if (!cancelled) {
          setError(isApiError(err) ? err.message : t("common.error.generic"));
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [reloadKey, t]);

  const loading = page === null && error === null;

  async function createTask(payload: {
    operation: string;
    node_id: string;
    payload_json: string;
    idempotency_key: string;
  }): Promise<boolean> {
    try {
      const body: Record<string, unknown> = { operation: payload.operation };
      if (payload.node_id) body.node_id = payload.node_id;
      if (payload.payload_json.trim()) body.payload = JSON.parse(payload.payload_json);
      if (payload.idempotency_key.trim()) body.idempotency_key = payload.idempotency_key.trim();
      await apiFetch("/api/v1/tasks", { method: "POST", body: JSON.stringify(body) });
      setNotice(t("tasks.createdNotice"));
      reload();
      return true;
    } catch (err) {
      setNotice(null);
      setError(isApiError(err) ? err.message : t("common.error.generic"));
      return false;
    }
  }

  async function cancelTask(id: string) {
    setError(null);
    setNotice(null);
    try {
      await apiFetch(`/api/v1/tasks/${id}/cancel`, { method: "POST" });
      setNotice(t("tasks.cancelledNotice"));
      reload();
    } catch (err) {
      setError(isApiError(err) ? err.message : t("common.error.generic"));
    }
  }

  return (
    <div className="flex flex-col gap-4 p-6">
      <div className="flex items-center justify-between">
        <div>
          <p className="text-[11px] text-ink-soft">{t("nav.group.operations")}</p>
          <h1 className="text-xl font-semibold text-ink">{t("nav.item.tasks")}</h1>
        </div>
        <button
          type="button"
          onClick={reload}
          className="rounded-lg border border-border bg-panel px-3 py-1.5 text-xs font-medium text-ink hover:bg-neutral-soft focus-visible:outline-2 focus-visible:outline-accent"
        >
          {t("tasks.refresh")}
        </button>
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

      <TaskCreateForm ops={ops} nodes={nodes} onCreate={createTask} />

      <section aria-label={t("nav.item.tasks")} className="rounded-xl border border-border bg-panel shadow-sm">
        {loading ? (
          <p className="p-6 text-sm text-ink-soft">{t("common.loading")}</p>
        ) : !page || page.tasks.length === 0 ? (
          <EmptyState
            title={t("tasks.empty.title")}
            description={t("tasks.empty.description")}
          />
        ) : (
          <table className="w-full text-left text-sm">
            <caption className="sr-only">{t("tasks.table.caption")}</caption>
            <thead>
              <tr className="border-b border-border text-xs uppercase tracking-wide text-ink-soft">
                <th scope="col" className="px-4 py-2 font-medium">{t("tasks.col.operation")}</th>
                <th scope="col" className="px-4 py-2 font-medium">{t("tasks.col.status")}</th>
                <th scope="col" className="px-4 py-2 font-medium">{t("tasks.col.actor")}</th>
                <th scope="col" className="px-4 py-2 font-medium">{t("tasks.col.correlation")}</th>
                <th scope="col" className="px-4 py-2 font-medium"><span className="sr-only">{t("tasks.col.actions")}</span></th>
              </tr>
            </thead>
            <tbody>
              {page.tasks.map((task) => (
                <tr key={task.id} className="border-b border-border/60 last:border-0">
                  <td className="px-4 py-2.5">
                    <p className="font-mono text-xs text-ink">{task.operation}</p>
                    {task.error_category && (
                      <p className="text-[11px] text-err">{t("tasks.errorCategory")}: {task.error_category}</p>
                    )}
                  </td>
                  <td className="px-4 py-2.5">
                    <StatusBadge variant={statusVariant[task.status]}>{t(`tasks.status.${task.status}` as TranslationTaskStatus)}</StatusBadge>
                    <span className="ml-2 text-[11px] text-ink-soft">{task.attempts}/{task.max_attempts}</span>
                  </td>
                  <td className="px-4 py-2.5 text-ink-soft">{task.actor}</td>
                  <td className="px-4 py-2.5">
                    <code className="text-[11px] text-ink-soft">{task.correlation_id.slice(0, 12)}</code>
                  </td>
                  <td className="px-4 py-2.5 text-right">
                    {task.status === "queued" && (
                      <button
                        type="button"
                        onClick={() => cancelTask(task.id)}
                        className="rounded-lg border border-border px-2 py-1 text-xs font-medium text-ink hover:bg-neutral-soft focus-visible:outline-2 focus-visible:outline-accent"
                      >
                        {t("tasks.cancel")}
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      <p className="text-[11px] text-ink-soft">{t("tasks.workerNote")}</p>
    </div>
  );
}

type TranslationTaskStatus = `tasks.status.${TaskRecord["status"]}`;

function TaskCreateForm({
  ops,
  nodes,
  onCreate,
}: {
  ops: OperationInfo[];
  nodes: { id: string; display_name: string }[];
  onCreate: (payload: {
    operation: string;
    node_id: string;
    payload_json: string;
    idempotency_key: string;
  }) => Promise<boolean>;
}) {
  const { t } = useI18n();
  const [operation, setOperation] = useState("");
  const [nodeId, setNodeId] = useState("");
  const [payloadJson, setPayloadJson] = useState("{}");
  const [idempotencyKey, setIdempotencyKey] = useState("");
  const [pending, setPending] = useState(false);

  // Varsayılan operasyon türetilir (effect yerine render sırasında).
  const effectiveOperation = operation || ops[0]?.name || "";

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (pending || !effectiveOperation) return;
    try {
      if (payloadJson.trim()) JSON.parse(payloadJson);
    } catch {
      return;
    }
    setPending(true);
    const ok = await onCreate({ operation: effectiveOperation, node_id: nodeId, payload_json: payloadJson, idempotency_key: idempotencyKey });
    setPending(false);
    if (ok) {
      setIdempotencyKey("");
    }
  }

  return (
    <form onSubmit={onSubmit} className="flex flex-col gap-3 rounded-xl border border-border bg-panel p-4 shadow-sm">
      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        <label className="flex flex-col gap-1">
          <span className="text-xs font-medium text-ink">{t("tasks.form.operation")}</span>
          <select
            value={effectiveOperation}
            onChange={(e) => setOperation(e.target.value)}
            className="rounded-lg border border-border bg-surface px-3 py-2 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
          >
            {ops.map((op) => (
              <option key={op.name} value={op.name}>
                {op.name} · v{op.version}
              </option>
            ))}
          </select>
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-xs font-medium text-ink">{t("tasks.form.node")}</span>
          <select
            value={nodeId}
            onChange={(e) => setNodeId(e.target.value)}
            className="rounded-lg border border-border bg-surface px-3 py-2 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
          >
            <option value="">{t("tasks.form.noNode")}</option>
            {nodes.map((n) => (
              <option key={n.id} value={n.id}>{n.display_name}</option>
            ))}
          </select>
        </label>
      </div>
      <label className="flex flex-col gap-1">
        <span className="text-xs font-medium text-ink">{t("tasks.form.payload")}</span>
        <textarea
          value={payloadJson}
          onChange={(e) => setPayloadJson(e.target.value)}
          rows={3}
          spellCheck={false}
          className="rounded-lg border border-border bg-surface px-3 py-2 font-mono text-xs text-ink focus-visible:outline-2 focus-visible:outline-accent"
        />
      </label>
      <label className="flex flex-col gap-1">
        <span className="text-xs font-medium text-ink">{t("tasks.form.idempotency")}</span>
        <input
          value={idempotencyKey}
          onChange={(e) => setIdempotencyKey(e.target.value)}
          minLength={8}
          maxLength={128}
          placeholder={t("tasks.form.idempotencyHint")}
          className="rounded-lg border border-border bg-surface px-3 py-2 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
        />
      </label>
      <div>
        <button
          type="submit"
          disabled={pending || !effectiveOperation}
          className="rounded-lg bg-accent px-4 py-2 text-sm font-medium text-white hover:bg-accent/90 focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-50"
        >
          {pending ? t("common.saving") : t("tasks.form.submit")}
        </button>
      </div>
    </form>
  );
}
