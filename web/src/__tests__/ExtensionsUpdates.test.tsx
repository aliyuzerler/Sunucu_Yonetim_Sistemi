import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ExtensionsPage } from "../pages/ExtensionsPage";
import { UpdatesPage } from "../pages/UpdatesPage";
import { LocaleProvider } from "../i18n/LocaleProvider";
import { apiFetch } from "../api/client";
import type { ExtensionRow, UpdateArtifact } from "../api/client";

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

function routeJson(routes: Record<string, unknown>) {
  return async (path: unknown, init?: RequestInit) => {
    const key = String(path).split("?")[0];
    if (key in routes) {
      const v = routes[key];
      if (v instanceof Error) throw v;
      if (typeof v === "function") return v(String(path), init);
      return v;
    }
    throw new Error(`beklenmeyen istek: ${String(path)}`);
  };
}

const ext: ExtensionRow = {
  id: "66666666-6666-4666-8666-666666666666",
  name: "demo-ext",
  display_name: "Demo Extension",
  vendor: "Example GmbH",
  version: "1.2.3",
  api_version: ">=1.0.0 <2.0.0",
  min_cp_version: "0.15.0",
  artifact_sha256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  state: "pending",
  requested_permissions: [
    { action: "backup:read", risk: "low", reason: "yedek listesini okur" },
  ],
  granted_permissions: [],
  delegated_ops: ["backup.cp.run"],
  capabilities: [],
  api_compatible: true,
  registered_by: "admin",
  registered_at: new Date().toISOString(),
  updated_at: new Date().toISOString(),
};

const artifact: UpdateArtifact = {
  id: "77777777-7777-4777-8777-777777777777",
  target: "agent_linux_amd64",
  version: "0.16.0-rc1",
  file_name: "server-control-agent_0.16.0-rc1_amd64.deb",
  size_bytes: 12_345_678,
  sha256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
  signature: "c29tZS1zaWduYXR1cmUtYmFzZTY0LXN0cmluZy0tLS0tLS0tLS0tLS0tLS0tLS0tLS0tLQ",
  api_version: "1.0.0",
  notes: "RC1 sürümü",
  state: "published",
  published_by: "admin",
  published_at: new Date().toISOString(),
};

describe("ExtensionsPage", () => {
  it("izin taleplerini ve onay akışını gösterir; onay confirm=true ile POST atar", async () => {
    mockFetch.mockImplementation(
      routeJson({ "/api/v1/extensions": { extensions: [ext], trust_enabled: true } }) as never,
    );
    const user = userEvent.setup();

    render(
      <LocaleProvider>
        <ExtensionsPage />
      </LocaleProvider>,
    );

    await waitFor(() => {
      expect(screen.getByText("Demo Extension")).toBeInTheDocument();
    });
    expect(screen.getByText("backup:read")).toBeInTheDocument();
    expect(screen.getByText("yedek listesini okur")).toBeInTheDocument();
    expect(screen.getByText("API uyumlu")).toBeInTheDocument();

    // Onay → POST /permissions/approve confirm=true gövdesiyle.
    mockFetch.mockImplementation(
      routeJson({
        "/api/v1/extensions": { extensions: [ext], trust_enabled: true },
        "/api/v1/extensions/66666666-6666-4666-8666-666666666666/permissions/approve": (
          _path: string,
          init?: RequestInit,
        ) => {
          expect(String(init?.body)).toContain('"confirm":true');
          return { ...ext, granted_permissions: [{ action: "backup:read", risk: "low", granted_by: "admin", granted_at: new Date().toISOString() }] } as never;
        },
      }) as never,
    );
    window.confirm = () => true;
    await user.click(screen.getByRole("button", { name: "Onayla" }));
    await waitFor(() => {
      expect(screen.getByText("İşlem tamamlandı.")).toBeInTheDocument();
    });
  });

  it("güven kökü kapalıyken dürüst uyarı gösterir", async () => {
    mockFetch.mockImplementation(
      routeJson({ "/api/v1/extensions": { extensions: [], trust_enabled: false } }) as never,
    );
    render(
      <LocaleProvider>
        <ExtensionsPage />
      </LocaleProvider>,
    );
    await waitFor(() => {
      expect(screen.getByText(/CP_EXTENSION_TRUST_KEYS/)).toBeInTheDocument();
    });
    expect(screen.getByText("Henüz eklenti kaydı yok")).toBeInTheDocument();
  });
});

describe("UpdatesPage", () => {
  it("artifact listesini gösterir ve iptal onayı ile POST atar", async () => {
    mockFetch.mockImplementation(
      routeJson({
        "/api/v1/update-artifacts": { artifacts: [artifact], trust_enabled: true },
        "/api/v1/nodes": { nodes: [], has_more: false },
      }) as never,
    );
    const user = userEvent.setup();

    render(
      <LocaleProvider>
        <UpdatesPage />
      </LocaleProvider>,
    );

    await waitFor(() => {
      expect(screen.getByText("agent_linux_amd64")).toBeInTheDocument();
    });
    expect(screen.getByText("v0.16.0-rc1")).toBeInTheDocument();
    expect(screen.getByText(/RC1 sürümü/)).toBeInTheDocument();

    mockFetch.mockImplementation(
      routeJson({
        "/api/v1/update-artifacts": { artifacts: [{ ...artifact, state: "revoked" }], trust_enabled: true },
        "/api/v1/nodes": { nodes: [], has_more: false },
        "/api/v1/update-artifacts/77777777-7777-4777-8777-777777777777/revoke": { revoked: true },
      }) as never,
    );
    window.confirm = () => true;
    await user.click(screen.getByRole("button", { name: "İptal et" }));
    await waitFor(() => {
      expect(screen.getByText("Artifact iptal edildi.")).toBeInTheDocument();
    });
  });

  it("node seçilmeden update görevleri devre dışıdır (dürüst boş durum)", async () => {
    mockFetch.mockImplementation(
      routeJson({
        "/api/v1/update-artifacts": { artifacts: [], trust_enabled: true },
        "/api/v1/nodes": { nodes: [], has_more: false },
      }) as never,
    );
    render(
      <LocaleProvider>
        <UpdatesPage />
      </LocaleProvider>,
    );
    await waitFor(() => {
      expect(screen.getByText("Henüz update artifact kaydı yok")).toBeInTheDocument();
    });
    expect(screen.getByRole("button", { name: "Durumu sorgula (update.status)" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Geri al (update.rollback)" })).toBeDisabled();
  });
});
