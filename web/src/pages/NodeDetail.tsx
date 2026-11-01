import { useCallback, useEffect, useState } from "react";
import type { FormEvent } from "react";
import { apiFetch, isApiError } from "../api/client";
import type {
  CapabilityRecord,
  MetricsSnapshot,
  NodeRecord,
  SystemFactsRecord,
  TaskRecord,
} from "../api/client";
import { StatusBadge } from "../components/StatusBadge";
import type { BadgeVariant } from "../components/StatusBadge";
import { useI18n } from "../i18n/LocaleProvider";

// Faz 04 unit allowlist — istemci tarafında da uygulanır; gerçek kural
// sunucu + agent + broker tarafında zorlanır (gizleme yetkilendirme değildir).
const UNIT_PATTERN = /^[A-Za-z0-9][A-Za-z0-9@._-]{0,246}\.service$/;

const SERVICE_OPERATIONS = [
  "linux.systemd.service.list",
  "linux.systemd.service.status",
  "linux.systemd.service.start",
  "linux.systemd.service.stop",
  "linux.systemd.service.restart",
] as const;

type ServiceOp = (typeof SERVICE_OPERATIONS)[number];

function bytesLabel(n: number, locale: string): string {
  if (!Number.isFinite(n) || n <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  const i = Math.min(units.length - 1, Math.floor(Math.log(n) / Math.log(1024)));
  const v = n / Math.pow(1024, i);
  return `${new Intl.NumberFormat(locale === "tr" ? "tr-TR" : "en-US", { maximumFractionDigits: 1 }).format(v)} ${units[i]}`;
}

function percentLabel(n: number, locale: string): string {
  return `${new Intl.NumberFormat(locale === "tr" ? "tr-TR" : "en-US", { maximumFractionDigits: 1 }).format(n)}%`;
}

function uptimeLabel(seconds: number, t: ReturnType<typeof useI18n>["t"]): string {
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (d > 0) return t("nodedetail.uptime.days", { days: d, hours: h, minutes: m });
  if (h > 0) return t("nodedetail.uptime.hours", { hours: h, minutes: m });
  return t("nodedetail.uptime.minutes", { minutes: m });
}

/**
 * NodeDetail: tek node'un Faz 04 telemetri görünümü — agent'ın bildirdiği
 * sistem bilgileri, en yeni metrik anlık görüntüsü (kaynak + timestamp ile),
 * kabiliyet beyanları ve tipli servis işlemleri (start/stop/restart görevleri).
 * UI yalnızca kolaylıktır: yetki sunucu tarafında RBAC ile zorlanır.
 */
export function NodeDetail({ node, onBack }: { node: NodeRecord; onBack: () => void }) {
  const { t, locale } = useI18n();
  const [facts, setFacts] = useState<SystemFactsRecord | null>(null);
  const [metrics, setMetrics] = useState<MetricsSnapshot | null>(null);
  const [capabilities, setCapabilities] = useState<CapabilityRecord[]>([]);
  const [tasks, setTasks] = useState<TaskRecord[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [reloadKey, setReloadKey] = useState(0);
  const [unit, setUnit] = useState("");
  const [unitError, setUnitError] = useState<string | null>(null);
  const [pendingOp, setPendingOp] = useState<ServiceOp | null>(null);
  const [lastTaskId, setLastTaskId] = useState<string | null>(null);

  const reload = useCallback(() => setReloadKey((k) => k + 1), []);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const [f, m, caps] = await Promise.all([
          apiFetch<SystemFactsRecord>(`/api/v1/nodes/${node.id}/facts/system`).catch(() => null),
          apiFetch<MetricsSnapshot>(`/api/v1/nodes/${node.id}/metrics/latest`).catch(() => null),
          apiFetch<{ capabilities: CapabilityRecord[] }>(`/api/v1/nodes/${node.id}/capabilities`)
            .then((r) => r.capabilities ?? [])
            .catch(() => []),
        ]);
        if (cancelled) return;
        setFacts(f);
        setMetrics(m);
        setCapabilities(caps);
        setError(null);
      } catch (err) {
        if (!cancelled) setError(isApiError(err) ? err.message : t("common.error.generic"));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [node.id, reloadKey, t]);

  // Servis görevleri: son 10 görev, bu node ve servis operasyonları.
  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const page = await apiFetch<{ tasks: TaskRecord[] }>(
          `/api/v1/tasks?node_id=${encodeURIComponent(node.id)}&limit=20`,
        );
        if (cancelled) return;
        setTasks(page.tasks.filter((x) => SERVICE_OPERATIONS.includes(x.operation as ServiceOp)).slice(0, 10));
      } catch {
        // Görev listesi isteğe bağlıdır; hata görünümü bozmaz.
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [node.id, reloadKey]);

  const serviceCap = capabilities.find((c) => c.capability_id === "linux.systemd.service.restart");

  async function submitServiceOp(op: ServiceOp) {
    if (op !== "linux.systemd.service.list" && !UNIT_PATTERN.test(unit.trim())) {
      setUnitError(t("nodedetail.services.unitInvalid"));
      return;
    }
    setUnitError(null);
    setPendingOp(op);
    try {
      const created = await apiFetch<TaskRecord>("/api/v1/tasks", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          operation: op,
          node_id: node.id,
          payload: op === "linux.systemd.service.list" ? {} : { unit: unit.trim() },
        }),
      });
      setLastTaskId(created.id);
      setPendingOp(null);
      reload();
    } catch (err) {
      setPendingOp(null);
      setError(isApiError(err) ? err.message : t("common.error.generic"));
    }
  }

  function onUnitSubmit(e: FormEvent) {
    e.preventDefault();
    void submitServiceOp("linux.systemd.service.status");
  }

  const taskBadge: Record<TaskRecord["status"], BadgeVariant> = {
    queued: "info",
    dispatched: "info",
    accepted: "info",
    running: "info",
    succeeded: "ok",
    failed: "error",
    cancelled: "neutral",
    expired: "warn",
    unknown: "neutral",
    partial: "warn",
  };

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <div>
          <button
            type="button"
            onClick={onBack}
            className="text-xs text-accent hover:underline focus-visible:outline-2 focus-visible:outline-accent"
          >
            ← {t("nodedetail.back")}
          </button>
          <h2 className="text-lg font-semibold text-ink">{node.display_name}</h2>
          {node.hostname && <p className="text-[11px] text-ink-soft">{node.hostname}</p>}
        </div>
        <button
          type="button"
          onClick={reload}
          className="rounded-lg border border-border px-3 py-1.5 text-xs text-ink hover:bg-neutral-soft focus-visible:outline-2 focus-visible:outline-accent"
        >
          {t("nodedetail.refresh")}
        </button>
      </div>

      {error && (
        <p role="alert" className="rounded-lg bg-err-soft px-3 py-2 text-xs text-err">
          {error}
        </p>
      )}

      {/* Sistem bilgileri */}
      <section aria-label={t("nodedetail.facts.title")} className="rounded-xl border border-border bg-panel p-4 shadow-sm">
        <h3 className="text-sm font-semibold text-ink">{t("nodedetail.facts.title")}</h3>
        {!facts ? (
          <p className="mt-2 text-xs text-ink-soft">{t("nodedetail.facts.none")}</p>
        ) : (
          <dl className="mt-2 grid grid-cols-2 gap-x-4 gap-y-1.5 text-xs md:grid-cols-4">
            <Fact label={t("nodedetail.facts.distro")} value={facts.os_pretty_name || facts.os_id || "—"} />
            <Fact label={t("nodedetail.facts.version")} value={facts.os_version_id || "—"} />
            <Fact label={t("nodedetail.facts.kernel")} value={facts.kernel_release || "—"} />
            <Fact label={t("nodedetail.facts.arch")} value={facts.arch || "—"} />
            <Fact
              label={t("nodedetail.facts.systemd")}
              value={facts.systemd_available ? facts.systemd_version || "✓" : t("nodedetail.facts.systemdNo")}
            />
            <Fact label={t("nodedetail.facts.pkgManager")} value={facts.package_manager || "—"} />
            <Fact label={t("nodedetail.facts.source")} value={facts.source || "—"} />
            <Fact label={t("nodedetail.facts.collectedAt")} value={new Date(facts.collected_at).toLocaleString(locale === "tr" ? "tr-TR" : "en-US")} />
          </dl>
        )}
      </section>

      {/* Metrikler */}
      <section aria-label={t("nodedetail.metrics.title")} className="rounded-xl border border-border bg-panel p-4 shadow-sm">
        <h3 className="text-sm font-semibold text-ink">{t("nodedetail.metrics.title")}</h3>
        {!metrics ? (
          <p className="mt-2 text-xs text-ink-soft">{t("nodedetail.metrics.none")}</p>
        ) : (
          <div className="mt-2 flex flex-col gap-3 text-xs">
            <div className="grid grid-cols-2 gap-x-4 gap-y-1.5 md:grid-cols-4">
              <Metric
                label={t("nodedetail.metrics.cpu")}
                value={percentLabel(metrics.cpu.percent, locale)}
                hint={t("nodedetail.metrics.cores", { count: metrics.cpu.logical_cores })}
              />
              <Metric
                label={t("nodedetail.metrics.memory")}
                value={percentLabel(metrics.memory.used_percent, locale)}
                hint={`${bytesLabel(metrics.memory.used_bytes, locale)} / ${bytesLabel(metrics.memory.total_bytes, locale)}`}
              />
              <Metric label={t("nodedetail.metrics.load")} value={`${metrics.cpu.load_1m} · ${metrics.cpu.load_5m} · ${metrics.cpu.load_15m}`} />
              <Metric label={t("nodedetail.metrics.uptime")} value={uptimeLabel(metrics.uptime_seconds, t)} />
            </div>

            {metrics.disks.length > 0 && (
              <div>
                <p className="mb-1 font-medium text-ink">{t("nodedetail.metrics.disks")}</p>
                <ul className="flex flex-col gap-1">
                  {metrics.disks.map((d) => (
                    <li key={`${d.device}-${d.mount_point}`} className="flex items-center gap-2">
                      <span className="w-40 shrink-0 truncate text-ink-soft" title={d.mount_point}>
                        {d.device} → {d.mount_point}
                      </span>
                      <div
                        role="progressbar"
                        aria-label={`${d.device} ${d.mount_point}`}
                        aria-valuenow={Math.round(d.total_bytes > 0 ? ((d.total_bytes - d.available_bytes) / d.total_bytes) * 100 : 0)}
                        aria-valuemin={0}
                        aria-valuemax={100}
                        className="h-1.5 w-40 overflow-hidden rounded-full bg-neutral-soft"
                      >
                        <div
                          className="h-full bg-accent"
                          style={{
                            width: `${d.total_bytes > 0 ? Math.min(100, ((d.total_bytes - d.available_bytes) / d.total_bytes) * 100) : 0}%`,
                          }}
                        />
                      </div>
                      <span className="text-ink-soft">
                        {bytesLabel(d.total_bytes - d.available_bytes, locale)} / {bytesLabel(d.total_bytes, locale)} ({d.fs_type})
                      </span>
                    </li>
                  ))}
                </ul>
              </div>
            )}

            {metrics.networks.length > 0 && (
              <div>
                <p className="mb-1 font-medium text-ink">{t("nodedetail.metrics.network")}</p>
                <ul className="flex flex-wrap gap-3">
                  {metrics.networks.map((n) => (
                    <li key={n.interface} className="text-ink-soft">
                      {n.interface}: ↓{bytesLabel(n.rx_bytes, locale)} ↑{bytesLabel(n.tx_bytes, locale)}
                    </li>
                  ))}
                </ul>
              </div>
            )}

            <p className="text-[11px] text-ink-soft">
              {t("nodedetail.metrics.provenance", {
                source: metrics.source,
                time: new Date(metrics.collected_at).toLocaleString(locale === "tr" ? "tr-TR" : "en-US"),
              })}
            </p>
          </div>
        )}
      </section>

      {/* Kabiliyetler */}
      <section aria-label={t("nodedetail.capabilities.title")} className="rounded-xl border border-border bg-panel p-4 shadow-sm">
        <h3 className="text-sm font-semibold text-ink">{t("nodedetail.capabilities.title")}</h3>
        {capabilities.length === 0 ? (
          <p className="mt-2 text-xs text-ink-soft">{t("nodedetail.capabilities.none")}</p>
        ) : (
          <ul className="mt-2 flex flex-wrap gap-2">
            {capabilities.map((c) => (
              <li
                key={c.capability_id}
                title={c.available ? c.capability_id : c.reason_unavailable || c.capability_id}
                className={`rounded-full px-2.5 py-1 text-[11px] ${
                  c.available ? "bg-ok-soft text-ok" : "bg-warn-soft text-warn"
                }`}
              >
                {c.available ? "✓" : "✕"} {c.capability_id}
                {!c.available && c.reason_unavailable ? ` — ${c.reason_unavailable}` : ""}
              </li>
            ))}
          </ul>
        )}
      </section>

      {/* Servis işlemleri (tipli; shell yok) */}
      <section aria-label={t("nodedetail.services.title")} className="rounded-xl border border-border bg-panel p-4 shadow-sm">
        <h3 className="text-sm font-semibold text-ink">{t("nodedetail.services.title")}</h3>
        <p className="mt-1 text-[11px] text-ink-soft">{t("nodedetail.services.note")}</p>
        {serviceCap && !serviceCap.available && (
          <p role="status" className="mt-2 rounded-lg bg-warn-soft px-3 py-2 text-xs text-warn">
            {t("nodedetail.services.unavailable", { reason: serviceCap.reason_unavailable || "" })}
          </p>
        )}
        <form onSubmit={onUnitSubmit} className="mt-3 flex flex-col gap-2">
          <label className="flex flex-col gap-1">
            <span className="text-xs font-medium text-ink">{t("nodedetail.services.unit")}</span>
            <input
              value={unit}
              onChange={(e) => {
                setUnit(e.target.value);
                setUnitError(null);
              }}
              placeholder="nginx.service"
              aria-invalid={unitError !== null}
              className="rounded-lg border border-border bg-surface px-3 py-1.5 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
            />
          </label>
          {unitError && (
            <p role="alert" className="text-xs text-err">
              {unitError}
            </p>
          )}
          <div className="flex flex-wrap gap-2">
            <button
              type="button"
              onClick={() => void submitServiceOp("linux.systemd.service.list")}
              disabled={pendingOp !== null}
              className="rounded-lg border border-border px-3 py-1.5 text-xs text-ink hover:bg-neutral-soft disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent"
            >
              {t("nodedetail.services.list")}
            </button>
            {(["status", "start", "stop", "restart"] as const).map((verb) => {
              const op = `linux.systemd.service.${verb}` as ServiceOp;
              const destructive = verb === "stop" || verb === "restart";
              return (
                <button
                  key={verb}
                  type="button"
                  onClick={() => void submitServiceOp(op)}
                  disabled={pendingOp !== null}
                  className={`rounded-lg px-3 py-1.5 text-xs font-medium disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent ${
                    destructive
                      ? "border border-warn/40 bg-warn-soft text-warn hover:bg-warn/20"
                      : "border border-border bg-surface text-ink hover:bg-neutral-soft"
                  }`}
                >
                  {t(`nodedetail.services.${verb}` as const)}
                </button>
              );
            })}
          </div>
        </form>

        {lastTaskId && (
          <p role="status" className="mt-2 text-[11px] text-ok">
            {t("nodedetail.services.queued", { task: lastTaskId })}
          </p>
        )}

        {tasks.length > 0 && (
          <table className="mt-3 w-full text-left text-xs">
            <caption className="sr-only">{t("nodedetail.services.tableCaption")}</caption>
            <thead>
              <tr className="border-b border-border text-[11px] uppercase tracking-wide text-ink-soft">
                <th scope="col" className="px-2 py-1.5 font-medium">{t("nodedetail.services.colOp")}</th>
                <th scope="col" className="px-2 py-1.5 font-medium">{t("nodedetail.services.colStatus")}</th>
                <th scope="col" className="px-2 py-1.5 font-medium">{t("nodedetail.services.colTime")}</th>
              </tr>
            </thead>
            <tbody>
              {tasks.map((task) => (
                <tr key={task.id} className="border-b border-border/60 last:border-0">
                  <td className="px-2 py-1.5 text-ink">{task.operation}</td>
                  <td className="px-2 py-1.5">
                    <StatusBadge variant={taskBadge[task.status]}>{t(`tasks.status.${task.status}` as const)}</StatusBadge>
                  </td>
                  <td className="px-2 py-1.5 text-ink-soft">{new Date(task.updated_at).toLocaleString(locale === "tr" ? "tr-TR" : "en-US")}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>
    </div>
  );
}

function Fact({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <dt className="text-[10px] uppercase tracking-wide text-ink-soft">{label}</dt>
      <dd className="truncate text-ink" title={value}>{value}</dd>
    </div>
  );
}

function Metric({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <div>
      <dt className="text-[10px] uppercase tracking-wide text-ink-soft">{label}</dt>
      <dd className="text-ink">{value}</dd>
      {hint && <dd className="text-[10px] text-ink-soft">{hint}</dd>}
    </div>
  );
}
