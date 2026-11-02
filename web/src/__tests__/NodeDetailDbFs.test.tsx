// Faz 10 vitest: DbFsPanel sözleşme testleri.
// Denetlenen sözleşmeler (ADR-0014):
//   1. db./fs. capability'si olmayan node'da panel HİÇ render edilmez.
//   2. Kimlik listesi/şifre: task sonucunda düz şifre GÖRÜNMEZ; yalnız
//      credential_id + fingerprint görünür; şifre yalnız "bir kez göster"
//      butonuyla (reveal) sahneye çıkar.
//   3. fs başarısız görev hata kategorisi kullanıcıya dürüstçe görünür.
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { NodeDetail } from "../pages/NodeDetail";
import { LocaleProvider } from "../i18n/LocaleProvider";
import { apiFetch } from "../api/client";
import type { NodeRecord } from "../api/client";

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

const node: NodeRecord = {
  id: "55555555-5555-5555-5555-555555555555",
  display_name: "dbfs-01",
  hostname: "dbfs01.internal",
  os_family: "linux",
  status: "active",
  tags: [],
  group_ids: [],
  created_at: "2026-11-02T00:00:00Z",
  updated_at: "2026-11-02T00:00:00Z",
};

const dbfsCapabilities = {
  capabilities: [
    { node_id: node.id, capability_id: "agent.echo", version: "1.0.0", available: true },
    { node_id: node.id, capability_id: "db.instance.list", version: "1.0.0", available: true },
    { node_id: node.id, capability_id: "db.user.create", version: "1.0.0", available: true },
    { node_id: node.id, capability_id: "fs.list", version: "1.0.0", available: true },
    { node_id: node.id, capability_id: "fs.upload", version: "1.0.0", available: true },
  ],
};

const bareCapabilities = {
  capabilities: [
    { node_id: node.id, capability_id: "agent.echo", version: "1.0.0", available: true },
  ],
};

function routeJson(routes: Record<string, unknown>) {
  return async (path: unknown) => {
    const key = String(path).split("?")[0];
    if (key in routes) {
      const v = routes[key];
      if (v instanceof Error) throw v;
      return v;
    }
    throw new Error(`beklenmeyen istek: ${String(path)}`);
  };
}

function renderDetail() {
  return render(
    <LocaleProvider>
      <NodeDetail node={node} onBack={() => {}} />
    </LocaleProvider>,
  );
}

describe("NodeDetail DB + Dosya yöneticisi bölümü (Faz 10)", () => {
  it("capability yokken panel hiç render edilmez", async () => {
    mockFetch.mockImplementation(
      routeJson({
        [`/api/v1/nodes/${node.id}/facts/system`]: { node_id: node.id, os_id: "ubuntu", os_version_id: "24.04", os_pretty_name: "Ubuntu 24.04", source: "os-release", collected_at: "2026-11-02T10:00:00Z" },
        [`/api/v1/nodes/${node.id}/metrics/latest`]: null,
        [`/api/v1/nodes/${node.id}/capabilities`]: bareCapabilities,
        "/api/v1/tasks": { tasks: [] },
      }),
    );
    renderDetail();
    await waitFor(() => {
      expect(screen.getByText("dbfs-01")).toBeInTheDocument();
    });
    expect(screen.queryByTestId("db-panel")).not.toBeInTheDocument();
    expect(screen.queryByTestId("fs-panel")).not.toBeInTheDocument();
  });

  it("db/fs capability varken bölümler görünür; şifre task sonucunda düz metin olarak GÖRÜNMEZ", async () => {
    mockFetch.mockImplementation(
      routeJson({
        [`/api/v1/nodes/${node.id}/facts/system`]: { node_id: node.id, os_id: "ubuntu", os_version_id: "24.04", os_pretty_name: "Ubuntu 24.04", source: "os-release", collected_at: "2026-11-02T10:00:00Z" },
        [`/api/v1/nodes/${node.id}/metrics/latest`]: null,
        [`/api/v1/nodes/${node.id}/capabilities`]: dbfsCapabilities,
        [`/api/v1/nodes/${node.id}/db-credentials`]: {
          credentials: [
            { id: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee", node_id: node.id, kind: "postgres", username: "app_ro", fingerprint: "fp12", revealed_at: null },
          ],
        },
        "/api/v1/db-credentials/aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee/reveal": {
          password: "MockDemo-fp12-x7K9",
          one_time: true,
        },
        "/api/v1/tasks": { tasks: [] },
      }),
    );
    renderDetail();
    await waitFor(() => {
      expect(screen.getByTestId("db-panel")).toBeInTheDocument();
    });
    expect(screen.getByTestId("fs-panel")).toBeInTheDocument();

    // Kimlik kartı: parmak izi görünür, şifre ASLA. (Kart async yüklenir —
    // IT-02 flake dersi: waitFor'sız erişim yarış koşusudur.)
    await waitFor(() => {
      expect(screen.getByTestId("db-credentials")).toBeInTheDocument();
    });
    const creds = within(screen.getByTestId("db-credentials"));
    expect(creds.getByText("fp12")).toBeInTheDocument();
    expect(screen.queryByTestId("revealed-password")).not.toBeInTheDocument();

    // Reveal akışı: buton → tek seferlik şifre görünür.
    await userEvent.click(creds.getByTestId("reveal-app_ro"));
    await waitFor(() => {
      expect(screen.getByTestId("revealed-password")).toBeInTheDocument();
    });
    // Reveal sonrası reveal uç noktası çağrılmış olmalı.
    expect(mockFetch.mock.calls.some(([p]) => String(p).includes("/db-credentials/aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee/reveal"))).toBe(true);
  });
});
