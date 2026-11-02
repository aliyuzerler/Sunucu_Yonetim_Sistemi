import { useCallback, useEffect, useState } from "react";
import { apiFetch, isApiError } from "../api/client";
import type { SecurityControl, SecurityReport } from "../api/client";
import { StatusBadge } from "../components/StatusBadge";
import type { BadgeVariant } from "../components/StatusBadge";
import { useI18n } from "../i18n/LocaleProvider";
import type { TranslationKey } from "../i18n/tr";

// Closed enum of control ids that have a translation. The control list comes
// from the backend; an UNKNOWN id must render the raw id honestly instead of
// guessing a label — it must never silently map to a wrong/untranslated key.
const CONTROL_LABEL_IDS = [
  "admin_bootstrapped",
  "secrets_key_configured",
  "agent_ca_configured",
  "agent_cert_expiry",
  "node_telemetry_freshness",
  "failed_logins_24h",
  "audit_recording_24h",
  "broker_optin_gates",
] as const;

function controlLabelKey(id: string): TranslationKey | null {
  return (CONTROL_LABEL_IDS as readonly string[]).includes(id)
    ? (`security.control.${id}` as TranslationKey)
    : null;
}

// stateVariant maps the honest control states; "unknown" is a first-class
// state and MUST render as unknown (warn-style), never as ok.
export function stateVariant(state: SecurityControl["state"]): BadgeVariant {
  switch (state) {
    case "ok":
      return "ok";
    case "warn":
      return "warn";
    case "alert":
      return "error";
    default:
      return "warn"; // unknown
  }
}

export function SecurityPage() {
  const { t, locale } = useI18n();
  const [report, setReport] = useState<SecurityReport | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [reloadKey, setReloadKey] = useState(0);

  const reload = useCallback(() => setReloadKey((k) => k + 1), []);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const rep = await apiFetch<SecurityReport>("/api/v1/security/overview");
        if (cancelled) return;
        setReport(rep);
        setError(null);
      } catch (err) {
        if (!cancelled) setError(isApiError(err) ? err.message : t("common.error.generic"));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [reloadKey, t]);

  const loading = report === null && error === null;

  return (
    <div className="flex flex-col gap-4 p-6">
      <div className="flex items-center justify-between">
        <div>
          <p className="text-[11px] text-ink-soft">{t("nav.group.operations")}</p>
          <h1 className="text-xl font-semibold text-ink">{t("nav.item.security")}</h1>
        </div>
        <button
          type="button"
          onClick={reload}
          className="rounded-lg border border-border bg-panel px-3 py-1.5 text-xs font-medium text-ink hover:bg-neutral-soft focus-visible:outline-2 focus-visible:outline-accent"
        >
          {t("security.refresh")}
        </button>
      </div>

      {error && (
        <p role="alert" className="rounded-lg bg-err-soft px-3 py-2 text-xs text-err">
          {error}
        </p>
      )}

      <p className="text-xs text-ink-soft">{t("security.honestyNote")}</p>

      <section aria-labelledby="security-controls" className="rounded-xl border border-border bg-panel p-4">
        <h2 id="security-controls" className="text-sm font-semibold text-ink">
          {t("security.controlsTitle")}
          {report && (
            <span className="ml-2 text-[11px] font-normal text-ink-soft">
              {new Date(report.generated_at).toLocaleString(locale === "tr" ? "tr-TR" : "en-US")}
            </span>
          )}
        </h2>
        {loading ? (
          <p className="mt-2 text-xs text-ink-soft">{t("common.loading")}</p>
        ) : report ? (
          <ul className="mt-3 flex flex-col gap-2">
            {report.controls.map((c) => (
              <li key={c.id} className="flex flex-col gap-1 rounded-lg border border-border px-3 py-2 sm:flex-row sm:items-center sm:justify-between">
                <div className="min-w-0">
                  <p className="text-xs font-medium text-ink">{controlLabelKey(c.id) ? t(controlLabelKey(c.id) as TranslationKey) : c.id}</p>
                  <p className="mt-0.5 break-all text-[11px] text-ink-soft">{c.evidence}</p>
                  <p className="mt-0.5 text-[11px] text-ink-soft">
                    {t("security.checkedAt", {
                      time: new Date(c.checked_at).toLocaleString(locale === "tr" ? "tr-TR" : "en-US"),
                    })}
                  </p>
                </div>
                <span className="shrink-0">
                  <StatusBadge variant={stateVariant(c.state)}>
                    {c.state === "unknown" ? t("security.state.unknown") : t(`security.state.${c.state}`)}
                  </StatusBadge>
                </span>
              </li>
            ))}
          </ul>
        ) : null}
      </section>
    </div>
  );
}
