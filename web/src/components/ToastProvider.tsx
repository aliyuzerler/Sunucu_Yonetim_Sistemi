import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import { useI18n } from "../i18n/LocaleProvider";

// UX denetimi m.4.3: window.alert/confirm çağırma diline alternatif,
// erişilebilir bildirim sistemi. Kullanıcı eylemlerinin sonucu (silme,
// iptal, onay) artık sağ altta kısa ömürlü bildirimle raporlanır.
//
// Erişilebilirlik: her bildirim role="status" + aria-live="polite" bölgede
// yaşar; ekran okuyucu yazılımı kendini kapatmayan içeriği duyar.
// Otomatik kaybolma 5 sn'dir ve kapatma butonu her zaman vardır.

export type ToastKind = "info" | "ok" | "err";

interface ToastItem {
  id: number;
  kind: ToastKind;
  message: string;
}

interface ToastContextValue {
  showToast: (message: string, kind?: ToastKind) => void;
}

const ToastContext = createContext<ToastContextValue | null>(null);

const TOAST_TTL_MS = 5000;
const MAX_TOASTS = 4;

let nextToastId = 1;

const kindClass: Record<ToastKind, string> = {
  info: "border-border bg-panel text-ink",
  ok: "border-ok/40 bg-ok-soft text-ok",
  err: "border-err/40 bg-err-soft text-err",
};

export function ToastProvider({ children }: { children: ReactNode }) {
  const { t } = useI18n();
  const [toasts, setToasts] = useState<ToastItem[]>([]);
  const timers = useRef<number[]>([]);

  const dismiss = useCallback((id: number) => {
    setToasts((prev) => prev.filter((x) => x.id !== id));
  }, []);

  const showToast = useCallback(
    (message: string, kind: ToastKind = "info") => {
      const id = nextToastId++;
      setToasts((prev) => [...prev.slice(-(MAX_TOASTS - 1)), { id, kind, message }]);
      const timer = window.setTimeout(() => dismiss(id), TOAST_TTL_MS);
      timers.current.push(timer);
    },
    [dismiss],
  );

  useEffect(() => {
    const pending = timers.current;
    return () => {
      for (const timer of pending) window.clearTimeout(timer);
    };
  }, []);

  const value = useMemo(() => ({ showToast }), [showToast]);

  return (
    <ToastContext.Provider value={value}>
      {children}
      <div
        aria-label={t("common.toast.dismiss")}
        className="pointer-events-none fixed bottom-4 right-4 z-50 flex w-80 flex-col gap-2"
      >
        {toasts.map((toast) => (
          <div
            key={toast.id}
            role="status"
            aria-live="polite"
            data-testid="toast"
            data-toast-kind={toast.kind}
            className={`pointer-events-auto flex items-start gap-2 rounded-lg border px-3 py-2 text-xs shadow-md ${kindClass[toast.kind]}`}
          >
            <span className="min-w-0 flex-1">{toast.message}</span>
            <button
              type="button"
              onClick={() => dismiss(toast.id)}
              aria-label={t("common.toast.dismiss")}
              className="shrink-0 rounded px-1 text-base leading-none opacity-60 hover:opacity-100 focus-visible:outline-2 focus-visible:outline-accent"
            >
              ×
            </button>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}

export function useToast(): ToastContextValue {
  const ctx = useContext(ToastContext);
  if (!ctx) {
    throw new Error("useToast, ToastProvider içinde kullanılmalıdır");
  }
  return ctx;
}
