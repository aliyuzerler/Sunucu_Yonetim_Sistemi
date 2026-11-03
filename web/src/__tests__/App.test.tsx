import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { App } from "../App";
import { LocaleProvider } from "../i18n/LocaleProvider";
import { AuthProvider } from "../auth/AuthProvider";
import { apiFetch } from "../api/client";

// API istemcisi testlerde mock'lanır; bileşenler sözleşmeye göre test edilir.
// StrictMode çift-mount olduğundan sıra-tabanlı değil yol-tabanlı mock kullanılır.
vi.mock("../api/client", () => ({
  apiFetch: vi.fn(),
  setCsrfToken: vi.fn(),
  setUnauthorizedHandler: vi.fn(),
  isApiError: (e: unknown) =>
    typeof e === "object" && e !== null && "code" in e && "status" in e,
}));

const mockFetch = vi.mocked(apiFetch);

function routeJson(routes: Record<string, unknown | Error>) {
  return async (path: unknown) => {
    const key = String(path);
    if (key in routes) {
      const v = routes[key];
      if (v instanceof Error) throw v;
      return v;
    }
    throw new Error(`beklenmeyen istek: ${key}`);
  };
}

const sessionRoute = {
  "/api/v1/auth/session": {
    user_id: "u1",
    username: "admin",
    session_id: "s1",
    server_time: "2026-01-01T00:00:00Z",
  },
  "/api/v1/auth/csrf": { csrf_token: "csrf-1" },
};

function renderApp() {
  return render(
    <LocaleProvider>
      <AuthProvider>
        <App />
      </AuthProvider>
    </LocaleProvider>,
  );
}

beforeEach(() => {
  mockFetch.mockReset();
  // m.4.5: Dil tercihi artık kalıcı — testler arası sızıntıyı önle.
  window.localStorage.clear();
  window.location.hash = "";
});

describe("App kabuğu", () => {
  it("oturum yoksa giriş sayfasını gösterir", async () => {
    mockFetch.mockImplementation(
      routeJson({
        "/api/v1/auth/session": Object.assign(
          new Error("Oturum gerekli."),
          { code: "unauthorized", status: 401 },
        ),
      }),
    );
    renderApp();
    await waitFor(() => {
      expect(screen.getByLabelText("Oturum açma")).toBeInTheDocument();
    });
  });

  it("oturum varsa gezinme gruplarını Türkçe render eder", async () => {
    mockFetch.mockImplementation(routeJson(sessionRoute));
    renderApp();
    await waitFor(() => {
      expect(screen.getByRole("navigation", { name: "Ana gezinme" })).toBeInTheDocument();
    });
    for (const group of ["Genel Bakış", "Altyapı", "Hosting", "Operasyonlar", "Ayarlar"]) {
      expect(screen.getAllByText(group).length).toBeGreaterThan(0);
    }
    expect(screen.getByRole("button", { name: /Sunucular/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Görevler ve Hareketler/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Denetim Kaydı/ })).toBeInTheDocument();
  });

  it("dil değiştirildiğinde gezinme İngilizceye döner", async () => {
    const user = userEvent.setup();
    mockFetch.mockImplementation(routeJson(sessionRoute));
    renderApp();
    await waitFor(() => {
      expect(screen.getByRole("button", { name: "EN" })).toBeInTheDocument();
    });
    await user.click(screen.getByRole("button", { name: "EN" }));
    expect(screen.getAllByText("Infrastructure").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Operations").length).toBeGreaterThan(0);
  });

  it("oturumlu kullanıcıya dürüst genel bakış gösterir (sahte metrik yok)", async () => {
    mockFetch.mockImplementation(
      routeJson({ ...sessionRoute, "/api/v1/overview": { nodes: 0, tasks: 0 } }),
    );
    renderApp();
    await waitFor(() => {
      expect(screen.getAllByText("0").length).toBeGreaterThan(0);
    });
    expect(screen.getAllByText("İzleme modülü etkin değil").length).toBeGreaterThan(0);
  });

  it("henüz uygulanmamış modül faz bilgisiyle açıklama gösterir", async () => {
    const user = userEvent.setup();
    mockFetch.mockImplementation(routeJson(sessionRoute));
    renderApp();
    await waitFor(() => {
      expect(screen.getByRole("button", { name: /Docker/ })).toBeInTheDocument();
    });
    await user.click(screen.getByRole("button", { name: /Docker/ }));
    expect(screen.getByText("Bu modül henüz uygulanmadı")).toBeInTheDocument();
    // Ana içerikteki açıklama hem modülü hem fazı dürüstçe belirtir.
    expect(
      screen.getByText(/docker modülü yol haritasında Faz 7 planında/),
    ).toBeInTheDocument();
  });
});
