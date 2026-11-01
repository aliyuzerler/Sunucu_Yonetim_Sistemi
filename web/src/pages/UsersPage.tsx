import { useEffect, useState } from "react";
import { apiFetch, isApiError } from "../api/client";
import { EmptyState } from "../components/EmptyState";
import { StatusBadge } from "../components/StatusBadge";
import { useI18n } from "../i18n/LocaleProvider";

// UsersPage is the Faz 02 read-only identity view. User lifecycle and role
// management UI are future work; the API already enforces RBAC.
interface UserRecord {
  id: string;
  username: string;
  display_name: string;
  status: string;
  created_at: string;
  last_login_at?: string | null;
}

export function UsersPage() {
  const { t } = useI18n();
  const [users, setUsers] = useState<UserRecord[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const res = await apiFetch<{ users: (UserRecord & { display_name: string })[] }>(
          "/api/v1/users",
        );
        if (!cancelled) setUsers(res.users);
      } catch (err) {
        if (!cancelled) setError(isApiError(err) ? err.message : t("common.error.generic"));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [t]);

  const loading = users === null && error === null;

  return (
    <div className="flex flex-col gap-4 p-6">
      <div>
        <p className="text-[11px] text-ink-soft">{t("nav.group.settings")}</p>
        <h1 className="text-xl font-semibold text-ink">{t("nav.item.users")}</h1>
      </div>

      {error && (
        <p role="alert" className="rounded-lg bg-err-soft px-3 py-2 text-xs text-err">
          {error}
        </p>
      )}

      <section aria-label={t("nav.item.users")} className="rounded-xl border border-border bg-panel shadow-sm">
        {loading ? (
          <p className="p-6 text-sm text-ink-soft">{t("common.loading")}</p>
        ) : !users || users.length === 0 ? (
          <EmptyState
            title={t("users.empty.title")}
            description={t("users.empty.description")}
          />
        ) : (
          <table className="w-full text-left text-sm">
            <caption className="sr-only">{t("users.table.caption")}</caption>
            <thead>
              <tr className="border-b border-border text-xs uppercase tracking-wide text-ink-soft">
                <th scope="col" className="px-4 py-2 font-medium">{t("users.col.username")}</th>
                <th scope="col" className="px-4 py-2 font-medium">{t("users.col.status")}</th>
                <th scope="col" className="px-4 py-2 font-medium">{t("users.col.lastLogin")}</th>
              </tr>
            </thead>
            <tbody>
              {users.map((u) => (
                <tr key={u.id} className="border-b border-border/60 last:border-0">
                  <td className="px-4 py-2.5">
                    <p className="font-medium text-ink">{u.username}</p>
                    {u.display_name && <p className="text-[11px] text-ink-soft">{u.display_name}</p>}
                  </td>
                  <td className="px-4 py-2.5">
                    {u.status === "active" ? (
                      <StatusBadge variant="ok">{t("users.status.active")}</StatusBadge>
                    ) : (
                      <StatusBadge variant="warn">{t("users.status.disabled")}</StatusBadge>
                    )}
                  </td>
                  <td className="px-4 py-2.5 text-xs text-ink-soft">
                    {u.last_login_at
                      ? new Date(u.last_login_at).toLocaleString()
                      : t("users.neverLoggedIn")}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      <p className="text-[11px] text-ink-soft">{t("users.scopeNote")}</p>
    </div>
  );
}
