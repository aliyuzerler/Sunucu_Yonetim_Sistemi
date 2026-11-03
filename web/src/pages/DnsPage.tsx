import { useCallback, useEffect, useState } from "react";
import { apiFetch, isApiError } from "../api/client";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { EmptyState } from "../components/EmptyState";
import { ToastProvider, useToast } from "../components/ToastProvider";
import { useI18n } from "../i18n/LocaleProvider";
import type { TaskRecord } from "../api/client";

// DnsPage is the Faz 09 domains & DNS view (docs/architecture/dns-ssl.md).
// Provider credentials are WRITE-ONLY: the create form posts them once and
// no read path ever renders them again (acceptance criterion 5). Record
// changes always pass through a diff preview (dns.record.diff) before the
// apply task is created (criterion 2), and TTL is validated client-side
// against the mirrored bounds.

interface ProviderRow {
  id: string;
  name: string;
  kind: string;
  capabilities: {
    record_types: string[];
    ttl_min: number;
    ttl_max: number;
    supports_dns01: boolean;
    wildcard: boolean;
  };
  has_credentials: boolean;
  credential_keys: string[];
  created_at: string;
}

interface DnsRecord {
  name: string;
  type: string;
  value: string;
  ttl: number;
}

interface DnsChange {
  action: string;
  before?: DnsRecord | null;
  after?: DnsRecord | null;
}

const RECORD_TYPES = ["A", "AAAA", "CNAME", "TXT", "MX"];
const NAME_PATTERN = /^(@|(\*\.)?[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*)$/;

type Report = { kind: "zones"; zones: string[] } | { kind: "records"; records: DnsRecord[] } | { kind: "diff"; change: DnsChange } | { kind: "applied"; change: DnsChange } | { kind: "deleted" } | null;

async function runTask(
  operation: string,
  payload: unknown,
  fallback: string,
): Promise<TaskRecord> {
  const created = await apiFetch<TaskRecord>("/api/v1/tasks", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ operation, payload }),
  });
  for (let attempt = 0; attempt < 12; attempt += 1) {
    await new Promise((r) => setTimeout(r, attempt === 0 ? 700 : 1000));
    const task = await apiFetch<TaskRecord>(`/api/v1/tasks/${encodeURIComponent(created.id)}`);
    if (task.status === "succeeded") return task;
    if (task.status === "failed" || task.status === "cancelled" || task.status === "expired") {
      throw new Error(task.error_category ?? fallback);
    }
  }
  throw new Error(fallback);
}

// Sayfa kendi ToastProvider'ını taşır: hem App kabuğundan hem izole
// render'dan (testler) bağımsız çalışır; iç içe sağlayıcı zararsızdır.
export function DnsPage() {
  return (
    <ToastProvider>
      <DnsPageInner />
    </ToastProvider>
  );
}

