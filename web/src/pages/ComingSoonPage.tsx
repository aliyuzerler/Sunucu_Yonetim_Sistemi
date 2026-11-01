import { modulePhase } from "../navigation/navItems";
import { useI18n } from "../i18n/LocaleProvider";

export function ComingSoonPage({ moduleId }: { moduleId: string }) {
  const { t } = useI18n();
  const phase = modulePhase[moduleId] ?? 0;
  return (
    <div className="flex h-full items-center justify-center p-6">
      <div className="max-w-md rounded-xl border border-border bg-panel p-8 text-center shadow-sm">
        <StatusLikeBadge />
        <h1 className="mt-3 text-lg font-semibold text-ink">{t("coming.title")}</h1>
        <p className="mt-2 text-sm leading-relaxed text-ink-soft">
          {t("coming.description", { module: moduleId, phase })}
        </p>
      </div>
    </div>
  );
}

function StatusLikeBadge() {
  return (
    <span
      aria-hidden="true"
      className="mx-auto inline-flex h-10 w-10 items-center justify-center rounded-full bg-info-soft text-lg text-info"
    >
      i
    </span>
  );
}
