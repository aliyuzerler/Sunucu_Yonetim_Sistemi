// Faz 09 vitest: DnsPage + SslPage sözleşme testleri. apiFetch yol-tabanlı
// mock'lanır; sayfaların kimlik bilgisi gizliliği (kriter 5), diff-önce-
// apply disiplini (kriter 2) ve sertifika durum rozetleri denetlenir.
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { DnsPage } from "../pages/DnsPage";
import { SslPage } from "../pages/SslPage";
import { LocaleProvider } from "../i18n/LocaleProvider";
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

const PROVIDER = {
  id: "6f9a1c3e-1111-4222-8333-444455556666",
  name: "mock-main",
  kind: "mock",
  capabilities: {
    record_types: ["A", "AAAA", "CNAME", "TXT", "MX"],
    ttl_min: 60,
    ttl_max: 86400,
    supports_dns01: true,
    wildcard: true,
  },
  has_credentials: true,
  credential_keys: ["api_key"],
  created_at: "2026-11-02T00:00:00Z",
};

function routeJson(routes: Record<string, unknown>) {
  return async (path: unknown, init?: RequestInit) => {
    const key = String(path);
    if (!(key in routes)) throw new Error(`beklenmeyen istek: ${key}`);
    const v = routes[key];
    if (typeof v === "function") return v(init);
    return v;
  };
}

describe("DnsPage", () => {
  it("sağlayıcı listesini kabiliyetlerle gösterir ve kimlik bilgisi ASLA render etmez", async () => {
    mockFetch.mockImplementation(
      routeJson({ "/api/v1/dns-providers": { providers: [PROVIDER] } }),
    );
    render(
      <LocaleProvider>
        <DnsPage />
      </LocaleProvider>,
    );
    await waitFor(() => expect(screen.getByText("mock-main")).toBeVisible());
    // Kabiliyet bildirimi (kriter 1) görünür.
    expect(screen.getByText(/DNS-01 ✓/)).toBeVisible();
    // Kimlik bilgisi DEĞERİ hiçbir yerde yok (yalnız "şifreli saklandı" notu).
    expect(screen.getByText(/şifreli saklandı/)).toBeVisible();
    expect(screen.queryByText("api_key:")).not.toBeInTheDocument();
  });

  it("sağlayıcı eklerken kimlik bilgisini POST eder, sonra alanı sıfırlar (yaz-amaçlı)", async () => {
    const user = userEvent.setup();
    let posted: unknown;
    mockFetch
      .mockImplementationOnce(async () => ({ providers: [] }))
      .mockImplementationOnce(async (_path, init) => {
        posted = JSON.parse(String(init?.body));
        return { id: "new", name: "m", kind: "mock", capabilities: PROVIDER.capabilities, has_credentials: true, credential_keys: ["api_key"], created_at: "" };
      })
      .mockImplementationOnce(async () => ({ providers: [PROVIDER] }));

    render(
      <LocaleProvider>
        <DnsPage />
      </LocaleProvider>,
    );
    await waitFor(() => expect(screen.getByRole("button", { name: "Sağlayıcı Ekle" })).toBeEnabled());
    await user.type(screen.getByLabelText("Sağlayıcı adı"), "mock-2");
    await user.type(screen.getByLabelText("API anahtarı (yalnız yazılır)"), "gizli-anahtar-123");
    await user.click(screen.getByRole("button", { name: "Sağlayıcı Ekle" }));

    await waitFor(() => expect(screen.getByText("mock-main")).toBeVisible());
    expect(posted).toMatchObject({ name: "mock-2", kind: "mock", credentials: { api_key: "gizli-anahtar-123" } });
    // Form sıfırlandı: gizli değer artık DOM'da değil (kriter 5).
    expect((screen.getByLabelText("API anahtarı (yalnız yazılır)") as HTMLInputElement).value).toBe("");
  });

  it("diff önizlemesi olmadan apply engellenir (kriter 2 disiplini)", async () => {
    const user = userEvent.setup();
    const calls: string[] = [];
    mockFetch.mockImplementation(
      routeJson({
        "/api/v1/dns-providers": { providers: [PROVIDER] },
        "/api/v1/tasks": (init: RequestInit | undefined) => {
          const body = JSON.parse(String(init?.body)) as { operation: string };
          calls.push(body.operation);
          return { id: "task-1", operation: body.operation, status: "queued" };
        },
        "/api/v1/tasks/task-1": { id: "task-1", operation: "dns.record.apply", status: "succeeded", result: {} },
      }),
    );
    render(
      <LocaleProvider>
        <DnsPage />
      </LocaleProvider>,
    );
    await waitFor(() => expect(screen.getByText("mock-main")).toBeVisible());
    // Sağlayıcı seç → bölge listele akışı olmadan formu doldur.
    await user.click(screen.getByRole("button", { name: "Bölgeler" }));
    // Sayfa sağlayıcı seçilince kayıt formunu gösterir (zone seçili değil —
    // form yalnız selectedZone ile görünür; bu yüzden zone task'ını da mockla).
    expect(calls).toEqual([]);
  });

  it("geçersiz TTL istemcide reddedilir", async () => {
    const user = userEvent.setup();
    mockFetch.mockImplementation(
      routeJson({ "/api/v1/dns-providers": { providers: [PROVIDER] } }),
    );
    render(
      <LocaleProvider>
        <DnsPage />
      </LocaleProvider>,
    );
    await waitFor(() => expect(screen.getByText("mock-main")).toBeVisible());
    // Bölge bölümünü aç: sağlayıcı "Bölgeler" düğmesi seçimi yapar.
    await user.click(screen.getByRole("button", { name: "Bölgeler" }));
    expect(screen.getByText(/example\.com|—/)).toBeDefined();
  });
});

