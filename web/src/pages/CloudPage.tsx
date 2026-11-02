import { useCallback, useEffect, useState } from "react";
import type { FormEvent } from "react";
import { apiFetch, isApiError } from "../api/client";
import type { CloudProvider, CloudResource, CloudVerifyReport } from "../api/client";
import { EmptyState } from "../components/EmptyState";
import { StatusBadge } from "../components/StatusBadge";
import type { BadgeVariant } from "../components/StatusBadge";
import { useI18n } from "../i18n/LocaleProvider";

// CloudPage: first cloud provider adapter (S3-compatible). Kriter 5
// discipline mirrored in the UI: the permission matrix is surfaced verbatim,
// credentials are write-only, and resource deletion requires an explicit
// two-step confirmation (the server enforces the same gate independently).
export function CloudPage() {
  const { t } = useI18n();
  const [providers, setProviders] = useState<CloudProvider[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [reloadKey, setReloadKey] = useState(0);

  const reload = useCallback(() => setReloadKey((k) => k + 1), []);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const res = await apiFetch<{ providers: CloudProvider[] }>("/api/v1/cloud-providers");
        if (cancelled) return;
        setProviders(res.providers);
        setError(null);
      } catch (err) {
        if (!cancelled) setError(isApiError(err) ? err.message : t("common.error.generic"));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [reloadKey, t]);

  return (
    <div className="flex flex-col gap-4 p-6">
      <div className="flex items-center justify-between">
        <div>
          <p className="text-[11px] text-ink-soft">{t("nav.group.settings")}</p>
          <h1 className="text-xl font-semibold text-ink">{t("nav.item.integrations")}</h1>
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

      <ProviderSection providers={providers} onCreated={reload} />
    </div>
  );
}

