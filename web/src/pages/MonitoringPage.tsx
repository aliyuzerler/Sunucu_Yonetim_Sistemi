import { useCallback, useEffect, useMemo, useState } from "react";
import type { FormEvent } from "react";
import { apiFetch, isApiError } from "../api/client";
import type { Alert, AlertRule, AlertStatus, MetricsSnapshot, NodePage } from "../api/client";
import { EmptyState } from "../components/EmptyState";
import { StatusBadge } from "../components/StatusBadge";
import type { BadgeVariant } from "../components/StatusBadge";
import { useI18n } from "../i18n/LocaleProvider";

const alertVariant: Record<AlertStatus, BadgeVariant> = {
  open: "error",
  acknowledged: "warn",
  silenced: "neutral",
  resolved: "ok",
};

const sevVariant: Record<Alert["severity"], BadgeVariant> = {
  info: "info",
  warning: "warn",
  critical: "error",
};

const FRESH_THRESHOLD_MS = 180_000;

// freshnessState maps the newest snapshot age to an honest badge: without
// data the node is "unknown" — never "ok" (phase 13 criterion 5).
export function freshnessState(snapshot: MetricsSnapshot | null, now: number): BadgeVariant {
  if (!snapshot?.collected_at) return "warn"; // unknown rendering (label below)
  const age = now - new Date(snapshot.collected_at).getTime();
  return age <= FRESH_THRESHOLD_MS ? "ok" : "warn";
}