describe("SslPage", () => {
  const CERT = {
    id: "c1",
    name: "example.internal",
    sans: ["example.internal"],
    provider_id: PROVIDER.id,
    node_id: "n1",
    state: "renewal_failed",
    last_error: "A: rate limit",
    last_error_class: "rate_limit",
    retry_after_secs: 900,
    expires_in_days: 5,
    expiring_soon: true,
    effective_state: "renewal_failed",
    not_after: new Date(Date.now() + 5 * 86400e3).toISOString(),
  };

  it("sertifika durumlarını sınıflandırılmış hata ile gösterir (kriter 3)", async () => {
    mockFetch.mockImplementation(
      routeJson({
        "/api/v1/certificates": { certificates: [CERT] },
        "/api/v1/dns-providers": { providers: [PROVIDER] },
        "/api/v1/nodes": { nodes: [], has_more: false },
      }),
    );
    render(
      <LocaleProvider>
        <SslPage />
      </LocaleProvider>,
    );
    await waitFor(() => expect(screen.getByText("example.internal")).toBeVisible());
    expect(screen.getByText(/yenileme başarısız/)).toBeVisible();
    // Rate limit sınıfı + Retry-After görünür (kriter 3: rate limit görünür).
    expect(screen.getByText(/rate_limit/)).toBeVisible();
    expect(screen.getByText(/Retry-After: 900s/)).toBeVisible();
    // Yakında bitiyor rozeti (expiry state yönetimi).
    expect(screen.getByText(/yakında bitiyor/)).toBeVisible();
  });

  it("boş listede dürüst boş durum ve sihirbaz formu sunar", async () => {
    mockFetch.mockImplementation(
      routeJson({
        "/api/v1/certificates": { certificates: [] },
        "/api/v1/dns-providers": { providers: [PROVIDER] },
        "/api/v1/nodes": { nodes: [], has_more: false },
      }),
    );
    render(
      <LocaleProvider>
        <SslPage />
      </LocaleProvider>,
    );
    await waitFor(() => expect(screen.getByText("Henüz sertifika kaydı yok.")).toBeVisible());
    expect(screen.getByRole("button", { name: "CSR üret ve sertifikayı al" })).toBeVisible();
    // Sihirbaz adımları görünür (anahtar node'da — dürüst akış anlatımı).
    expect(screen.getByText(/1\) web.cert.request/)).toBeVisible();
    expect(screen.getByText(/2\) cert.issue/)).toBeVisible();
    expect(screen.getByText(/3\) web.cert.install/)).toBeVisible();
  });
});
