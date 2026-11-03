import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";
import { tr } from "./tr";
import type { Locale, TranslationKey } from "./tr";
import { en } from "./en";

interface LocaleContextValue {
  locale: Locale;
  setLocale: (l: Locale) => void;
  t: (key: TranslationKey, vars?: Record<string, string | number>) => string;
}

const LocaleContext = createContext<LocaleContextValue | null>(null);

const dictionaries: Record<Locale, Record<TranslationKey, string>> = { tr, en };

// UX denetimi m.4.5: Dil tercihi artık kalıcıdır.
//   1) localStorage ("servercontrol.locale") — kullanıcı açıkça seçtinse,
//   2) tarayıcı dili (navigator.language) — ilk ziyarette,
//   3) "tr" — her şey başarısız olursa (proje dili).
// Okuma/yazma try/catch ile sarılır: gizli mod veya kapalı depolama
// durumunda uygulama çalışmaya devam eder, tercih yalnız oturuma özgü olur.
const LOCALE_STORAGE_KEY = "servercontrol.locale";

function readInitialLocale(): Locale {
  try {
    const stored = window.localStorage.getItem(LOCALE_STORAGE_KEY);
    if (stored === "tr" || stored === "en") return stored;
  } catch {
    // localStorage kullanılamıyor — tarayıcı diline düş.
  }
  try {
    if (typeof navigator !== "undefined" && navigator.language?.toLowerCase().startsWith("tr")) {
      return "tr";
    }
  } catch {
    // navigator erişilemiyor — varsayılan.
  }
  return "tr";
}

function persistLocale(l: Locale) {
  try {
    window.localStorage.setItem(LOCALE_STORAGE_KEY, l);
  } catch {
    // Depolama yazılamıyor — tercih yalnız oturuma özgü kalır.
  }
}

function applyDocumentLang(l: Locale) {
  try {
    document.documentElement.lang = l === "tr" ? "tr-TR" : "en-US";
  } catch {
    // jsdom dışı/kısıtlı ortamda sessiz geç.
  }
}

export function LocaleProvider({ children }: { children: ReactNode }) {
  const [locale, setLocaleState] = useState<Locale>(readInitialLocale);

  const setLocale = useCallback((l: Locale) => {
    setLocaleState(l);
    persistLocale(l);
    applyDocumentLang(l);
  }, []);

  useEffect(() => {
    applyDocumentLang(locale);
  }, [locale]);

  const t = useCallback(
    (key: TranslationKey, vars?: Record<string, string | number>): string => {
      let text: string = dictionaries[locale][key] ?? key;
      if (vars) {
        for (const [name, value] of Object.entries(vars)) {
          text = text.replaceAll(`{${name}}`, String(value));
        }
      }
      return text;
    },
    [locale],
  );

  const value = useMemo(() => ({ locale, setLocale, t }), [locale, setLocale, t]);
  return <LocaleContext.Provider value={value}>{children}</LocaleContext.Provider>;
}

export function useI18n(): LocaleContextValue {
  const ctx = useContext(LocaleContext);
  if (!ctx) {
    throw new Error("useI18n, LocaleProvider içinde kullanılmalıdır");
  }
  return ctx;
}
