import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { LoginPage } from "../pages/LoginPage";
import { NodesPage } from "../pages/NodesPage";
import { LocaleProvider } from "../i18n/LocaleProvider";
import { AuthProvider } from "../auth/AuthProvider";
import { apiFetch } from "../api/client";

vi.mock("../api/client", () => ({
  apiFetch: vi.fn(),
  setCsrfToken: vi.fn(),
  setUnauthorizedHandler: vi.fn(),
  isApiError: (e: unknown) =>
    typeof e === "object" && e !== null && "code" in e && "status" in e,
}));

const mockFetch = vi.mocked(apiFetch);

beforeEach(() => {
  mockFetch.mockReset();
});

describe("LoginPage", () => {
  it("boş alanlarda gönder düğmesi pasiftir ve hata durumunda mesaj gösterir", async () => {
    const user = userEvent.setup();
    render(
      <LocaleProvider>
        <AuthProvider>
          <LoginPage />
        </AuthProvider>
      </LocaleProvider>,
    );

    const submit = screen.getByRole("button", { name: "Giriş yap" });
    expect(submit).toBeDisabled();

    await user.type(screen.getByLabelText("Kullanıcı adı"), "admin");
    await user.type(screen.getByLabelText("Parola"), "yanlis-parola-123");
    expect(submit).toBeEnabled();

    mockFetch.mockRejectedValueOnce({
      code: "invalid_credentials",
      message: "Kullanıcı adı veya parola hatalı.",
      status: 401,
      correlation_id: "c1",
      retryable: false,
    });
    await user.click(submit);
    await waitFor(() => {
      expect(screen.getByRole("alert")).toHaveTextContent("Kullanıcı adı veya parola hatalı.");
    });
  });
});

describe("NodesPage", () => {
  it("veri yoksa dürüst boş durum ve ekleme formu gösterir", async () => {
    mockFetch
      .mockResolvedValueOnce({ nodes: [], has_more: false })
      .mockResolvedValueOnce({ groups: [] });

    render(
      <LocaleProvider>
        <NodesPage />
      </LocaleProvider>,
    );

    await waitFor(() => {
      expect(screen.getByText("Henüz kayıtlı sunucu yok")).toBeInTheDocument();
    });
    await userEvent.setup().click(screen.getByRole("button", { name: "Sunucu Ekle" }));
    expect(screen.getByText("Henüz grup yok; grup adı yazıp oluşturabilirsiniz.")).toBeInTheDocument();
  });

  it("sunucu listesini durum etiketleriyle tablo halinde gösterir", async () => {
    mockFetch
      .mockResolvedValueOnce({
        nodes: [
          {
            id: "11111111-1111-1111-1111-111111111111",
            display_name: "web-01",
            hostname: "web01.internal",
            os_family: "linux",
            status: "pending",
            tags: ["prod", "web"],
            group_ids: [],
            created_at: "2026-11-01T00:00:00Z",
            updated_at: "2026-11-01T00:00:00Z",
          },
        ],
        has_more: false,
      })
      .mockResolvedValueOnce({ groups: [] });

    render(
      <LocaleProvider>
        <NodesPage />
      </LocaleProvider>,
    );

    await waitFor(() => {
      expect(screen.getByText("web-01")).toBeInTheDocument();
    });
    expect(screen.getByText("web01.internal")).toBeInTheDocument();
    expect(screen.getByText("Beklemede")).toBeInTheDocument();
    expect(screen.getByText("prod")).toBeInTheDocument();
  });
});
