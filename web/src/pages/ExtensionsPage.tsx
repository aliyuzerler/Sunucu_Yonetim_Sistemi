import { useCallback, useEffect, useState } from "react";
import { apiFetch, isApiError } from "../api/client";
import type { ExtensionRow } from "../api/client";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { EmptyState } from "../components/EmptyState";
import { StatusBadge } from "../components/StatusBadge";
import type { BadgeVariant } from "../components/StatusBadge";
import { useI18n } from "../i18n/LocaleProvider";

const stateVariant: Record<ExtensionRow["state"], BadgeVariant> = {
  pending: "warn",
  approved: "ok",
  revoked: "error",
};

function riskVariant(risk: string): BadgeVariant {
  switch (risk) {
    case "low":
      return "neutral";
    case "high":
      return "error";
    default:
      return "warn";
  }
}

// ExtensionsPage — Faz 15 eklenti sözleşmesi (ADR-0019): imzalı manifest
// listesi, istenen izinler ve TEK TEK onay akışı. Varsayılan izin YOKTUR:
// kayıt pending gelir ve hiçbir izin etkili değildir; her onay ayrı,
// auditli ve geri alınabilir bir karardır.
export function ExtensionsPage() {
  const { t, locale } = useI18n();
  const [rows, setRows] = useState<ExtensionRow[] | null>(null);
  const [trustEnabled, setTrustEnabled] = useState(true);
  const [manifest, setManifest] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [reloadKey, setReloadKey] = useState(0);
  // UX denetimi m.4.3: window.confirm yerine dialog durumu — bekleyen
  // onay, açıklaması ve yıkıcılık derecesiyle birlikte tutulur.
  const [pendingConfirm, setPendingConfirm] = useState<{
    description: string;
    danger: boolean;
    action: () => void;
  } | null>(null);

  const reload = useCallback(() => setReloadKey((k) => k + 1), []);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const res = await apiFetch<{ extensions: ExtensionRow[]; trust_enabled: boolean }>("/api/v1/extensions");
        if (cancelled) return;
        setRows(res.extensions ?? []);
        setTrustEnabled(res.trust_enabled);
        setError(null);
      } catch (err) {
        if (!cancelled) setError(isApiError(err) ? err.message : t("common.error.generic"));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [reloadKey, t]);

  async function post(path: string, body: unknown) {
    setBusy(true);
    setError(null);
    setNotice(null);
    try {
      await apiFetch(path, { method: "POST", body: JSON.stringify(body) });
      setNotice(t("extensions.done"));
      reload();
    } catch (err) {
      setError(isApiError(err) ? err.message : t("common.error.generic"));
    } finally {
      setBusy(false);
    }
  }

  async function register() {
    setBusy(true);
    setError(null);
    setNotice(null);
    try {
      JSON.parse(manifest); // yerel ön-denetim; gerçek doğrulama CP'de
      await apiFetch("/api/v1/extensions", { method: "POST", body: JSON.stringify({ manifest: JSON.parse(manifest) }) });
      setNotice(t("extensions.registered"));
      setManifest("");
      reload();
    } catch (err) {
      setError(isApiError(err) ? err.message : t("common.error.generic"));
    } finally {
      setBusy(false);
    }
  }

  const loading = rows === null && error === null;

  return (
    <div className="flex flex-col gap-4 p-6">
      <div className="flex items-center justify-between">
        <div>
          <p className="text-[11px] text-ink-soft">{t("nav.group.settings")}</p>
          <h1 className="text-xl font-semibold text-ink">{t("nav.item.modules")}</h1>
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
        {t("extensions.policyNote")}
      </p>

      {!trustEnabled && (
        <p role="status" className="rounded-lg bg-warn-soft px-3 py-2 text-xs text-ink">
          {t("extensions.trustDisabled")}
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

      <section aria-labelledby="ext-register" className="rounded-xl border border-border bg-panel p-4">
        <h2 id="ext-register" className="text-sm font-semibold text-ink">
          {t("extensions.registerTitle")}
        </h2>
        <p className="mt-1 text-[11px] text-ink-soft">{t("extensions.registerNote")}</p>
        <textarea
          aria-label={t("extensions.manifestLabel")}
          value={manifest}
          onChange={(e) => setManifest(e.target.value)}
          rows={6}
          spellCheck={false}
          className="mt-2 w-full rounded-lg border border-border bg-surface p-2 font-mono text-[11px] text-ink focus-visible:outline-2 focus-visible:outline-accent"
          placeholder='{ "manifest_version": "SCP-EXT-MANIFEST-V1", ... }'
        />
        <button
          type="button"
          disabled={busy || manifest.trim() === ""}
          onClick={() => void register()}
          className="mt-2 rounded-lg bg-accent px-3 py-1.5 text-xs font-semibold text-white hover:opacity-90 focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-50"
        >
          {t("extensions.register")}
        </button>
      </section>

      <section aria-labelledby="ext-list" className="rounded-xl border border-border bg-panel p-4">
        <h2 id="ext-list" className="text-sm font-semibold text-ink">
          {t("extensions.listTitle")}
        </h2>
        {loading ? (
          <p className="mt-2 text-xs text-ink-soft">{t("common.loading")}</p>
        ) : (rows ?? []).length === 0 ? (
          <div className="mt-3">
            <EmptyState title={t("extensions.none")} description={t("extensions.noneHint")} />
          </div>
        ) : (
          <ul className="mt-3 flex flex-col gap-3">
            {(rows ?? []).map((e) => (
              <li key={e.id} className="rounded-lg border border-border p-3">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="text-sm font-semibold text-ink">{e.display_name}</span>
                  <span className="font-mono text-[11px] text-ink-soft">{e.name}</span>
                  <StatusBadge variant={stateVariant[e.state]}>{t(`extensions.state.${e.state}`)}</StatusBadge>
                  <StatusBadge variant={e.api_compatible ? "ok" : "error"}>
                    {e.api_compatible ? t("extensions.apiOk") : t("extensions.apiBad")}
                  </StatusBadge>
                  <span className="font-mono text-[11px] text-ink-soft">v{e.version}</span>
                  <span className="text-[11px] text-ink-soft">API {e.api_version}</span>
                </div>
                <p className="mt-1 text-[11px] text-ink-soft">
                  {e.vendor} · SHA256 {e.artifact_sha256.slice(0, 12)}…
                </p>

                <h3 className="mt-2 text-[11px] font-semibold text-ink">{t("extensions.permissions")}</h3>
                {(e.requested_permissions ?? []).length === 0 ? (
                  <p className="mt-1 text-[11px] text-ink-soft">{t("extensions.noPermissions")}</p>
                ) : (
                  <ul className="mt-1 flex flex-col gap-1">
                    {(e.requested_permissions ?? []).map((p) => {
                      const grant = (e.granted_permissions ?? []).find((g) => g.action === p.action);
                      const active = grant !== undefined && grant.revoked_at == null;
                      return (
                        <li key={p.action} className="flex flex-wrap items-center gap-2 rounded-lg border border-border px-2.5 py-1.5">
                          <span className="font-mono text-[11px] text-ink">{p.action}</span>
                          <StatusBadge variant={riskVariant(p.risk)}>{t(`extensions.risk.${p.risk}`)}</StatusBadge>
                          <span className="text-[11px] text-ink-soft">{p.reason}</span>
                          {active ? (
                            <>
                              <StatusBadge variant="ok">{t("extensions.granted")}</StatusBadge>
                              <button
                                type="button"
                                disabled={busy}
                                onClick={() => void post(`/api/v1/extensions/${encodeURIComponent(e.id)}/permissions/revoke`, { action: p.action })}
                                className="ml-auto rounded-lg border border-border bg-panel px-2.5 py-1 text-[11px] font-medium text-ink hover:bg-neutral-soft focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-50"
                              >
                                {t("extensions.revoke")}
                              </button>
                            </>
                          ) : (
                            <button
                              type="button"
                              disabled={busy || e.state === "revoked"}
                              onClick={() => {
                                setPendingConfirm({
                                  description: t("extensions.approveConfirm"),
                                  danger: false,
                                  action: () => void post(`/api/v1/extensions/${encodeURIComponent(e.id)}/permissions/approve`, { action: p.action, confirm: true }),
                                });
                              }}
                              className="ml-auto rounded-lg bg-accent px-2.5 py-1 text-[11px] font-semibold text-white hover:opacity-90 focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-50"
                            >
                              {t("extensions.approve")}
                            </button>
                          )}
                        </li>
                      );
                    })}
                  </ul>
                )}

                {(e.delegated_ops ?? []).length > 0 && (
                  <p className="mt-2 text-[11px] text-ink-soft">
                    {t("extensions.delegatedOps")}: {(e.delegated_ops ?? []).join(", ")}
                  </p>
                )}

                {e.state !== "revoked" && (
                  <button
                    type="button"
                    disabled={busy}
                    onClick={() => {
                      setPendingConfirm({
                        description: t("extensions.revokeConfirm"),
                        danger: true,
                        action: () => void post(`/api/v1/extensions/${encodeURIComponent(e.id)}/revoke`, {}),
                      });
                    }}
                    className="mt-2 rounded-lg border border-err/40 bg-panel px-2.5 py-1 text-[11px] font-medium text-err hover:bg-err-soft focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-50"
                  >
                    {t("extensions.disableExtension")}
                  </button>
                )}
                <p className="mt-1 text-[10px] text-ink-soft">
                  {t("extensions.registeredBy")}: {e.registered_by} · {new Date(e.registered_at).toLocaleString(locale === "tr" ? "tr-TR" : "en-US")}
                </p>
              </li>
            ))}
          </ul>
        )}
      </section>

      <ConfirmDialog
        open={pendingConfirm !== null}
        title={t("common.confirm.title")}
        description={pendingConfirm?.description ?? ""}
        confirmLabel={t("common.confirm.confirm")}
        cancelLabel={t("common.cancel")}
        danger={pendingConfirm?.danger ?? false}
        busy={busy}
        onConfirm={() => {
          const action = pendingConfirm?.action;
          setPendingConfirm(null);
          action?.();
        }}
        onCancel={() => setPendingConfirm(null)}
      />
    </div>
  );
}