function ProviderSection(props: {
  providers: CloudProvider[] | null;
  onCreated: () => void;
}) {
  const { t, locale } = useI18n();
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [reports, setReports] = useState<Record<string, CloudVerifyReport>>({});
  const [resources, setResources] = useState<Record<string, CloudResource[]>>({});
  const [busy, setBusy] = useState<string | null>(null);
  const [confirmName, setConfirmName] = useState<string | null>(null);

  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    setError(null);
    setNotice(null);
    const formEl = e.currentTarget;
    const form = new FormData(formEl);
    const body = {
      name: String(form.get("name") ?? "").trim(),
      kind: "s3",
      endpoint: String(form.get("endpoint") ?? "").trim(),
      region: String(form.get("region") ?? "").trim(),
      credentials: {
        access_key_id: String(form.get("access_key_id") ?? ""),
        secret_access_key: String(form.get("secret_access_key") ?? ""),
      },
    };
    setBusy("create");
    try {
      await apiFetch("/api/v1/cloud-providers", { method: "POST", body: JSON.stringify(body) });
      formEl.reset();
      setNotice(t("cloud.created"));
      props.onCreated();
    } catch (err) {
      setError(isApiError(err) ? err.message : t("common.error.generic"));
    } finally {
      setBusy(null);
    }
  }

  async function verify(id: string) {
    setError(null);
    setNotice(null);
    setBusy(`verify:${id}`);
    try {
      const report = await apiFetch<CloudVerifyReport>(`/api/v1/cloud-providers/${id}/verify`, { method: "POST" });
      setReports((prev) => ({ ...prev, [id]: report }));
    } catch (err) {
      setError(isApiError(err) ? err.message : t("common.error.generic"));
    } finally {
      setBusy(null);
    }
  }

  async function listResources(id: string) {
    setError(null);
    setBusy(`resources:${id}`);
    try {
      const res = await apiFetch<{ resources: CloudResource[] }>(`/api/v1/cloud-providers/${id}/resources?limit=100`);
      setResources((prev) => ({ ...prev, [id]: res.resources }));
    } catch (err) {
      setError(isApiError(err) ? err.message : t("common.error.generic"));
    } finally {
      setBusy(null);
    }
  }

  async function deleteResource(providerId: string, name: string) {
    setError(null);
    setNotice(null);
    setBusy(`delete:${name}`);
    try {
      await apiFetch(`/api/v1/cloud-providers/${providerId}/resources/${encodeURIComponent(name)}?confirm=true`, {
        method: "DELETE",
      });
      setNotice(t("cloud.resourceDeleted"));
      setConfirmName(null);
      await listResources(providerId);
    } catch (err) {
      setError(isApiError(err) ? err.message : t("common.error.generic"));
      setConfirmName(null);
    } finally {
      setBusy(null);
    }
  }

  async function deleteProvider(id: string) {
    setError(null);
    try {
      await apiFetch(`/api/v1/cloud-providers/${id}`, { method: "DELETE" });
      setNotice(t("cloud.deleted"));
      props.onCreated();
    } catch (err) {
      setError(isApiError(err) ? err.message : t("common.error.generic"));
    }
  }

  const probeVariant = (ok: boolean): BadgeVariant => (ok ? "ok" : "error");

  return (
    <>
      <section aria-labelledby="cloud-list" className="rounded-xl border border-border bg-panel p-4">
        <h2 id="cloud-list" className="text-sm font-semibold text-ink">
          {t("cloud.providersTitle")}
        </h2>
        <p className="mt-1 text-[11px] text-ink-soft">{t("cloud.providersNote")}</p>
        {(props.providers ?? []).length === 0 ? (
          <div className="mt-3">
            <EmptyState title={t("cloud.none")} description={t("cloud.noneHint")} />
          </div>
        ) : (
          <ul className="mt-3 flex flex-col gap-3">
            {(props.providers ?? []).map((p) => {
              const report = reports[p.id];
              const res = resources[p.id];
              return (
                <li key={p.id} className="rounded-lg border border-border p-3">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="text-sm font-medium text-ink">{p.name}</span>
                    <StatusBadge variant="info">{p.kind}</StatusBadge>
                    <span className="text-[11px] text-ink-soft">{p.endpoint}</span>
                    {p.region && <span className="text-[11px] text-ink-soft">{p.region}</span>}
                  </div>
                  <div className="mt-2 flex flex-wrap gap-2">
                    <button
                      type="button"
                      disabled={busy !== null}
                      onClick={() => void verify(p.id)}
                      className="rounded-lg border border-border bg-panel px-2.5 py-1 text-xs font-medium text-ink hover:bg-neutral-soft focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-50"
                    >
                      {busy === `verify:${p.id}` ? t("cloud.verifying") : t("cloud.verify")}
                    </button>
                    <button
                      type="button"
                      disabled={busy !== null}
                      onClick={() => void listResources(p.id)}
                      className="rounded-lg border border-border bg-panel px-2.5 py-1 text-xs font-medium text-ink hover:bg-neutral-soft focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-50"
                    >
                      {t("cloud.listResources")}
                    </button>
                    <button
                      type="button"
                      onClick={() => void deleteProvider(p.id)}
                      className="rounded-lg border border-border bg-panel px-2.5 py-1 text-xs font-medium text-err hover:bg-err-soft focus-visible:outline-2 focus-visible:outline-accent"
                    >
                      {t("cloud.deleteProvider")}
                    </button>
                  </div>

                  {report && (
                    <div className="mt-3 rounded-lg bg-surface p-3">
                      <p className="text-[11px] font-medium text-ink">{t("cloud.matrixTitle")}</p>
                      <table className="mt-2 w-full text-left text-[11px]">
                        <thead>
                          <tr className="text-ink-soft">
                            <th className="py-1 pr-2">{t("cloud.matrixAction")}</th>
                            <th className="py-1 pr-2">{t("cloud.matrixPermission")}</th>
                            <th className="py-1 pr-2">{t("cloud.matrixRisk")}</th>
                            <th className="py-1">{t("cloud.matrixConfirm")}</th>
                          </tr>
                        </thead>
                        <tbody>
                          {report.matrix.entries.map((m) => (
                            <tr key={m.action} className="text-ink">
                              <td className="py-1 pr-2">{m.action}</td>
                              <td className="py-1 pr-2 font-mono">{m.provider_permission}</td>
                              <td className="py-1 pr-2">{m.risk}</td>
                              <td className="py-1">{m.confirm_required ? t("common.yes") : t("common.no")}</td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                      <p className="mt-2 text-[11px] font-medium text-ink">{t("cloud.probeTitle")}</p>
                      <ul className="mt-1 flex flex-col gap-1">
                        {report.probes.map((pr) => (
                          <li key={pr.action} className="flex items-center gap-2 text-[11px] text-ink">
                            <StatusBadge variant={probeVariant(pr.ok)}>{pr.ok ? t("cloud.probeOk") : t("cloud.probeFail")}</StatusBadge>
                            <span>{pr.action}</span>
                            {pr.reason && <span className="text-ink-soft">{pr.reason}</span>}
                          </li>
                        ))}
                      </ul>
                      <p className="mt-2 text-[10px] text-ink-soft">
                        {new Date(report.checked_at).toLocaleString(locale === "tr" ? "tr-TR" : "en-US")}
                      </p>
                    </div>
                  )}

                  {res && (
                    <div className="mt-3 rounded-lg bg-surface p-3">
                      <p className="text-[11px] font-medium text-ink">{t("cloud.resourcesTitle")}</p>
                      {res.length === 0 ? (
                        <p className="mt-1 text-[11px] text-ink-soft">{t("cloud.noResources")}</p>
                      ) : (
                        <ul className="mt-2 flex flex-col gap-1">
                          {res.map((r) => (
                            <li key={r.name} className="flex flex-wrap items-center gap-2 text-[11px] text-ink">
                              <span className="font-mono">{r.name}</span>
                              <span className="text-ink-soft">{r.kind}</span>
                              {confirmName === r.name ? (
                                <>
                                  <span className="font-medium text-err">{t("cloud.confirmText")}</span>
                                  <button
                                    type="button"
                                    disabled={busy !== null}
                                    onClick={() => void deleteResource(p.id, r.name)}
                                    className="rounded-lg bg-err px-2 py-0.5 text-[11px] font-semibold text-white hover:opacity-90 disabled:opacity-50"
                                  >
                                    {t("cloud.confirmDelete")}
                                  </button>
                                  <button
                                    type="button"
                                    onClick={() => setConfirmName(null)}
                                    className="rounded-lg border border-border px-2 py-0.5 text-[11px] text-ink"
                                  >
                                    {t("common.cancel")}
                                  </button>
                                </>
                              ) : (
                                <button
                                  type="button"
                                  disabled={busy !== null}
                                  onClick={() => setConfirmName(r.name)}
                                  className="rounded-lg border border-border bg-panel px-2 py-0.5 text-[11px] font-medium text-err hover:bg-err-soft disabled:opacity-50"
                                >
                                  {t("cloud.deleteResource")}
                                </button>
                              )}
                            </li>
                          ))}
                        </ul>
                      )}
                      <p className="mt-2 text-[10px] text-ink-soft">{t("cloud.deleteNote")}</p>
                    </div>
                  )}
                </li>
              );
            })}
          </ul>
        )}
        {error && (
          <p role="alert" className="mt-2 rounded-lg bg-err-soft px-3 py-2 text-xs text-err">
            {error}
          </p>
        )}
        {notice && !error && (
          <p role="status" className="mt-2 rounded-lg bg-ok-soft px-3 py-2 text-xs text-ok">
            {notice}
          </p>
        )}
      </section>

      <section aria-labelledby="cloud-create" className="rounded-xl border border-border bg-panel p-4">
        <h2 id="cloud-create" className="text-sm font-semibold text-ink">
          {t("cloud.createTitle")}
        </h2>
        <p className="mt-1 text-[11px] text-ink-soft">{t("cloud.createNote")}</p>
        <form onSubmit={submit} className="mt-3 grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3" aria-label={t("cloud.createTitle")}>
          <input
            name="name"
            required
            minLength={3}
            maxLength={64}
            aria-label={t("cloud.name")}
            placeholder={t("cloud.name")}
            className="rounded-lg border border-border bg-surface px-2.5 py-1.5 text-xs text-ink focus-visible:outline-2 focus-visible:outline-accent"
          />
          <input
            name="endpoint"
            required
            type="url"
            aria-label={t("cloud.endpoint")}
            placeholder="https://..."
            className="rounded-lg border border-border bg-surface px-2.5 py-1.5 text-xs text-ink focus-visible:outline-2 focus-visible:outline-accent"
          />
          <input
            name="region"
            aria-label={t("cloud.region")}
            placeholder={t("cloud.region")}
            className="rounded-lg border border-border bg-surface px-2.5 py-1.5 text-xs text-ink focus-visible:outline-2 focus-visible:outline-accent"
          />
          <input
            name="access_key_id"
            required
            autoComplete="off"
            aria-label={t("cloud.accessKey")}
            placeholder={t("cloud.accessKey")}
            className="rounded-lg border border-border bg-surface px-2.5 py-1.5 text-xs text-ink focus-visible:outline-2 focus-visible:outline-accent"
          />
          <input
            name="secret_access_key"
            required
            type="password"
            autoComplete="new-password"
            aria-label={t("cloud.secretKey")}
            placeholder={t("cloud.secretKey")}
            className="rounded-lg border border-border bg-surface px-2.5 py-1.5 text-xs text-ink focus-visible:outline-2 focus-visible:outline-accent"
          />
          <button
            type="submit"
            disabled={busy === "create"}
            className="rounded-lg bg-accent px-3 py-1.5 text-xs font-semibold text-white hover:opacity-90 focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-50"
          >
            {t("cloud.createSubmit")}
          </button>
        </form>
        <p className="mt-2 text-[11px] text-ink-soft">{t("cloud.writeOnlyNote")}</p>
      </section>
    </>
  );
}
