import { useCallback, useEffect, useState } from "react";
import { apiFetch, isApiError } from "../api/client";
import type { NodePage, TaskRecord, UpdateArtifact } from "../api/client";
import { EmptyState } from "../components/EmptyState";
import { StatusBadge } from "../components/StatusBadge";
import type { BadgeVariant } from "../components/StatusBadge";
import { useI18n } from "../i18n/LocaleProvider";

const stateVariant: Record<UpdateArtifact["state"], BadgeVariant> = {
  published: "ok",
  revoked: "error",
};

function formatBytes(n: number): string {
  const units = ["B", "KiB", "MiB", "GiB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v >= 10 || i === 0 ? 0 : 1)} ${units[i]}`;
}

// waitForTask polls one task until a terminal status (bounded).
async function waitForTask(id: string, timeoutMs = 180_000): Promise<TaskRecord | null> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const task = await apiFetch<TaskRecord>(`/api/v1/tasks/${encodeURIComponent(id)}`);
      if (["succeeded", "failed", "cancelled", "expired", "rejected"].includes(task.status)) {
        return task;
      }
    } catch {
      return null;
    }
    await new Promise((r) => setTimeout(r, 1500));
  }
  return null;
}

// UpdatesPage — Faz 15 imzalı güncelleme hattı (ADR-0020): sürüm artifact
// kayıt defteri (imza CP'de fail-closed doğrulanır) + node update akışı
// (stage → health → aktivasyon; rollback her zaman açık).
export function UpdatesPage() {
  const { t } = useI18n();
  const [rows, setRows] = useState<UpdateArtifact[] | null>(null);
  const [trustEnabled, setTrustEnabled] = useState(true);
  const [nodes, setNodes] = useState<NodePage["nodes"]>([]);
  const [selectedNode, setSelectedNode] = useState("");
  const [manifest, setManifest] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [reloadKey, setReloadKey] = useState(0);

  const reload = useCallback(() => setReloadKey((k) => k + 1), []);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const [arts, nodeList] = await Promise.all([
          apiFetch<{ artifacts: UpdateArtifact[]; trust_enabled: boolean }>("/api/v1/update-artifacts"),
          apiFetch<NodePage>("/api/v1/nodes?limit=200"),
        ]);
        if (cancelled) return;
        setRows(arts.artifacts ?? []);
        setTrustEnabled(arts.trust_enabled);
        setNodes(nodeList.nodes);
        setError(null);
      } catch (err) {
        if (!cancelled) setError(isApiError(err) ? err.message : t("common.error.generic"));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [reloadKey, t]);

  async function publish() {
    setBusy("publish");
    setError(null);
    setNotice(null);
    try {
      const parsed = JSON.parse(manifest);
      await apiFetch("/api/v1/update-artifacts", { method: "POST", body: JSON.stringify({ manifest: parsed }) });
      setNotice(t("updates.published"));
      setManifest("");
      reload();
    } catch (err) {
      setError(isApiError(err) ? err.message : t("common.error.generic"));
    } finally {
      setBusy(null);
    }
  }

  async function revoke(id: string) {
    setBusy(`revoke:${id}`);
    setError(null);
    setNotice(null);
    try {
      await apiFetch(`/api/v1/update-artifacts/${encodeURIComponent(id)}/revoke`, { method: "POST", body: JSON.stringify({}) });
      setNotice(t("updates.revoked"));
      reload();
    } catch (err) {
      setError(isApiError(err) ? err.message : t("common.error.generic"));
    } finally {
      setBusy(null);
    }
  }

  // runUpdateTask posts a node update task and polls to completion.
  async function runUpdateTask(op: string, payload: Record<string, unknown>, label: string) {
    setError(null);
    setNotice(null);
    setBusy(label);
    try {
      const created = await apiFetch<TaskRecord>("/api/v1/tasks", {
        method: "POST",
        body: JSON.stringify({ operation: op, node_id: selectedNode, payload }),
      });
      const done = await waitForTask(created.id);
      if (done?.status === "succeeded" && done.result) {
        setNotice(JSON.stringify(done.result));
      } else if (done?.status === "failed") {
        setError(t("updates.taskFailed"));
      } else {
        setNotice(t("updates.taskPending"));
      }
      reload();
    } catch (err) {
      setError(isApiError(err) ? err.message : t("common.error.generic"));
    } finally {
      setBusy(null);
    }
  }

  const loading = rows === null && error === null;

  return (
    <div className="flex flex-col gap-4 p-6">
      <div className="flex items-center justify-between">
        <div>
          <p className="text-[11px] text-ink-soft">{t("nav.group.settings")}</p>
          <h1 className="text-xl font-semibold text-ink">{t("nav.item.updates")}</h1>
        </div>
        <button
          type="button"
          onClick={reload}
          className="rounded-lg border border-border bg-panel px-3 py-1.5 text-xs font-medium text-ink hover:bg-neutral-soft focus-visible:outline-2 focus-visible:outline-accent"
        >
          {t("monitoring.refresh")}
        </button>
      </div>

      <p className="rounded-lg border border-border bg-panel px-3 py-2 text-[11px] text-ink-soft">
        {t("updates.policyNote")}
      </p>

      {!trustEnabled && (
        <p role="status" className="rounded-lg bg-warn-soft px-3 py-2 text-xs text-ink">
          {t("updates.trustDisabled")}
        </p>
      )}

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

      <section aria-labelledby="upd-publish" className="rounded-xl border border-border bg-panel p-4">
        <h2 id="upd-publish" className="text-sm font-semibold text-ink">
          {t("updates.publishTitle")}
        </h2>
        <p className="mt-1 text-[11px] text-ink-soft">{t("updates.publishNote")}</p>
        <textarea
          aria-label={t("updates.manifestLabel")}
          value={manifest}
          onChange={(e) => setManifest(e.target.value)}
          rows={6}
          spellCheck={false}
          className="mt-2 w-full rounded-lg border border-border bg-surface p-2 font-mono text-[11px] text-ink focus-visible:outline-2 focus-visible:outline-accent"
          placeholder='{ "manifest_version": "SCP-UPDATE-MANIFEST-V1", ... }'
        />
        <button
          type="button"
          disabled={busy !== null || manifest.trim() === ""}
          onClick={() => void publish()}
          className="mt-2 rounded-lg bg-accent px-3 py-1.5 text-xs font-semibold text-white hover:opacity-90 focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-50"
        >
          {busy === "publish" ? t("updates.publishing") : t("updates.publish")}
        </button>
      </section>

      <section aria-labelledby="upd-list" className="rounded-xl border border-border bg-panel p-4">
        <h2 id="upd-list" className="text-sm font-semibold text-ink">
          {t("updates.listTitle")}
        </h2>
        {loading ? (
          <p className="mt-2 text-xs text-ink-soft">{t("common.loading")}</p>
        ) : (rows ?? []).length === 0 ? (
          <div className="mt-3">
            <EmptyState title={t("updates.none")} description={t("updates.noneHint")} />
          </div>
        ) : (
          <ul className="mt-3 flex flex-col gap-2">
            {(rows ?? []).map((a) => (
              <li key={a.id} className="flex flex-wrap items-center gap-2 rounded-lg border border-border p-3">
                <StatusBadge variant={stateVariant[a.state]}>{t(`updates.state.${a.state}`)}</StatusBadge>
                <span className="font-mono text-[11px] text-ink">{a.target}</span>
                <span className="text-sm font-semibold text-ink">v{a.version}</span>
                <span className="text-[11px] text-ink-soft">{a.file_name}</span>
                <span className="text-[11px] text-ink-soft">{formatBytes(a.size_bytes)}</span>
                <span className="font-mono text-[11px] text-ink-soft">{a.sha256.slice(0, 12)}…</span>
                {a.notes && <span className="text-[11px] text-ink-soft">{a.notes}</span>}
                {a.state === "published" && (
                  <button
                    type="button"
                    disabled={busy !== null}
                    onClick={() => {
                      if (window.confirm(t("updates.revokeConfirm"))) {
                        void revoke(a.id);
                      }
                    }}
                    className="ml-auto rounded-lg border border-err/40 bg-panel px-2.5 py-1 text-[11px] font-medium text-err hover:bg-err-soft focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-50"
                  >
                    {t("updates.revoke")}
                  </button>
                )}
              </li>
            ))}
          </ul>
        )}
      </section>

      <section aria-labelledby="upd-node" className="rounded-xl border border-border bg-panel p-4">
        <h2 id="upd-node" className="text-sm font-semibold text-ink">
          {t("updates.nodeTitle")}
        </h2>
        <p className="mt-1 text-[11px] text-ink-soft">{t("updates.nodeNote")}</p>
        <div className="mt-3 flex flex-wrap items-center gap-2">
          <select
            aria-label={t("updates.nodeSelect")}
            value={selectedNode}
            onChange={(e) => setSelectedNode(e.target.value)}
            className="rounded-lg border border-border bg-surface px-2.5 py-1.5 text-xs text-ink focus-visible:outline-2 focus-visible:outline-accent"
          >
            <option value="">{t("updates.selectNode")}</option>
            {nodes.map((n) => (
              <option key={n.id} value={n.id}>
                {n.display_name}
              </option>
            ))}
          </select>
          <button
            type="button"
            disabled={busy !== null || !selectedNode}
            onClick={() => void runUpdateTask("update.status", {}, "status")}
            className="rounded-lg border border-border bg-panel px-2.5 py-1 text-xs font-medium text-ink hover:bg-neutral-soft focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-50"
          >
            {t("updates.statusBtn")}
          </button>
          <button
            type="button"
            disabled={busy !== null || !selectedNode}
            onClick={() => {
              if (window.confirm(t("updates.rollbackConfirm"))) {
                void runUpdateTask("update.rollback", {}, "rollback");
              }
            }}
            className="rounded-lg border border-border bg-panel px-2.5 py-1 text-xs font-medium text-ink hover:bg-neutral-soft focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-50"
          >
            {t("updates.rollbackBtn")}
          </button>
        </div>
        <p className="mt-2 text-[11px] text-ink-soft">{t("updates.stageNote")}</p>
      </section>
    </div>
  );
}
