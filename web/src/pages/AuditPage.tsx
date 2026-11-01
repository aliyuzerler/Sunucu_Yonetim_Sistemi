import { useCallback, useEffect, useState } from "react";
import { apiFetch, isApiError } from "../api/client";
import type { AuditPage } from "../api/client";
import { EmptyState } from "../components/EmptyState";
import { StatusBadge } from "../components/StatusBadge";
import type { BadgeVariant } from "../components/StatusBadge";
import { useI18n } from "../i18n/LocaleProvider";

const resultVariant: Record<AuditPage["entries"][number]["result"], BadgeVariant> = {
  success: "ok",
  failure: "error",
  denied: "warn",
};

function formatTime(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
}

export function AuditPage() {
  const { t } = useI18n();
  const [page, setPage] = useState<AuditPage | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [nextCursor, setNextCursor] = useState<string | undefined>(undefined);
  const [actionFilter, setActionFilter] = useState("");

  const load = useCallback(
    async (cursor?: string, action = "") => {
      try {
        const params = new URLSearchParams({ limit: "50" });
        if (cursor) params.set("cursor", cursor);
        if (action) params.set("action", action);
        const res = await apiFetch<AuditPage>(`/api/v1/audit?${params.toString()}`);
        setPage((prev) =>
          cursor && prev
            ? { ...res, entries: [...prev.entries, ...res.entries] }
            : res,
        );
        setNextCursor(res.has_more ? res.next_cursor : undefined);
        setError(null);
      } catch (err) {
        setError(isApiError(err) ? err.message : t("common.error.generic"));
      }
    },
    [t],
  );

  useEffect(() => {
    // İlk yükleme: canonical async-fetch-in-effect deseni (lint uyumlu).
    let cancelled = false;
    (async () => {
      try {
        const res = await apiFetch<AuditPage>("/api/v1/audit?limit=50");
        if (cancelled) return;
        setPage(res);
        setNextCursor(res.has_more ? res.next_cursor : undefined);
        setError(null);
      } catch (err) {
        if (!cancelled) {
          setError(isApiError(err) ? err.message : t("common.error.generic"));
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [t]);

  // busy yalnızca kullanıcı tetiklemeli yenilemelerde (event handler) kurulur.
  const loadWithBusy = (cursor?: string, action = "") => {
    setBusy(true);
    void load(cursor, action).finally(() => setBusy(false));
  };

  const loading = page === null && error === null;

  return (
    <div className="flex flex-col gap-4 p-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <p className="text-[11px] text-ink-soft">{t("nav.group.operations")}</p>
          <h1 className="text-xl font-semibold text-ink">{t("audit.title")}</h1>
        </div>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            loadWithBusy(undefined, actionFilter.trim().toLowerCase());
          }}
          className="flex gap-2"
          aria-label={t("audit.filterLabel")}
        >
          <input
            value={actionFilter}
            onChange={(e) => setActionFilter(e.target.value)}
            placeholder={t("audit.filterPlaceholder")}
            className="rounded-lg border border-border bg-panel px-3 py-1.5 text-xs text-ink focus-visible:outline-2 focus-visible:outline-accent"
          />
          <button
            type="submit"
            className="rounded-lg border border-border bg-panel px-3 py-1.5 text-xs font-medium text-ink hover:bg-neutral-soft focus-visible:outline-2 focus-visible:outline-accent"
          >
            {t("audit.filterApply")}
          </button>
        </form>
      </div>

      {error && (
        <p role="alert" className="rounded-lg bg-err-soft px-3 py-2 text-xs text-err">
          {error}
        </p>
      )}

      <section aria-label={t("audit.title")} className="rounded-xl border border-border bg-panel shadow-sm">
        {loading && !page ? (
          <p className="p-6 text-sm text-ink-soft">{t("common.loading")}</p>
        ) : !page || page.entries.length === 0 ? (
          <EmptyState
            title={t("audit.empty.title")}
            description={t("audit.empty.description")}
          />
        ) : (
          <table className="w-full text-left text-sm">
            <caption className="sr-only">{t("audit.table.caption")}</caption>
            <thead>
              <tr className="border-b border-border text-xs uppercase tracking-wide text-ink-soft">
                <th scope="col" className="px-4 py-2 font-medium">{t("audit.col.time")}</th>
                <th scope="col" className="px-4 py-2 font-medium">{t("audit.col.actor")}</th>
                <th scope="col" className="px-4 py-2 font-medium">{t("audit.col.action")}</th>
                <th scope="col" className="px-4 py-2 font-medium">{t("audit.col.target")}</th>
                <th scope="col" className="px-4 py-2 font-medium">{t("audit.col.result")}</th>
              </tr>
            </thead>
            <tbody>
              {page.entries.map((e) => (
                <tr key={e.id} className="border-b border-border/60 last:border-0">
                  <td className="px-4 py-2.5 text-xs text-ink-soft">{formatTime(e.created_at)}</td>
                  <td className="px-4 py-2.5 text-ink">{e.actor}</td>
                  <td className="px-4 py-2.5">
                    <code className="text-xs text-ink">{e.action}</code>
                  </td>
                  <td className="px-4 py-2.5 text-xs text-ink-soft">
                    {e.target_type ? `${e.target_type}:${e.target_id.slice(0, 12)}` : "—"}
                  </td>
                  <td className="px-4 py-2.5">
                    <StatusBadge variant={resultVariant[e.result]}>{t(`audit.result.${e.result}`)}</StatusBadge>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      {nextCursor && (
        <button
          type="button"
          onClick={() => loadWithBusy(nextCursor, actionFilter.trim().toLowerCase())}
          disabled={busy}
          className="self-start rounded-lg border border-border bg-panel px-3 py-1.5 text-xs font-medium text-ink hover:bg-neutral-soft focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-50"
        >
          {busy ? t("common.loading") : t("audit.loadMore")}
        </button>
      )}

      <p className="text-[11px] text-ink-soft">{t("audit.appendOnlyNote")}</p>
    </div>
  );
}
