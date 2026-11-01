import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";
import { apiFetch, setCsrfToken, setUnauthorizedHandler } from "../api/client";
import type { LoginResponse, SessionResponse, SessionUser } from "../api/client";

type AuthStatus = "loading" | "anonymous" | "authenticated";

interface AuthContextValue {
  status: AuthStatus;
  user: SessionUser | null;
  login: (username: string, password: string) => Promise<void>;
  logout: () => Promise<void>;
}

const AuthContext = createContext<AuthContextValue | null>(null);

// CSRF token tarayıcı yenilenmelerinde korunur (sessionStorage). Cookie
// HttpOnly olduğundan token'ı yeniden almak için /auth/csrf uçları vardır.
const CSRF_STORAGE_KEY = "srvctl.csrf";

function readStoredCsrf(): string {
  try {
    return sessionStorage.getItem(CSRF_STORAGE_KEY) ?? "";
  } catch {
    return "";
  }
}

function storeCsrf(token: string): void {
  try {
    if (token) {
      sessionStorage.setItem(CSRF_STORAGE_KEY, token);
    } else {
      sessionStorage.removeItem(CSRF_STORAGE_KEY);
    }
  } catch {
    // sessionStorage kullanılamıyorsa yalnızca bellek içi kullanılır.
  }
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<AuthStatus>("loading");
  const [user, setUser] = useState<SessionUser | null>(null);

  // Oturum durumu yükleme: /auth/session 200 → authenticated; 401 → anonim.
  useEffect(() => {
    let cancelled = false;
    setCsrfToken(readStoredCsrf());
    setUnauthorizedHandler(() => {
      if (!cancelled) {
        setUser(null);
        setStatus("anonymous");
        setCsrfToken("");
        storeCsrf("");
      }
    });

    (async () => {
      try {
        const session = await apiFetch<SessionResponse>("/api/v1/auth/session");
        // Canlı oturum varsa güncel CSRF token'ını al.
        const csrf = await apiFetch<{ csrf_token: string }>("/api/v1/auth/csrf");
        if (cancelled) return;
        setCsrfToken(csrf.csrf_token);
        storeCsrf(csrf.csrf_token);
        setUser({ id: session.user_id, username: session.username });
        setStatus("authenticated");
      } catch {
        if (cancelled) return;
        setUser(null);
        setStatus("anonymous");
      }
    })();

    return () => {
      cancelled = true;
    };
  }, []);

  const login = useCallback(async (username: string, password: string) => {
    const res = await apiFetch<LoginResponse>("/api/v1/auth/login", {
      method: "POST",
      body: JSON.stringify({ username, password }),
    });
    setCsrfToken(res.csrf_token);
    storeCsrf(res.csrf_token);
    setUser(res.user);
    setStatus("authenticated");
  }, []);

  const logout = useCallback(async () => {
    try {
      await apiFetch("/api/v1/auth/logout", { method: "POST" });
    } catch {
      // Logout hatasında da lokal oturum temizlenir.
    }
    setUser(null);
    setStatus("anonymous");
    setCsrfToken("");
    storeCsrf("");
  }, []);

  const value = useMemo(
    () => ({ status, user, login, logout }),
    [status, user, login, logout],
  );
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (!ctx) {
    throw new Error("useAuth, AuthProvider içinde kullanılmalıdır");
  }
  return ctx;
}
