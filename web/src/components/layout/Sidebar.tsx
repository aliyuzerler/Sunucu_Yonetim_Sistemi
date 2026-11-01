import { navGroups, modulePhase } from "../../navigation/navItems";
import { useI18n } from "../../i18n/LocaleProvider";

export function Sidebar({
  active,
  onNavigate,
}: {
  active: string;
  onNavigate: (id: string) => void;
}) {
  const { t } = useI18n();
  return (
    <nav aria-label="Ana gezinme" className="flex h-full w-56 shrink-0 flex-col overflow-y-auto bg-sidebar px-3 py-4">
      <div className="mb-4 px-2">
        <p className="text-sm font-semibold text-white">{t("app.title")}</p>
        <p className="text-[11px] text-sidebar-ink">{t("app.subtitle")}</p>
      </div>
      {navGroups.map((group) => (
        <div key={group.id} className="mb-3">
          <p className="px-2 pb-1 text-[10px] font-semibold uppercase tracking-wider text-sidebar-ink">
            {t(group.key)}
          </p>
          <ul>
            {group.items.map((item) => {
              const phase = modulePhase[item.id] ?? 0;
              const isActive = active === item.id;
              return (
                <li key={item.id}>
                  <button
                    type="button"
                    onClick={() => onNavigate(item.id)}
                    aria-current={isActive ? "page" : undefined}
                    className={`flex w-full items-center justify-between rounded-md px-2 py-1.5 text-left text-[13px] transition-colors focus-visible:outline-2 focus-visible:outline-accent ${
                      isActive
                        ? "bg-white/10 font-medium text-white"
                        : "text-sidebar-ink hover:bg-white/5 hover:text-white"
                    }`}
                  >
                    <span>{t(item.key)}</span>
                    {phase > 0 && (
                      <span className="rounded-full bg-white/10 px-1.5 py-0.5 text-[10px] text-sidebar-ink">
                        {t("nav.phaseChip", { phase })}
                      </span>
                    )}
                  </button>
                </li>
              );
            })}
          </ul>
        </div>
      ))}
    </nav>
  );
}