export function MonitoringPage() {
  const { t, locale } = useI18n();
  const [alerts, setAlerts] = useState<Alert[] | null>(null);
  const [rules, setRules] = useState<AlertRule[] | null>(null);
  const [nodes, setNodes] = useState<NodePage["nodes"]>([]);
  const [fresher, setFresher] = useState<Record<string, MetricsSnapshot | null>>({});
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [reloadKey, setReloadKey] = useState(0);
  const [nowMs, setNowMs] = useState(() => Date.now());

  const reload = useCallback(() => setReloadKey((k) => k + 1), []);

  useEffect(() => {
    const id = setInterval(() => setNowMs(Date.now()), 30_000);
    return () => clearInterval(id);
  }, []);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const [alertList, ruleList, nodeList] = await Promise.all([
          apiFetch<{ alerts: Alert[] }>("/api/v1/alerts?limit=100"),
          apiFetch<{ rules: AlertRule[] }>("/api/v1/alert-rules?include_disabled=1"),
          apiFetch<NodePage>("/api/v1/nodes?limit=200"),
        ]);
        if (cancelled) return;
        setAlerts(alertList.alerts);
        setRules(ruleList.rules);
        setNodes(nodeList.nodes);
        setError(null);
        // Node freshness kartları: mevcut snapshot'ları paralel çek.
        const results = await Promise.all(
          nodeList.nodes.map(async (n) => {
            try {
              const snap = await apiFetch<MetricsSnapshot>(`/api/v1/nodes/${n.id}/metrics/latest`);
              return [n.id, snap] as const;
            } catch {
              return [n.id, null] as const;
            }
          }),
        );
        if (!cancelled) setFresher(Object.fromEntries(results));
      } catch (err) {
        if (!cancelled) setError(isApiError(err) ? err.message : t("common.error.generic"));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [reloadKey, t]);

  async function alertAction(id: string, action: "acknowledge" | "resolve", body?: unknown) {
    setError(null);
    setNotice(null);
    try {
      await apiFetch(`/api/v1/alerts/${id}/${action}`, {
        method: "POST",
        // exactOptionalPropertyTypes: body yalnızca VARSA ekle.
        ...(body !== undefined ? { body: JSON.stringify(body) } : {}),
      });
      setNotice(t("monitoring.actionDone"));
      reload();
    } catch (err) {
      setError(isApiError(err) ? err.message : t("common.error.generic"));
    }
  }

  async function silenceAlert(id: string, minutes: number) {
    setError(null);
    setNotice(null);
    try {
      await apiFetch(`/api/v1/alerts/${id}/silence`, {
        method: "POST",
        body: JSON.stringify({ duration_minutes: minutes }),
      });
      setNotice(t("monitoring.actionDone"));
      reload();
    } catch (err) {
      setError(isApiError(err) ? err.message : t("common.error.generic"));
    }
  }

  async function toggleRule(rule: AlertRule) {
    setError(null);
    try {
      await apiFetch(`/api/v1/alert-rules/${rule.id}`, {
        method: "PATCH",
        body: JSON.stringify({ enabled: !rule.enabled }),
      });
      reload();
    } catch (err) {
      setError(isApiError(err) ? err.message : t("common.error.generic"));
    }
  }

  async function deleteRule(id: string) {
    setError(null);
    try {
      await apiFetch(`/api/v1/alert-rules/${id}`, { method: "DELETE" });
      setNotice(t("monitoring.ruleDeleted"));
      reload();
    } catch (err) {
      setError(isApiError(err) ? err.message : t("common.error.generic"));
    }
  }

  const activeAlerts = useMemo(
    () => (alerts ?? []).filter((a) => a.status !== "resolved"),
    [alerts],
  );
  const loading = alerts === null && error === null;

  return (
    <div className="flex flex-col gap-4 p-6">
      <div className="flex items-center justify-between">
        <div>
          <p className="text-[11px] text-ink-soft">{t("nav.group.operations")}</p>
          <h1 className="text-xl font-semibold text-ink">{t("nav.item.monitoring")}</h1>
        </div>
        <button
          type="button"
          onClick={reload}
          className="rounded-lg border border-border bg-panel px-3 py-1.5 text-xs font-medium text-ink hover:bg-neutral-soft focus-visible:outline-2 focus-visible:outline-accent"
        >
          {t("monitoring.refresh")}
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

      {/* Node freshness özeti — veri yoksa "bilinmiyor" dürüst gösterilir. */}
      <section aria-labelledby="monitoring-freshness" className="rounded-xl border border-border bg-panel p-4">
        <h2 id="monitoring-freshness" className="text-sm font-semibold text-ink">
          {t("monitoring.freshnessTitle")}
        </h2>
        {nodes.length === 0 ? (
          <p className="mt-2 text-xs text-ink-soft">{t("monitoring.noNodes")}</p>
        ) : (
          <ul className="mt-3 grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3">
            {nodes.map((n) => {
              const snap = fresher[n.id] ?? null;
              const state = snap ? freshnessState(snap, nowMs) : "warn";
              const known = snap !== null && fresher[n.id] !== undefined;
              const ageMin = snap ? Math.round((nowMs - new Date(snap.collected_at).getTime()) / 60000) : null;
              return (
                <li key={n.id} className="flex items-center justify-between rounded-lg border border-border px-3 py-2">
                  <span className="truncate text-xs font-medium text-ink">{n.display_name}</span>
                  <span className="flex items-center gap-2">
                    <StatusBadge variant={state}>
                      {known ? t("monitoring.freshMinutes", { n: String(ageMin ?? 0) }) : t("monitoring.freshUnknown")}
                    </StatusBadge>
                    {snap && (
                      <span className="text-[11px] text-ink-soft">
                        {t("monitoring.cpuMem", {
                          cpu: snap.cpu.percent.toFixed(0),
                          mem: snap.memory.used_percent.toFixed(0),
                        })}
                      </span>
                    )}
                  </span>
                </li>
              );
            })}
          </ul>
        )}
        <p className="mt-2 text-[11px] text-ink-soft">{t("monitoring.freshnessNote")}</p>
      </section>

      {/* Aktif alarmlar */}
      <section aria-labelledby="monitoring-alerts" className="rounded-xl border border-border bg-panel p-4">
        <h2 id="monitoring-alerts" className="text-sm font-semibold text-ink">
          {t("monitoring.alertsTitle")}
        </h2>
        {loading ? (
          <p className="mt-2 text-xs text-ink-soft">{t("common.loading")}</p>
        ) : activeAlerts.length === 0 ? (
          <div className="mt-3">
            <EmptyState title={t("monitoring.noAlerts")} description={t("monitoring.noAlertsHint")} />
          </div>
        ) : (
          <ul className="mt-3 flex flex-col gap-2">
            {activeAlerts.map((a) => (
              <li key={a.id} className="rounded-lg border border-border p-3">
                <div className="flex flex-wrap items-center gap-2">
                  <StatusBadge variant={alertVariant[a.status]}>{t(`monitoring.status.${a.status}`)}</StatusBadge>
                  <StatusBadge variant={sevVariant[a.severity]}>{t(`monitoring.severity.${a.severity}`)}</StatusBadge>
                  <span className="text-sm font-medium text-ink">{a.title}</span>
                  <span className="text-[11px] text-ink-soft">
                    {t("monitoring.occurrences", { n: String(a.occurrences) })} ·{" "}
                    {new Date(a.last_seen_at).toLocaleString(locale === "tr" ? "tr-TR" : "en-US")}
                  </span>
                </div>
                <p className="mt-1 text-xs text-ink-soft">{a.detail}</p>
                <div className="mt-2 flex flex-wrap items-center gap-2">
                  {(a.status === "open" || a.status === "acknowledged") && (
                    <button
                      type="button"
                      onClick={() => alertAction(a.id, "acknowledge")}
                      className="rounded-lg border border-border bg-panel px-2.5 py-1 text-xs font-medium text-ink hover:bg-neutral-soft focus-visible:outline-2 focus-visible:outline-accent"
                    >
                      {t("monitoring.ack")}
                    </button>
                  )}
                  <button
                    type="button"
                    onClick={() => silenceAlert(a.id, 60)}
                    className="rounded-lg border border-border bg-panel px-2.5 py-1 text-xs font-medium text-ink hover:bg-neutral-soft focus-visible:outline-2 focus-visible:outline-accent"
                  >
                    {t("monitoring.silence1h")}
                  </button>
                  <button
                    type="button"
                    onClick={() => alertAction(a.id, "resolve")}
                    className="rounded-lg border border-border bg-panel px-2.5 py-1 text-xs font-medium text-ink hover:bg-neutral-soft focus-visible:outline-2 focus-visible:outline-accent"
                  >
                    {t("monitoring.resolve")}
                  </button>
                </div>
              </li>
            ))}
          </ul>
        )}
      </section>

      <RuleSection rules={rules} nodes={nodes} onToggle={toggleRule} onDelete={deleteRule} onCreated={reload} />
    </div>
  );
}

