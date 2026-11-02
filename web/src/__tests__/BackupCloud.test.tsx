import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { BackupsPage } from "../pages/BackupsPage";
import { CloudPage } from "../pages/CloudPage";
import { LocaleProvider } from "../i18n/LocaleProvider";
import { apiFetch } from "../api/client";
import type { BackupRecord, RestoreDrill } from "../api/client";

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

const node = {
  id: "11111111-1111-1111-1111-111111111111",
  display_name: "web-01",
  status: "active",
};

const cpBackup: BackupRecord = {
  id: "22222222-2222-4222-8222-222222222222",
  scope: "control_plane",
  kind: "control_plane_db",
  status: "success",
  verify_state: "verified",
  verify_detail: "tablo=27 satır=120",
  size_bytes: 204800,
  sha256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  created_at: new Date().toISOString(),
  updated_at: new Date().toISOString(),
};

const nodeBackup: BackupRecord = {
  id: "33333333-3333-4333-8333-333333333333",
  scope: "node",
  node_id: node.id,
  kind: "restic",
  status: "success",
  verify_state: "verified",
  size_bytes: 4096,
  snapshot_id: "abcd1234ef567890",
  created_at: new Date().toISOString(),
  updated_at: new Date().toISOString(),
};

const drill: RestoreDrill = {
  id: "44444444-4444-4444-8444-444444444444",
  backup_id: cpBackup.id,
  target: "scp_drill_deadbeef",
  status: "success",
  detail: "hedef=scp_drill_deadbeef tablo=27 satır=120",
  tables_restored: 27,
  rows_restored: 120,
  created_at: new Date().toISOString(),
};

