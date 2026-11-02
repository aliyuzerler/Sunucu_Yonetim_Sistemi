import { useCallback, useEffect, useState } from "react";
import { apiFetch, isApiError } from "../api/client";
import type { BackupRecord, NodePage, RestoreDrill, TaskRecord } from "../api/client";
import { EmptyState } from "../components/EmptyState";
import { StatusBadge } from "../components/StatusBadge";
import type { BadgeVariant } from "../components/StatusBadge";
import { useI18n } from "../i18n/LocaleProvider";

const statusVariant: Record<BackupRecord["status"], BadgeVariant> = {
  running: "warn",
  success: "ok",
  failed: "error",
};

const verifyVariant: Record<BackupRecord["verify_state"], BadgeVariant> = {
  unverified: "neutral",
  verified: "ok",
  failed: "error",
};

const drillVariant: Record<RestoreDrill["status"], BadgeVariant> = {
  running: "warn",
  success: "ok",
  failed: "error",
};

function formatBytes(n: number, lang: "tr" | "en"): string {
  if (n <= 0) return "0 B";
  const units = ["B", "KiB", "MiB", "GiB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v >= 10 || i === 0 ? 0 : 1)} ${units[i]}` + (lang === "tr" ? "" : "");
}

// waitForTask polls one task until a terminal status (bounded); used to give
// the operator immediate feedback after triggering backup operations.
async function waitForTask(id: string, timeoutMs = 120_000): Promise<TaskRecord | null> {
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

export function BackupsPage() {
  const { t, locale } = useI18n();
  const [cpBackups, setCpBackups] = useState<BackupRecord[] | null>(null);
  const [nodeBackups, setNodeBackups] = useState<BackupRecord[] | null>(null);
  const [drills, setDrills] = useState<RestoreDrill[] | null>(null);
  const [nodes, setNodes] = useState<NodePage["nodes"]>([]);
  const [selectedNode, setSelectedNode] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [reloadKey, setReloadKey] = useState(0);

  const reload = useCallback(() => setReloadKey((k) => k + 1), []);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const [cp, nodeList] = await Promise.all([
          apiFetch<{ backups: BackupRecord[] }>("/api/v1/backups?scope=control_plane&limit=50"),
          apiFetch<NodePage>("/api/v1/nodes?limit=200"),
        ]);
        if (cancelled) return;
        setCpBackups(cp.backups);
        setNodes(nodeList.nodes);
        setError(null);
        const nodeFilter = selectedNode ? `&node_id=${encodeURIComponent(selectedNode)}` : "";
        const nb = await apiFetch<{ backups: BackupRecord[] }>(`/api/v1/backups?scope=node&limit=50${nodeFilter}`);
        const dr = await apiFetch<{ drills: RestoreDrill[] }>(`/api/v1/restore-drills?limit=30${nodeFilter}`);
        if (cancelled) return;
        setNodeBackups(nb.backups);
        setDrills(dr.drills);
      } catch (err) {
        if (!cancelled) setError(isApiError(err) ? err.message : t("common.error.generic"));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [reloadKey, selectedNode, t]);

  // runBackupTask posts a typed task and polls it to completion.
  async function runBackupTask(op: string, payload: Record<string, unknown>, label: string) {
    setError(null);
    setNotice(null);
    setBusy(label);
    try {
      const created = await apiFetch<TaskRecord>("/api/v1/tasks", {
        method: "POST",
        body: JSON.stringify({ operation: op, payload }),
      });
      const done = await waitForTask(created.id);
      if (done?.status === "succeeded") {
        setNotice(t("backup.taskDone"));
      } else if (done?.status === "failed") {
        setError(t("backup.taskFailed"));
      } else {
        setNotice(t("backup.taskPending"));
      }
      reload();
    } catch (err) {
      setError(isApiError(err) ? err.message : t("common.error.generic"));
    } finally {
      setBusy(null);
    }
  }

  const loading = cpBackups === null && error === null;

  return (
    <div className="flex flex-col gap-4 p-6">
      <div className="flex items-center justify-between">
        <div>
          <p className="text-[11px] text-ink-soft">{t("nav.group.operations")}</p>
          <h1 className="text-xl font-semibold text-ink">{t("nav.item.backups")}</h1>
        </div>
        <div className="flex gap-2">
          <button
            type="button"
            disabled={busy !== null}
            onClick={() => void runBackupTask("backup.cp.run", {}, "cp.run")}
            className="rounded-lg bg-accent px-3 py-1.5 text-xs font-semibold text-white hover:opacity-90 focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-50"
          >
            {busy === "cp.run" ? t("backup.running") : t("backup.cpRun")}
          </button>
          <button
            type="button"
            onClick={reload}
            className="rounded-lg border border-border bg-panel px-3 py-1.5 text-xs font-medium text-ink hover:bg-neutral-soft focus-visible:outline-2 focus-visible:outline-accent"
          >
            {t("monitoring.refresh")}
          </button>
        </div>
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

      {/* Kontrol paneli yedeği — node veri yedeğinden AYRI kapsamdır (kriter 1). */}
      <section aria-labelledby="backup-cp" className="rounded-xl border border-border bg-panel p-4">
        <h2 id="backup-cp" className="text-sm font-semibold text-ink">
          {t("backup.cpTitle")}
        </h2>
        <p className="mt-1 text-[11px] text-ink-soft">{t("backup.cpNote")}</p>
        {loading ? (
          <p className="mt-2 text-xs text-ink-soft">{t("common.loading")}</p>
        ) : (cpBackups ?? []).length === 0 ? (
          <div className="mt-3">
            <EmptyState title={t("backup.noCp")} description={t("backup.noCpHint")} />
          </div>
        ) : (
          <ul className="mt-3 flex flex-col gap-2">
            {(cpBackups ?? []).map((b) => (
              <li key={b.id} className="rounded-lg border border-border p-3">
                <div className="flex flex-wrap items-center gap-2">
                  <StatusBadge variant={statusVariant[b.status]}>{t(`backup.status.${b.status}`)}</StatusBadge>
                  <StatusBadge variant={verifyVariant[b.verify_state]}>{t(`backup.verify.${b.verify_state}`)}</StatusBadge>
                  <span className="text-xs text-ink-soft">{formatBytes(b.size_bytes, locale)}</span>
                  <span className="text-[11px] text-ink-soft">{new Date(b.created_at).toLocaleString(locale === "tr" ? "tr-TR" : "en-US")}</span>
                  {b.sha256 && <span className="font-mono text-[11px] text-ink-soft">{b.sha256.slice(0, 12)}…</span>}
                </div>
                {b.verify_detail && <p className="mt-1 text-[11px] text-ink-soft">{b.verify_detail}</p>}
                <div className="mt-2 flex flex-wrap gap-2">
                  <button
                    type="button"
                    disabled={busy !== null}
                    onClick={() => void runBackupTask("backup.cp.verify", { backup_id: b.id }, `verify:${b.id}`)}
                    className="rounded-lg border border-border bg-panel px-2.5 py-1 text-xs font-medium text-ink hover:bg-neutral-soft focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-50"
                  >
                    {t("backup.verifyAgain")}
                  </button>
                  <button
                    type="button"
                    disabled={busy !== null || b.status !== "success"}
                    onClick={() => void runBackupTask("backup.cp.restore_drill", { backup_id: b.id }, `drill:${b.id}`)}
                    className="rounded-lg border border-border bg-panel px-2.5 py-1 text-xs font-medium text-ink hover:bg-neutral-soft focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-50"
                  >
                    {busy === `drill:${b.id}` ? t("backup.running") : t("backup.cpDrill")}
                  </button>
                </div>
              </li>
            ))}
          </ul>
        )}
      </section>

      {/* Node veri yedeği — restic; kapalı aile dürüst unsupported döner. */}
      <section aria-labelledby="backup-node" className="rounded-xl border border-border bg-panel p-4">
        <h2 id="backup-node" className="text-sm font-semibold text-ink">
          {t("backup.nodeTitle")}
        </h2>
        <p className="mt-1 text-[11px] text-ink-soft">{t("backup.nodeNote")}</p>
        <div className="mt-3 flex flex-wrap items-center gap-2">
          <select
            aria-label={t("backup.nodeSelect")}
            value={selectedNode}
            onChange={(e) => setSelectedNode(e.target.value)}
            className="rounded-lg border border-border bg-surface px-2.5 py-1.5 text-xs text-ink focus-visible:outline-2 focus-visible:outline-accent"
          >
            <option value="">{t("backup.allNodes")}</option>
            {nodes.map((n) => (
              <option key={n.id} value={n.id}>
                {n.display_name}
              </option>
            ))}
          </select>
          <button
            type="button"
            disabled={busy !== null || !selectedNode}
            onClick={() => void runBackupTask("backup.repo.init", {}, "node.init")}
            className="rounded-lg border border-border bg-panel px-2.5 py-1 text-xs font-medium text-ink hover:bg-neutral-soft focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-50"
          >
            {t("backup.repoInit")}
          </button>
          <button
            type="button"
            disabled={busy !== null || !selectedNode}
            onClick={() => void runBackupTask("backup.run", {}, "node.run")}
            className="rounded-lg bg-accent px-3 py-1.5 text-xs font-semibold text-white hover:opacity-90 focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-50"
          >
            {busy === "node.run" ? t("backup.running") : t("backup.nodeRun")}
          </button>
        </div>
        {(nodeBackups ?? []).length === 0 ? (
          <div className="mt-3">
            <EmptyState title={t("backup.noNode")} description={t("backup.noNodeHint")} />
          </div>
        ) : (
          <ul className="mt-3 flex flex-col gap-2">
            {(nodeBackups ?? []).map((b) => (
              <li key={b.id} className="rounded-lg border border-border p-3">
                <div className="flex flex-wrap items-center gap-2">
                  <StatusBadge variant={statusVariant[b.status]}>{t(`backup.status.${b.status}`)}</StatusBadge>
                  <StatusBadge variant={verifyVariant[b.verify_state]}>{t(`backup.verify.${b.verify_state}`)}</StatusBadge>
                  {b.snapshot_id && <span className="font-mono text-[11px] text-ink-soft">{t("backup.snapshot")}: {b.snapshot_id}</span>}
                  <span className="text-xs text-ink-soft">{formatBytes(b.size_bytes, locale)}</span>
                  <span className="text-[11px] text-ink-soft">{new Date(b.created_at).toLocaleString(locale === "tr" ? "tr-TR" : "en-US")}</span>
                </div>
                <div className="mt-2 flex flex-wrap gap-2">
                  <button
                    type="button"
                    disabled={busy !== null || !b.snapshot_id}
                    onClick={() => void runBackupTask("backup.verify", { backup_id: b.id }, `nverify:${b.id}`)}
                    className="rounded-lg border border-border bg-panel px-2.5 py-1 text-xs font-medium text-ink hover:bg-neutral-soft focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-50"
                  >
                    {t("backup.nodeVerify")}
                  </button>
                  <button
                    type="button"
                    disabled={busy !== null || !b.snapshot_id}
                    onClick={() => void runBackupTask("backup.restore", { backup_id: b.id, snapshot: b.snapshot_id }, `nrestore:${b.id}`)}
                    className="rounded-lg border border-border bg-panel px-2.5 py-1 text-xs font-medium text-ink hover:bg-neutral-soft focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-50"
                  >
                    {busy === `nrestore:${b.id}` ? t("backup.running") : t("backup.nodeRestore")}
                  </button>
                </div>
              </li>
            ))}
          </ul>
        )}
      </section>

      {/* Restore tatbikatları — izole hedef kayıtları (kriter 2). */}
      <section aria-labelledby="backup-drills" className="rounded-xl border border-border bg-panel p-4">
        <h2 id="backup-drills" className="text-sm font-semibold text-ink">
          {t("backup.drillsTitle")}
        </h2>
        <p className="mt-1 text-[11px] text-ink-soft">{t("backup.drillsNote")}</p>
        {(drills ?? []).length === 0 ? (
          <p className="mt-2 text-xs text-ink-soft">{t("backup.noDrills")}</p>
        ) : (
          <ul className="mt-3 flex flex-col gap-2">
            {(drills ?? []).map((d) => (
              <li key={d.id} className="flex flex-wrap items-center gap-2 rounded-lg border border-border px-3 py-2">
                <StatusBadge variant={drillVariant[d.status]}>{t(`backup.status.${d.status}`)}</StatusBadge>
                <span className="text-[11px] text-ink-soft">{new Date(d.created_at).toLocaleString(locale === "tr" ? "tr-TR" : "en-US")}</span>
                <span className="text-[11px] text-ink-soft">{d.target}</span>
                {d.detail && <span className="text-[11px] text-ink-soft">{d.detail}</span>}
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}
