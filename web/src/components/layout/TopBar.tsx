import { useAuth } from "../../auth/AuthProvider";
import { useI18n } from "../../i18n/LocaleProvider";
import type { Locale } from "../../i18n/tr";

export function TopBar() {
  const { t, locale, setLocale } = useI18n();
  const { user, logout } = useAuth();

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
        {user && (
          <div className="flex items-center gap-2">
            <span className="text-xs font-medium text-ink">{user.username}</span>
            <button
              type="button"
              onClick={() => void logout()}
              className="rounded-lg border border-border px-2 py-1 text-xs font-medium text-ink hover:bg-neutral-soft focus-visible:outline-2 focus-visible:outline-accent"
            >
              {t("topbar.logout")}
            </button>
          </div>
        )}
      </div>
    </header>
  );
}