describe("BackupsPage", () => {
  it("CP yedeğini doğrulama durumuyla gösterir; tatbikat görevi başlatır", async () => {
    const routes = routeJson({
      "/api/v1/backups": (path: string) =>
        path.includes("scope=control_plane")
          ? { backups: [cpBackup] }
          : { backups: [nodeBackup] },
      "/api/v1/restore-drills": { drills: [drill] },
      "/api/v1/nodes": { nodes: [node], has_more: false },
    });
    mockFetch.mockImplementation(routes as never);
    const user = userEvent.setup();

    render(
      <LocaleProvider>
        <BackupsPage />
      </LocaleProvider>,
    );

    await waitFor(() => {
      expect(screen.getAllByText("Doğrulandı").length).toBeGreaterThanOrEqual(1);
    });
    expect(screen.getAllByText(/tablo=27 satır=120/).length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText(/scp_drill_deadbeef/).length).toBeGreaterThanOrEqual(1);

    // İzole tatbikat butonu POST /tasks atar (backup.cp.restore_drill).
    mockFetch.mockImplementation(
      routeJson({
        "/api/v1/backups": (path: string) =>
          path.includes("scope=control_plane")
            ? { backups: [cpBackup] }
            : { backups: [nodeBackup] },
        "/api/v1/restore-drills": { drills: [drill] },
        "/api/v1/nodes": { nodes: [node], has_more: false },
        "/api/v1/tasks": (path: string, init?: RequestInit) => {
          if (!String(path).includes("/api/v1/tasks/")) {
            expect(String(init?.body)).toContain("backup.cp.restore_drill");
            return { id: "t1", status: "queued" } as never;
          }
          return { id: "t1", status: "queued" } as never;
        },
        "/api/v1/tasks/t1": { id: "t1", status: "succeeded" },
      }) as never,
    );
    await user.click(screen.getByRole("button", { name: "İzole restore tatbikatı" }));
    await waitFor(() => {
      expect(screen.getByText("İşlem tamamlandı.")).toBeInTheDocument();
    });
  });

  it("node yedeği olmadan node işlemleri butonları devre dışıdır (dürüst boş durum)", async () => {
    mockFetch.mockImplementation(
      routeJson({
        "/api/v1/backups": { backups: [] },
        "/api/v1/restore-drills": { drills: [] },
        "/api/v1/nodes": { nodes: [], has_more: false },
      }) as never,
    );
    render(
      <LocaleProvider>
        <BackupsPage />
      </LocaleProvider>,
    );
    await waitFor(() => {
      expect(screen.getByText("Henüz kontrol paneli yedeği yok")).toBeInTheDocument();
    });
    expect(screen.getByText("Henüz node yedeği yok")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Node Yedeği Al" })).toBeDisabled();
  });
});

describe("CloudPage", () => {
  const provider = {
    id: "55555555-5555-4555-8555-555555555555",
    name: "citest-s3",
    kind: "s3" as const,
    endpoint: "https://s3.example.com",
    region: "test-1",
    credential_keys: ["access_key_id", "secret_access_key"],
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
  };

  it("sağlayıcıyı listeler, izin matrisini ve silme onayını gösterir", async () => {
    mockFetch.mockImplementation(
      routeJson({
        "/api/v1/cloud-providers": { providers: [provider] },
      }) as never,
    );
    const user = userEvent.setup();

    render(
      <LocaleProvider>
        <CloudPage />
      </LocaleProvider>,
    );

    await waitFor(() => {
      expect(screen.getByText("citest-s3")).toBeInTheDocument();
    });
    expect(screen.getByText("s3")).toBeInTheDocument();

    // Verify → matris tablosu + probe sonuçları.
    mockFetch.mockImplementation(
      routeJson({
        "/api/v1/cloud-providers": { providers: [provider] },
        "/api/v1/cloud-providers/55555555-5555-4555-8555-555555555555/verify": {
          provider: "s3",
          endpoint: "https://s3.example.com",
          region: "test-1",
          matrix: {
            provider: "s3",
            entries: [
              { action: "list", provider_permission: "s3:ListAllMyBuckets", risk: "read", confirm_required: false },
              { action: "delete", provider_permission: "s3:DeleteBucket", risk: "destructive", confirm_required: true },
            ],
          },
          probes: [{ action: "list", ok: true, reason: "kanıtlandı" }],
          checked_at: new Date().toISOString(),
        },
      }) as never,
    );
    await user.click(screen.getByRole("button", { name: "Kimliği doğrula" }));
    await waitFor(() => {
      expect(screen.getByText("s3:DeleteBucket")).toBeInTheDocument();
    });
    expect(screen.getByText("Kanıtlandı")).toBeInTheDocument();
  });

  it("kaynak silme iki adımlı onay ister ve confirm=true ile DELETE atar", async () => {
    mockFetch.mockImplementation(
      routeJson({
        "/api/v1/cloud-providers": { providers: [provider] },
        "/api/v1/cloud-providers/55555555-5555-4555-8555-555555555555/resources": {
          resources: [{ name: "doomed-bucket", kind: "bucket" }],
        },
      }) as never,
    );
    const user = userEvent.setup();

    render(
      <LocaleProvider>
        <CloudPage />
      </LocaleProvider>,
    );

    await waitFor(() => {
      expect(screen.getByText("citest-s3")).toBeInTheDocument();
    });
    await user.click(screen.getByRole("button", { name: "Kaynakları listele" }));
    await waitFor(() => {
      expect(screen.getByText("doomed-bucket")).toBeInTheDocument();
    });

    // İlk adım: onay metni görünür, DELETE henüz ATILMAZ.
    let deleteCalled = false;
    mockFetch.mockImplementation(
      routeJson({
        "/api/v1/cloud-providers": { providers: [provider] },
        "/api/v1/cloud-providers/55555555-5555-4555-8555-555555555555/resources": {
          resources: [],
        },
        "/api/v1/cloud-providers/55555555-5555-4555-8555-555555555555/resources/doomed-bucket": (
          path: string,
          init?: RequestInit,
        ) => {
          if (init?.method === "DELETE") {
            expect(String(path)).toContain("confirm=true");
            deleteCalled = true;
          }
          return { deleted: true } as never;
        },
      }) as never,
    );
    await user.click(screen.getByRole("button", { name: "Sil" }));
    expect(screen.getByText(/kalıcı siler/)).toBeInTheDocument();
    expect(deleteCalled).toBe(false);

    await user.click(screen.getByRole("button", { name: "Kalıcı sil" }));
    await waitFor(() => {
      expect(deleteCalled).toBe(true);
    });
    expect(screen.getByText("Kaynak silindi.")).toBeInTheDocument();
  });
});
