import { useEffect, useRef, useState } from "react";
import { useI18n } from "../i18n/LocaleProvider";

// UX denetimi m.4.4: Tek seferlik gösterilen token/parola değerleri için
// kopyalama butonu. Kullanıcı artık uzun token'ı fareyle seçmek zorunda
// kalmaz; tek tıkla panoya alınır ve sonuç buton üzerinde dürüstçe bildirilir.
//
// Yazma yolu: önce Clipboard API (navigator.clipboard, yalnız güvenli
// bağlamda var); yoksa geçici textarea + execCommand yedeği. Hiçbiri
// yoksa "kopyalanamadı" durumu gösterilir — sessiz başarı yok.

type CopyState = "idle" | "done" | "failed";

async function writeClipboard(value: string): Promise<boolean> {
  try {
    if (typeof navigator !== "undefined" && navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(value);
      return true;
    }
  } catch {
    // Güvenli olmayan bağlam veya izin reddi — yedeğe düş.
  }
  try {
    const ta = document.createElement("textarea");
    ta.value = value;
    ta.setAttribute("readonly", "");
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.select();
    const ok = document.execCommand("copy");
    document.body.removeChild(ta);
    return ok;
  } catch {
    return false;
  }
}

export function CopyButton({
  value,
  label,
  className = "",
}: {
  value: string;
  /** Varsayılan "Kopyala" etiketi yerine özel etiket (ör. "Komutu kopyala"). */
  label?: string;
  className?: string;
}) {
  const { t } = useI18n();
  const [state, setState] = useState<CopyState>("idle");
  const timer = useRef<number | null>(null);

  useEffect(() => {
    return () => {
      if (timer.current !== null) window.clearTimeout(timer.current);
    };
  }, []);

  async function onCopy() {
    const ok = await writeClipboard(value);
    setState(ok ? "done" : "failed");
    if (timer.current !== null) window.clearTimeout(timer.current);
    // 2 sn sonra "Kopyalandı" geri "Kopyala"ya döner.
    timer.current = window.setTimeout(() => setState("idle"), 2000);
  }

  const text =
    state === "done"
      ? t("common.copy.copied")
      : state === "failed"
        ? t("common.copy.failed")
        : (label ?? t("common.copy.button"));

  return (
    <button
      type="button"
      onClick={() => void onCopy()}
      data-testid="copy-button"
      data-copy-state={state}
      aria-live="polite"
      className={`rounded-lg border border-border bg-panel px-2.5 py-1 text-[11px] font-medium text-ink hover:bg-neutral-soft focus-visible:outline-2 focus-visible:outline-accent ${className}`}
    >
      {text}
    </button>
  );
}
