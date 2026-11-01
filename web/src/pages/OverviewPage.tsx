import { EmptyState } from "../components/EmptyState";
import { StatusBadge } from "../components/StatusBadge";
import { useI18n } from "../i18n/LocaleProvider";

export function OverviewPage() {
  const { t } = useI18n();
  return (
    <div className="flex flex-col gap-4 p-6">
      <div>
        <p className="text-[11px] text-ink-soft">{t("overview.breadcrumb")}</p>
        <h1 className="text-xl font-semibold text-ink">{t("overview.title")}</h1>
      </div>

      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
        <section aria-label={t("overview.card.nodes")} className="rounded-xl border border-border bg-panel p-4 shadow-sm">
          <h2 className="mb-2 text-sm font-medium text-ink">{t("overview.card.nodes")}</h2>
          <EmptyState
            title={t("overview.nodes.empty.title")}
            description={t("overview.nodes.empty.description")}
          />
        </section>

        <section aria-label={t("overview.card.tasks")} className="rounded-xl border border-border bg-panel p-4 shadow-sm">
          <h2 className="mb-2 text-sm font-medium text-ink">{t("overview.card.tasks")}</h2>
          <EmptyState
            title={t("overview.tasks.empty.title")}
            description={t("overview.tasks.empty.description")}
          />
        </section>

        <section aria-label={t("overview.card.status")} className="rounded-xl border border-border bg-panel p-4 shadow-sm">
          <h2 className="mb-2 text-sm font-medium text-ink">{t("overview.card.status")}</h2>
          <div className="flex flex-col gap-2 px-4 py-6">
            <StatusBadge variant="neutral">{t("overview.status.badge")}</StatusBadge>
            <p className="text-xs leading-relaxed text-ink-soft">{t("overview.status.description")}</p>
          </div>
        </section>
      </div>
    </div>
  );
}
