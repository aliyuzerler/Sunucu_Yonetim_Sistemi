import { useI18n } from "../../i18n/LocaleProvider";
import type { Locale } from "../../i18n/tr";

export function TopBar() {
  const { t, locale, setLocale } = useI18n();

  const localeButton = (target: Locale, label: string) => (
    <button
      type="button"
      onClick={() => setLocale(target)}
      aria-pressed={locale === target}
      className={`rounded-md px-2 py-1 text-xs font-medium transition-colors focus-visible:outline-2 focus-visible:outline-accent ${
        locale === target
          ? "bg-accent text-white"
          : "text-ink-soft hover:bg-accent-soft hover:text-accent"
      }`}
    >
      {label}
    </button>
  );

  return (
    <header className="flex h-12 items-center justify-between border-b border-border bg-panel px-4">
      <span className="text-xs text-ink-soft">{t("topbar.scope")}</span>
      <div className="flex items-center gap-3">
        <div
          role="group"
          aria-label="Dil / Language"
          className="flex items-center gap-1 rounded-lg border border-border bg-surface p-0.5"
        >
          {localeButton("tr", t("topbar.locale.tr"))}
          {localeButton("en", t("topbar.locale.en"))}
        </div>
        <span
          aria-label={t("topbar.user")}
          className="flex h-7 w-7 items-center justify-center rounded-full bg-accent-soft text-xs font-semibold text-accent"
        >
          Y
        </span>
      </div>
    </header>
  );
}