function DnsPageInner() {
  const { t } = useI18n();
  const { showToast } = useToast();
  const [providers, setProviders] = useState<ProviderRow[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  // UX denetimi m.4.3: window.confirm yerine erişilebilir dialog.
  const [pendingDelete, setPendingDelete] = useState<ProviderRow | null>(null);

  // add-provider form
  const [newName, setNewName] = useState("");
  const [newKind, setNewKind] = useState("mock");
  const [newApiKey, setNewApiKey] = useState("");
  const [creating, setCreating] = useState(false);

  // zone/record browser
  const [selectedProvider, setSelectedProvider] = useState<string>("");
  const [zones, setZones] = useState<string[] | null>(null);
  const [selectedZone, setSelectedZone] = useState("");
  const [records, setRecords] = useState<DnsRecord[] | null>(null);
  const [report, setReport] = useState<Report>(null);
  const [busy, setBusy] = useState(false);

  // record form
  const [recName, setRecName] = useState("");
  const [recType, setRecType] = useState("A");
  const [recValue, setRecValue] = useState("");
  const [recTTL, setRecTTL] = useState(3600);

  const reloadProviders = useCallback(async () => {
    try {
      const res = await apiFetch<{ providers: ProviderRow[] }>("/api/v1/dns-providers");
      setProviders(res.providers);
      setError(null);
    } catch (err) {
      setError(isApiError(err) ? err.message : t("common.error.generic"));
    }
  }, [t]);

  // İlk yükleme: UsersPage deseni — asenkron IIFE + cancelled bayrağı
  // (react-hooks/set-state-in-effect kuralı senkron setState'i yasaklar).
  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const res = await apiFetch<{ providers: ProviderRow[] }>("/api/v1/dns-providers");
        if (!cancelled) setProviders(res.providers);
      } catch (err) {
        if (!cancelled) setError(isApiError(err) ? err.message : t("common.error.generic"));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [t]);

  async function submitProvider(e: { preventDefault: () => void }) {
    e.preventDefault();
    setFormError(null);
    if (!newName.trim()) {
      setFormError(t("dns.providers.name"));
      return;
    }
    if (!newApiKey.trim()) {
      setFormError(t("dns.providers.apiKey"));
      return;
    }
    setCreating(true);
    try {
      // Kimlik bilgisi sözleşmesi türe göre değişir: mock → api_key,
      // cloudflare → api_token (ADR-0021). Her ikisi de YALNIZ YAZILIR.
      const credentials =
        newKind === "cloudflare" ? { api_token: newApiKey } : { api_key: newApiKey };
      await apiFetch("/api/v1/dns-providers", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          name: newName.trim(),
          kind: newKind,
          credentials,
        }),
      });
      // Başarı sonrası form sıfırlanır — kimlik bilgisi asla geri okunmaz.
      setNewName("");
      setNewApiKey("");
      await reloadProviders();
    } catch (err) {
      setFormError(isApiError(err) ? err.message : t("common.error.generic"));
    } finally {
      setCreating(false);
    }
  }

  async function deleteProvider(id: string) {
    try {
      await apiFetch(`/api/v1/dns-providers/${encodeURIComponent(id)}`, { method: "DELETE" });
      if (selectedProvider === id) {
        setSelectedProvider("");
        setZones(null);
        setSelectedZone("");
        setRecords(null);
        setReport(null);
      }
      showToast(t("dns.providers.deleted"), "ok");
      await reloadProviders();
    } catch (err) {
      setError(isApiError(err) ? err.message : t("common.error.generic"));
    }
  }

  async function listZones() {
    if (!selectedProvider) return;
    setBusy(true);
    setError(null);
    try {
      const task = await runTask("dns.zone.list", { provider_id: selectedProvider }, t("common.error.generic"));
      const result = (task.result ?? {}) as { zones?: string[] };
      setZones(result.zones ?? []);
      setReport({ kind: "zones", zones: result.zones ?? [] });
    } catch (err) {
      setError(err instanceof Error ? err.message : t("common.error.generic"));
    } finally {
      setBusy(false);
    }
  }

  async function listRecords() {
    if (!selectedProvider || !selectedZone) return;
    setBusy(true);
    setError(null);
    try {
      const task = await runTask(
        "dns.record.list",
        { provider_id: selectedProvider, zone: selectedZone },
        t("common.error.generic"),
      );
      const result = (task.result ?? {}) as { records?: DnsRecord[] };
      setRecords(result.records ?? []);
      setReport({ kind: "records", records: result.records ?? [] });
    } catch (err) {
      setError(err instanceof Error ? err.message : t("common.error.generic"));
    } finally {
      setBusy(false);
    }
  }

  function validateRecordForm(): DnsRecord | null {
    if (!NAME_PATTERN.test(recName.trim())) {
      setFormError(t("dns.records.nameInvalid"));
      return null;
    }
    if (recTTL < 60 || recTTL > 86400) {
      setFormError(t("dns.records.ttlInvalid"));
      return null;
    }
    if (!recValue.trim()) {
      setFormError(t("dns.records.valueRequired"));
      return null;
    }
    setFormError(null);
    return { name: recName.trim(), type: recType, value: recValue.trim(), ttl: recTTL };
  }

  async function previewDiff() {
    const rec = validateRecordForm();
    if (!rec || !selectedZone) return;
    setBusy(true);
    try {
      const task = await runTask(
        "dns.record.diff",
        { provider_id: selectedProvider, zone: selectedZone, ...rec },
        t("common.error.generic"),
      );
      const result = (task.result ?? {}) as { change?: DnsChange };
      setReport({ kind: "diff", change: result.change ?? { action: "create", after: rec } });
    } catch (err) {
      setError(err instanceof Error ? err.message : t("common.error.generic"));
    } finally {
      setBusy(false);
    }
  }

  async function applyRecord() {
    const rec = validateRecordForm();
    if (!rec || !selectedZone) return;
    if (!report || report.kind !== "diff") {
      setFormError(t("dns.records.noDiff"));
      return;
    }
    setBusy(true);
    try {
      const task = await runTask(
        "dns.record.apply",
        { provider_id: selectedProvider, zone: selectedZone, ...rec },
        t("common.error.generic"),
      );
      const result = (task.result ?? {}) as { change?: DnsChange };
      setReport({ kind: "applied", change: result.change ?? { action: "update", after: rec } });
      await listRecords();
    } catch (err) {
      setError(err instanceof Error ? err.message : t("common.error.generic"));
    } finally {
      setBusy(false);
    }
  }

  async function deleteRecord(name: string, type: string) {
    if (!selectedZone) return;
    setBusy(true);
    try {
      await runTask(
        "dns.record.delete",
        { provider_id: selectedProvider, zone: selectedZone, name, type },
        t("common.error.generic"),
      );
      setReport({ kind: "deleted" });
      await listRecords();
    } catch (err) {
      setError(err instanceof Error ? err.message : t("common.error.generic"));
    } finally {
      setBusy(false);
    }
  }

  const selected = providers?.find((p) => p.id === selectedProvider);

  return (
    <div className="flex flex-col gap-4 p-6">
      <div>
        <p className="text-[11px] text-ink-soft">{t("nav.group.hosting")}</p>
        <h1 className="text-xl font-semibold text-ink">{t("dns.title")}</h1>
        <p className="mt-1 max-w-3xl text-xs leading-relaxed text-ink-soft">{t("dns.note")}</p>
      </div>

      {error && (
        <p role="alert" className="rounded-lg bg-err-soft px-3 py-2 text-xs text-err">
          {error}
        </p>
      )}

      {/* ---- Sağlayıcılar (kimlik bilgileri yaz-amaçlı) ---- */}
      <section aria-label={t("dns.providers.title")} className="rounded-xl border border-border bg-panel shadow-sm">
        <div className="border-b border-border px-4 py-3">
          <h2 className="text-sm font-semibold text-ink">{t("dns.providers.title")}</h2>
        </div>
        {providers === null ? (
          <p className="p-6 text-sm text-ink-soft">{t("common.loading")}</p>
        ) : providers.length === 0 ? (
          <EmptyState title={t("dns.providers.empty")} description={t("dns.providers.emptyHint")} />
        ) : (
          <table className="w-full text-left text-sm">
            <caption className="sr-only">{t("dns.providers.title")}</caption>
            <thead>
              <tr className="border-b border-border text-xs uppercase tracking-wide text-ink-soft">
                <th scope="col" className="px-4 py-2 font-medium">{t("dns.providers.name")}</th>
                <th scope="col" className="px-4 py-2 font-medium">{t("dns.providers.kind")}</th>
                <th scope="col" className="px-4 py-2 font-medium">{t("dns.providers.caps")}</th>
                <th scope="col" className="px-4 py-2 font-medium"><span className="sr-only">/</span></th>
              </tr>
            </thead>
            <tbody>
              {providers.map((p) => (
                <tr key={p.id} className="border-b border-border/60 last:border-0">
                  <td className="px-4 py-2.5">
                    <p className="font-medium text-ink">{p.name}</p>
                    <p className="text-[11px] text-ink-soft">{t("dns.providers.hasCreds")}</p>
                  </td>
                  <td className="px-4 py-2.5 text-xs">{p.kind}</td>
                  <td className="px-4 py-2.5 text-[11px] text-ink-soft">
                    {p.capabilities.record_types.join(", ")} · TTL {p.capabilities.ttl_min}-
                    {p.capabilities.ttl_max} · DNS-01 {p.capabilities.supports_dns01 ? "✓" : "✗"}
                  </td>
                  <td className="px-4 py-2.5 text-right">
                    <button
                      type="button"
                      onClick={() => {
                        setSelectedProvider(p.id);
                        setZones(null);
                        setSelectedZone("");
                        setRecords(null);
                        setReport(null);
                      }}
                      className="rounded-md border border-border px-2 py-1 text-xs hover:bg-surface focus-visible:outline-2 focus-visible:outline-accent"
                    >
                      {t("dns.zones.title")}
                    </button>{" "}
                    <button
                      type="button"
                      onClick={() => setPendingDelete(p)}
                      className="rounded-md border border-border px-2 py-1 text-xs text-err hover:bg-err-soft focus-visible:outline-2 focus-visible:outline-accent"
                    >
                      {t("dns.providers.delete")}
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        <form onSubmit={submitProvider} className="flex flex-wrap items-end gap-3 border-t border-border px-4 py-3">
          <label className="text-xs text-ink-soft">
            {t("dns.providers.name")}
            <input
              value={newName}
              onChange={(e) => setNewName(e.target.value)}
              className="mt-1 block w-44 rounded-md border border-border bg-surface px-2 py-1.5 text-sm text-ink"
            />
          </label>
          <label className="text-xs text-ink-soft">
            {t("dns.providers.kind")}
            <select
              value={newKind}
              onChange={(e) => setNewKind(e.target.value)}
              className="mt-1 block w-56 rounded-md border border-border bg-surface px-2 py-1.5 text-sm text-ink"
            >
              <option value="mock">{t("dns.providers.kindMock")}</option>
              <option value="cloudflare">{t("dns.providers.kindCloudflare")}</option>
            </select>
          </label>
          <label className="text-xs text-ink-soft">
            {newKind === "cloudflare" ? t("dns.providers.apiToken") : t("dns.providers.apiKey")}
            <input
              type="password"
              value={newApiKey}
              onChange={(e) => setNewApiKey(e.target.value)}
              className="mt-1 block w-52 rounded-md border border-border bg-surface px-2 py-1.5 text-sm text-ink"
            />
          </label>
          <button
            type="submit"
            disabled={creating}
            className="rounded-md bg-accent px-3 py-1.5 text-xs font-medium text-white disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent"
          >
            {t("dns.providers.add")}
          </button>
          {formError && (
            <p role="alert" className="text-xs text-err">
              {formError}
            </p>
          )}
          {newKind === "cloudflare" && !formError && (
            <p className="w-full text-xs text-ink-soft">{t("dns.providers.cloudflareHint")}</p>
          )}
        </form>
      </section>

      {/* ---- Bölgeler + kayıtlar ---- */}
      {selected && (
        <section aria-label={t("dns.zones.title")} className="rounded-xl border border-border bg-panel shadow-sm">
          <div className="flex flex-wrap items-center gap-3 border-b border-border px-4 py-3">
            <h2 className="text-sm font-semibold text-ink">
              {t("dns.zones.title")} — {selected.name}
            </h2>
            <button
              type="button"
              onClick={() => void listZones()}
              disabled={busy}
              className="rounded-md border border-border px-2 py-1 text-xs hover:bg-surface disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent"
            >
              {t("dns.zones.list")}
            </button>
            {zones && zones.length > 0 && (
              <select
                aria-label={t("dns.zones.title")}
                value={selectedZone}
                onChange={(e) => {
                  setSelectedZone(e.target.value);
                  setRecords(null);
                  setReport(null);
                }}
                className="rounded-md border border-border bg-surface px-2 py-1 text-xs"
              >
                <option value="">{t("dns.zones.none")}</option>
                {zones.map((z) => (
                  <option key={z} value={z}>
                    {z}
                  </option>
                ))}
              </select>
            )}
            {selectedZone && (
              <button
                type="button"
                onClick={() => void listRecords()}
                disabled={busy}
                className="rounded-md border border-border px-2 py-1 text-xs hover:bg-surface disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent"
              >
                {t("dns.records.list")}
              </button>
            )}
          </div>

          {records !== null && (
            <div className="p-4">
              {records.length === 0 ? (
                <EmptyState title={t("dns.records.empty")} description="" />
              ) : (
                <table className="w-full text-left text-sm">
                  <caption className="sr-only">{t("dns.records.title")}</caption>
                  <thead>
                    <tr className="border-b border-border text-xs uppercase tracking-wide text-ink-soft">
                      <th scope="col" className="px-2 py-2 font-medium">{t("dns.records.name")}</th>
                      <th scope="col" className="px-2 py-2 font-medium">{t("dns.records.type")}</th>
                      <th scope="col" className="px-2 py-2 font-medium">{t("dns.records.value")}</th>
                      <th scope="col" className="px-2 py-2 font-medium">TTL</th>
                      <th scope="col" className="px-2 py-2 font-medium"><span className="sr-only">/</span></th>
                    </tr>
                  </thead>
                  <tbody>
                    {records.map((r) => (
                      <tr key={`${r.name}/${r.type}`} className="border-b border-border/60 last:border-0">
                        <td className="px-2 py-2 font-medium text-ink">{r.name}</td>
                        <td className="px-2 py-2 text-xs">{r.type}</td>
                        <td className="max-w-[22rem] truncate px-2 py-2 text-xs text-ink-soft">{r.value}</td>
                        <td className="px-2 py-2 text-xs">{r.ttl}</td>
                        <td className="px-2 py-2 text-right">
                          <button
                            type="button"
                            onClick={() => void deleteRecord(r.name, r.type)}
                            disabled={busy}
                            className="rounded-md border border-border px-2 py-0.5 text-[11px] text-err hover:bg-err-soft disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent"
                          >
                            {t("dns.records.delete")}
                          </button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </div>
          )}

          {selectedZone && (
            <form
              onSubmit={(e) => e.preventDefault()}
              className="flex flex-wrap items-end gap-3 border-t border-border px-4 py-3"
            >
              <label className="text-xs text-ink-soft">
                {t("dns.records.name")}
                <input
                  value={recName}
                  onChange={(e) => setRecName(e.target.value)}
                  placeholder="@ | www | *.sub"
                  className="mt-1 block w-40 rounded-md border border-border bg-surface px-2 py-1.5 text-sm text-ink"
                />
              </label>
              <label className="text-xs text-ink-soft">
                {t("dns.records.type")}
                <select
                  value={recType}
                  onChange={(e) => setRecType(e.target.value)}
                  className="mt-1 block w-24 rounded-md border border-border bg-surface px-2 py-1.5 text-sm text-ink"
                >
                  {RECORD_TYPES.map((tp) => (
                    <option key={tp} value={tp}>
                      {tp}
                    </option>
                  ))}
                </select>
              </label>
              <label className="text-xs text-ink-soft">
                {t("dns.records.value")}
                <input
                  value={recValue}
                  onChange={(e) => setRecValue(e.target.value)}
                  className="mt-1 block w-56 rounded-md border border-border bg-surface px-2 py-1.5 text-sm text-ink"
                />
              </label>
              <label className="text-xs text-ink-soft">
                {t("dns.records.ttl")}
                <input
                  type="number"
                  min={60}
                  max={86400}
                  value={recTTL}
                  onChange={(e) => setRecTTL(Number(e.target.value))}
                  className="mt-1 block w-28 rounded-md border border-border bg-surface px-2 py-1.5 text-sm text-ink"
                />
              </label>
              <button
                type="button"
                onClick={() => void previewDiff()}
                disabled={busy}
                className="rounded-md border border-border px-3 py-1.5 text-xs hover:bg-surface disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent"
              >
                {t("dns.records.diff")}
              </button>
              <button
                type="button"
                onClick={() => void applyRecord()}
                disabled={busy}
                className="rounded-md bg-accent px-3 py-1.5 text-xs font-medium text-white disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent"
              >
                {t("dns.records.apply")}
              </button>
            </form>
          )}

          {report?.kind === "diff" && report.change && (
            <div className="border-t border-border px-4 py-3" data-testid="dns-diff">
              <p className="text-xs font-semibold text-ink">
                {t("dns.records.diffTitle")} ·{" "}
                {report.change.action === "create" ? t("dns.diff.create") : t("dns.diff.update")}
              </p>
              <pre className="mt-2 overflow-x-auto rounded-md bg-surface p-3 text-[11px] leading-relaxed text-ink-soft">
                {t("dns.diff.before")}: {report.change.before ? JSON.stringify(report.change.before) : "—"}
                {"\n"}
                {t("dns.diff.after")}: {report.change.after ? JSON.stringify(report.change.after) : "—"}
              </pre>
            </div>
          )}
          {report?.kind === "applied" && report.change && (
            <p role="status" className="border-t border-border px-4 py-3 text-xs text-ok">
              {report.change.action === "create" ? t("dns.diff.create") : t("dns.diff.update")}:{" "}
              {report.change.after?.name}/{report.change.after?.type}
            </p>
          )}
          {report?.kind === "deleted" && (
            <p role="status" className="border-t border-border px-4 py-3 text-xs text-ok">
              {t("dns.records.delete")} ✓
            </p>
          )}
        </section>
      )}

      <ConfirmDialog
        open={pendingDelete !== null}
        title={t("common.confirm.title")}
        description={t("dns.providers.deleteConfirm")}
        confirmLabel={t("common.confirm.confirm")}
        cancelLabel={t("common.cancel")}
        danger
        onConfirm={() => {
          const id = pendingDelete?.id;
          setPendingDelete(null);
          if (id) void deleteProvider(id);
        }}
        onCancel={() => setPendingDelete(null)}
      />
    </div>
  );
}
