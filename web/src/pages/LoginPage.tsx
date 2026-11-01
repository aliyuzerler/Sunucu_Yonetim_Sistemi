import { useState } from "react";
import type { FormEvent } from "react";
import { useAuth } from "../auth/AuthProvider";
import { useI18n } from "../i18n/LocaleProvider";
import { isApiError } from "../api/client";

export function LoginPage() {
  const { t } = useI18n();
  const { login } = useAuth();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (pending || !username.trim() || !password) return;
    setPending(true);
    setError(null);
    try {
      await login(username.trim(), password);
    } catch (err) {
      setError(isApiError(err) ? err.message : t("login.error.generic"));
    } finally {
      setPending(false);
    }
  }

  return (
    <div className="flex h-screen items-center justify-center bg-surface p-6">
      <div className="w-full max-w-sm rounded-xl border border-border bg-panel p-6 shadow-sm">
        <p className="text-sm font-semibold text-ink">{t("app.title")}</p>
        <p className="mb-5 text-[11px] text-ink-soft">{t("app.subtitle")}</p>

        <form onSubmit={onSubmit} className="flex flex-col gap-3" aria-label={t("login.title")}>
          <label className="flex flex-col gap-1">
            <span className="text-xs font-medium text-ink">{t("login.username")}</span>
            <input
              type="text"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              autoComplete="username"
              required
              minLength={3}
              maxLength={32}
              className="rounded-lg border border-border bg-surface px-3 py-2 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
            />
          </label>
          <label className="flex flex-col gap-1">
            <span className="text-xs font-medium text-ink">{t("login.password")}</span>
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="current-password"
              required
              minLength={12}
              className="rounded-lg border border-border bg-surface px-3 py-2 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
            />
          </label>

          {error && (
            <p role="alert" className="rounded-lg bg-err-soft px-3 py-2 text-xs text-err">
              {error}
            </p>
          )}

          <button
            type="submit"
            disabled={pending || !username.trim() || !password}
            className="mt-1 rounded-lg bg-accent px-3 py-2 text-sm font-medium text-white transition-colors hover:bg-accent/90 focus-visible:outline-2 focus-visible:outline-accent disabled:cursor-not-allowed disabled:opacity-50"
          >
            {pending ? t("login.pending") : t("login.submit")}
          </button>
        </form>

        <p className="mt-4 text-[11px] leading-relaxed text-ink-soft">{t("login.hint")}</p>
      </div>
    </div>
  );
}
