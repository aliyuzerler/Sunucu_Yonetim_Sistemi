import { useEffect, useState } from "react";
import { apiFetch, isApiError } from "../api/client";
import { EmptyState } from "../components/EmptyState";
import { StatusBadge } from "../components/StatusBadge";
import { useI18n } from "../i18n/LocaleProvider";

interface OverviewSummary {
  nodes: number;
  tasks: number;
}

export function OverviewPage() {
  const { t } = useI18n();
  const [summary, setSummary] = useState<OverviewSummary | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const res = await apiFetch<OverviewSummary>("/api/v1/overview");
        if (!cancelled && res) setSummary(res);
      } catch (err) {
        if (!cancelled) setError(isApiError(err) ? err.message : t("common.error.generic"));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [t]);

  return (
    <div className="flex flex-col gap-4 p-6">
      <div>
        <p className="text-[11px] text-ink-soft">{t("overview.breadcrumb")}</p>
        <h1 className="text-xl font-semibold text-ink">{t("overview.title")}</h1>
      </div>

      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
        <section aria-label={t("overview.card.nodes")} className="rounded-xl border border-border bg-panel p-4 shadow-sm">
          <h2 className="mb-2 text-sm font-medium text-ink">{t("overview.card.nodes")}</h2>
          {error ? (
            <p role="alert" className="px-4 py-6 text-xs text-err">{error}</p>
          ) : summary === null ? (
            <p className="px-4 py-6 text-xs text-ink-soft">{t("common.loading")}</p>
          ) : (
            <div className="flex flex-col gap-2 px-4 py-6">
              <p className="text-2xl font-semibold text-ink">{summary.nodes}</p>
              <p className="text-xs leading-relaxed text-ink-soft">
                {summary.nodes === 0
                  ? t("overview.nodes.empty.description")
                  : t("overview.nodes.description")}
              </p>
            </div>
          )}
        </section>

        <section aria-label={t("overview.card.tasks")} className="rounded-xl border border-border bg-panel p-4 shadow-sm">
          <h2 className="mb-2 text-sm font-medium text-ink">{t("overview.card.tasks")}</h2>
          {error ? (
            <p className="px-4 py-6 text-xs text-err">{error}</p>
          ) : summary === null ? (
            <p className="px-4 py-6 text-xs text-ink-soft">{t("common.loading")}</p>
          ) : (
            <div className="flex flex-col gap-2 px-4 py-6">
              <p className="text-2xl font-semibold text-ink">{summary.tasks}</p>
              <p className="text-xs leading-relaxed text-ink-soft">{t("overview.tasks.description")}</p>
            </div>
          )}
        </section>

        <section aria-label={t("overview.card.status")} className="rounded-xl border border-border bg-panel p-4 shadow-sm">
          <h2 className="mb-2 text-sm font-medium text-ink">{t("overview.card.status")}</h2>
          <div className="flex flex-col gap-2 px-4 py-6">
            <StatusBadge variant="neutral">{t("overview.status.badge")}</StatusBadge>
            <p className="text-xs leading-relaxed text-ink-soft">{t("overview.status.description")}</p>
          </div>
        </section>
      </div>

      <section aria-label={t("overview.activity.title")} className="rounded-xl border border-border bg-panel p-4 shadow-sm">
        <h2 className="mb-2 text-sm font-medium text-ink">{t("overview.activity.title")}</h2>
        {summary !== null && summary.tasks === 0 ? (
          <EmptyState
            title={t("overview.tasks.empty.title")}
            description={t("overview.tasks.empty.description")}
          />
        ) : (
          <p className="px-4 py-6 text-xs text-ink-soft">{t("overview.activity.description")}</p>
        )}
      </section>
    </div>
  );
}