// RuleSection lists rules and offers the bounded create form.
function RuleSection(props: {
  rules: AlertRule[] | null;
  nodes: NodePage["nodes"];
  onToggle: (r: AlertRule) => void;
  onDelete: (id: string) => void;
  onCreated: () => void;
}) {
  const { t } = useI18n();
  const [error, setError] = useState<string | null>(null);

  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    // currentTarget async devamda null olur (React sözleşmesi) — referans await ÖNCESİ alınır.
    const formEl = e.currentTarget;
    const form = new FormData(formEl);
    const body = {
      name: String(form.get("name") ?? "").trim(),
      node_id: String(form.get("node_id") ?? ""),
      metric: String(form.get("metric") ?? "cpu"),
      dimension: String(form.get("dimension") ?? ""),
      op: String(form.get("op") ?? "gt"),
      threshold: Number(form.get("threshold") ?? 0),
      duration_seconds: Number(form.get("duration_seconds") ?? 0),
      severity: String(form.get("severity") ?? "warning"),
    };
    try {
      await apiFetch("/api/v1/alert-rules", { method: "POST", body: JSON.stringify(body) });
      formEl.reset();
      props.onCreated();
    } catch (err) {
      setError(isApiError(err) ? err.message : t("common.error.generic"));
    }
  }

  return (
    <section aria-labelledby="monitoring-rules" className="rounded-xl border border-border bg-panel p-4">
      <h2 id="monitoring-rules" className="text-sm font-semibold text-ink">
        {t("monitoring.rulesTitle")}
      </h2>
      {props.rules && props.rules.length > 0 ? (
        <ul className="mt-3 flex flex-col gap-2">
          {props.rules.map((r) => (
            <li key={r.id} className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-border px-3 py-2">
              <span className="text-xs text-ink">
                <span className="font-medium">{r.name}</span> · {r.metric}
                {r.dimension ? ` (${r.dimension})` : ""} {r.op} {r.threshold}
                {r.duration_seconds > 0 ? ` · ${r.duration_seconds}s` : ""} ·{" "}
                <StatusBadge variant={r.enabled ? "ok" : "neutral"}>{r.enabled ? t("monitoring.ruleEnabled") : t("monitoring.ruleDisabled")}</StatusBadge>
              </span>
              <span className="flex gap-2">
                <button
                  type="button"
                  onClick={() => props.onToggle(r)}
                  className="rounded-lg border border-border bg-panel px-2.5 py-1 text-xs font-medium text-ink hover:bg-neutral-soft focus-visible:outline-2 focus-visible:outline-accent"
                >
                  {r.enabled ? t("monitoring.ruleDisable") : t("monitoring.ruleEnable")}
                </button>
                <button
                  type="button"
                  onClick={() => props.onDelete(r.id)}
                  className="rounded-lg border border-border bg-panel px-2.5 py-1 text-xs font-medium text-err hover:bg-err-soft focus-visible:outline-2 focus-visible:outline-accent"
                >
                  {t("monitoring.ruleDelete")}
                </button>
              </span>
            </li>
          ))}
        </ul>
      ) : (
        <p className="mt-2 text-xs text-ink-soft">{t("monitoring.noRules")}</p>
      )}

      <form onSubmit={submit} className="mt-4 grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-4" aria-label={t("monitoring.ruleCreate")}>
        <input
          name="name"
          required
          maxLength={80}
          aria-label={t("monitoring.ruleName")}
          placeholder={t("monitoring.ruleName")}
          className="rounded-lg border border-border bg-surface px-2.5 py-1.5 text-xs text-ink focus-visible:outline-2 focus-visible:outline-accent"
        />
        <select
          name="node_id"
          className="rounded-lg border border-border bg-surface px-2.5 py-1.5 text-xs text-ink focus-visible:outline-2 focus-visible:outline-accent"
          aria-label={t("monitoring.ruleScope")}
        >
          <option value="">{t("monitoring.scopeAll")}</option>
          {props.nodes.map((n) => (
            <option key={n.id} value={n.id}>
              {n.display_name}
            </option>
          ))}
        </select>
        <select
          name="metric"
          className="rounded-lg border border-border bg-surface px-2.5 py-1.5 text-xs text-ink focus-visible:outline-2 focus-visible:outline-accent"
          aria-label={t("monitoring.ruleMetric")}
        >
          <option value="cpu">CPU %</option>
          <option value="memory">{t("monitoring.metricMemory")}</option>
          <option value="disk">{t("monitoring.metricDisk")}</option>
          <option value="metrics_freshness">{t("monitoring.metricFreshness")}</option>
        </select>
        <select
          name="severity"
          className="rounded-lg border border-border bg-surface px-2.5 py-1.5 text-xs text-ink focus-visible:outline-2 focus-visible:outline-accent"
          aria-label={t("monitoring.ruleSeverity")}
        >
          <option value="info">{t("monitoring.severity.info")}</option>
          <option value="warning">{t("monitoring.severity.warning")}</option>
          <option value="critical">{t("monitoring.severity.critical")}</option>
        </select>
        <select name="op" className="rounded-lg border border-border bg-surface px-2.5 py-1.5 text-xs text-ink focus-visible:outline-2 focus-visible:outline-accent" aria-label={t("monitoring.ruleOp")}>
          <option value="gt">&gt; {t("monitoring.opAbove")}</option>
          <option value="lt">&lt; {t("monitoring.opBelow")}</option>
        </select>
        <input
          name="threshold"
          type="number"
          step="any"
          min="0.1"
          required
          aria-label={t("monitoring.ruleThreshold")}
          placeholder={t("monitoring.ruleThreshold")}
          className="rounded-lg border border-border bg-surface px-2.5 py-1.5 text-xs text-ink focus-visible:outline-2 focus-visible:outline-accent"
        />
        <input
          name="duration_seconds"
          type="number"
          min="0"
          max="3600"
          defaultValue={0}
          placeholder={t("monitoring.ruleDuration")}
          className="rounded-lg border border-border bg-surface px-2.5 py-1.5 text-xs text-ink focus-visible:outline-2 focus-visible:outline-accent"
        />
        <button
          type="submit"
          className="rounded-lg bg-accent px-3 py-1.5 text-xs font-semibold text-white hover:opacity-90 focus-visible:outline-2 focus-visible:outline-accent"
        >
          {t("monitoring.ruleCreate")}
        </button>
      </form>
      {error && (
        <p role="alert" className="mt-2 rounded-lg bg-err-soft px-3 py-2 text-xs text-err">
          {error}
        </p>
      )}
      <p className="mt-2 text-[11px] text-ink-soft">{t("monitoring.ruleNote")}</p>
    </section>
  );
}
