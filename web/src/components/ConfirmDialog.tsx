import { useEffect, useRef } from "react";

// UX denetimi m.4.3: window.confirm yerine gerçek dialog bileşeni.
// Yerel pencere diyaloglarının aksine uygulamanın temasına uyar,
// klavye ile tam çalışır (Odak + Escape) ve ekran okuyucuya doğru
// rol/etiket bildirir.
//
// Güvenlik varsayılanı: odak HER ZAMAN güvenli seçenekte (Vazgeç)
// açılır — Enter'in yanlışlıkla yıkıcı eylemi tetiklememesi için.

export function ConfirmDialog({
  open,
  title,
  description,
  confirmLabel,
  cancelLabel,
  danger = false,
  busy = false,
  onConfirm,
  onCancel,
}: {
  open: boolean;
  title: string;
  description: string;
  confirmLabel: string;
  cancelLabel: string;
  danger?: boolean;
  busy?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  const cancelRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (!open) return;
    cancelRef.current?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onCancel();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onCancel]);

  if (!open) return null;

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
      role="presentation"
      onClick={() => {
        if (!busy) onCancel();
      }}
      data-testid="confirm-backdrop"
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="confirm-dialog-title"
        aria-describedby="confirm-dialog-description"
        className="w-full max-w-md rounded-xl border border-border bg-panel p-4 shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <h2 id="confirm-dialog-title" className="text-sm font-semibold text-ink">
          {title}
        </h2>
        <p id="confirm-dialog-description" className="mt-1 text-xs text-ink-soft">
          {description}
        </p>
        <div className="mt-4 flex justify-end gap-2">
          <button
            ref={cancelRef}
            type="button"
            onClick={onCancel}
            disabled={busy}
            className="rounded-lg border border-border bg-panel px-3 py-1.5 text-xs font-medium text-ink hover:bg-neutral-soft focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-50"
          >
            {cancelLabel}
          </button>
          <button
            type="button"
            onClick={onConfirm}
            disabled={busy}
            data-testid="confirm-dialog-confirm"
            className={
              danger
                ? "rounded-lg border border-err/40 bg-err-soft px-3 py-1.5 text-xs font-semibold text-err hover:bg-err/20 focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-50"
                : "rounded-lg bg-accent px-3 py-1.5 text-xs font-semibold text-white hover:bg-accent/90 focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-50"
            }
          >
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
