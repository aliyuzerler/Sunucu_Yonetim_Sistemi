import { createContext, useCallback, useContext, useMemo, useState } from "react";
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

export function LocaleProvider({ children }: { children: ReactNode }) {
  const [locale, setLocale] = useState<Locale>("tr");

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

  const value = useMemo(() => ({ locale, setLocale, t }), [locale, t]);
  return <LocaleContext.Provider value={value}>{children}</LocaleContext.Provider>;
}

export function useI18n(): LocaleContextValue {
  const ctx = useContext(LocaleContext);
  if (!ctx) {
    throw new Error("useI18n, LocaleProvider içinde kullanılmalıdır");
  }
  return ctx;
}
