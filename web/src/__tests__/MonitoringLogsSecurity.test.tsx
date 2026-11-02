import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MonitoringPage, freshnessState } from "../pages/MonitoringPage";
import { LogsPage } from "../pages/LogsPage";
import { SecurityPage, stateVariant } from "../pages/SecurityPage";
import { LocaleProvider } from "../i18n/LocaleProvider";
import { apiFetch } from "../api/client";
import type { MetricsSnapshot, SecurityReport } from "../api/client";

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

const freshSnapshot: MetricsSnapshot = {
  collected_at: new Date(Date.now() - 30_000).toISOString(),
  source: "procfs+statfs",
  cpu: { percent: 12.5, logical_cores: 4, load_1m: 0.2, load_5m: 0.1, load_15m: 0.1 },
  memory: { total_bytes: 8e9, available_bytes: 4e9, used_bytes: 4e9, used_percent: 50 },
  disks: [],
  networks: [],
  uptime_seconds: 1000,
};

const alertOpen = {
  id: "a1",
  rule_id: "r1",
  node_id: node.id,
  dimension: "",
  fingerprint: "r1|node|",
  status: "open" as const,
  severity: "warning" as const,
  value: 95,
  occurrences: 3,
  title: "CPU yüksek (düğüm cpu)",
  detail: "değer 95.0%, eşik 90.0% (gt), pencere 0s",
  first_seen_at: new Date().toISOString(),
  last_seen_at: new Date().toISOString(),
  acknowledged_at: null,
  silenced_until: null,
  resolved_at: null,
};

describe("MonitoringPage", () => {
  it("aktif alarmları durum rozetleriyle gösterir ve onaylama aksiyonu POST atar", async () => {
    const routes = routeJson({
      "/api/v1/alerts": { alerts: [alertOpen] },
      "/api/v1/alert-rules": { rules: [] },
      "/api/v1/nodes": { nodes: [node], has_more: false },
      "/api/v1/nodes/11111111-1111-1111-1111-111111111111/metrics/latest": freshSnapshot,
    });
    mockFetch.mockImplementation(routes as never);
    const user = userEvent.setup();

    render(
      <LocaleProvider>
        <MonitoringPage />
      </LocaleProvider>,
    );

    await waitFor(() => {
      expect(screen.getByText("CPU yüksek (düğüm cpu)")).toBeInTheDocument();
    });
    expect(screen.getByText("Açık")).toBeInTheDocument();
    // "Tekrar: 3 · <tarih>" aynı öğede devam ettiği için exact-match değil regex kullanılır.
    expect(screen.getByText(/Tekrar: 3/)).toBeInTheDocument();

    // Onaylama sonrası reload() GET'leri de yanıtlanmalı; aksi halde effect hatası
    // bildirimi gizler (notice && !error koşulu) — acknowledge + tüm GET rotaları.
    mockFetch.mockImplementation(
      routeJson({
        "/api/v1/alerts": { alerts: [{ ...alertOpen, status: "acknowledged" }] },
        "/api/v1/alert-rules": { rules: [] },
        "/api/v1/nodes": { nodes: [node], has_more: false },
        "/api/v1/nodes/11111111-1111-1111-1111-111111111111/metrics/latest": freshSnapshot,
        "/api/v1/alerts/a1/acknowledge": (_p: string, init?: RequestInit) => {
          expect(init?.method).toBe("POST");
          return { ok: true };
        },
      }) as never,
    );
    await user.click(screen.getByRole("button", { name: "Onayla" }));
    await waitFor(() => {
      expect(screen.getByText("İşlem tamamlandı.")).toBeInTheDocument();
    });
  });

  it("kural oluşturma formu POST gövdesini kapalı şemayla gönderir", async () => {
    const routes = routeJson({
      "/api/v1/alerts": { alerts: [] },
      "/api/v1/alert-rules": { rules: [] },
      "/api/v1/nodes": { nodes: [node], has_more: false },
      "/api/v1/nodes/11111111-1111-1111-1111-111111111111/metrics/latest": freshSnapshot,
    });
    mockFetch.mockImplementation(routes as never);
    const user = userEvent.setup();

    render(
      <LocaleProvider>
        <MonitoringPage />
      </LocaleProvider>,
    );
    await waitFor(() => {
      expect(screen.getByLabelText("Kural adı")).toBeInTheDocument();
    });

    let captured: Record<string, unknown> | null = null;
    mockFetch.mockImplementation(
      routeJson({
        // DİKKAT: POST sonrası reload() da bu anahtara GET atar — yalnız
        // POST gövdesi yakalanır (ürün düzeltmesi: reload artık çalışıyor).
        "/api/v1/alert-rules": (_p: string, init?: RequestInit) => {
          if ((init?.method ?? "GET") !== "POST") return { rules: [] };
          captured = JSON.parse(String(init?.body ?? "{}"));
          return { id: "r2", ...captured };
        },
        "/api/v1/alerts": { alerts: [] },
        "/api/v1/nodes": { nodes: [node], has_more: false },
        "/api/v1/nodes/11111111-1111-1111-1111-111111111111/metrics/latest": freshSnapshot,
      }) as never,
    );
    await user.type(screen.getByLabelText("Kural adı"), "Disk doluyor");
    await user.type(screen.getByLabelText("Eşik"), "90");
    await user.click(screen.getByRole("button", { name: "Kural oluştur" }));
    await waitFor(() => {
      expect(captured).not.toBeNull();
    });
    expect(captured).toMatchObject({ name: "Disk doluyor", metric: "cpu", op: "gt", threshold: 90 });
  });
});

describe("freshnessState", () => {
  it("veri yoksa/asla ok dönmez; taze snapshot ok döner", () => {
    expect(freshnessState(null, Date.now())).toBe("warn");
    expect(freshnessState(freshSnapshot, Date.now())).toBe("ok");
    const stale = { ...freshSnapshot, collected_at: new Date(Date.now() - 600_000).toISOString() };
    expect(freshnessState(stale, Date.now())).toBe("warn");
  });
});

describe("LogsPage", () => {
  it("filtrelerle task oluşturur ve redacted sonuçları tablo halinde gösterir", async () => {
    mockFetch.mockImplementation(
      routeJson({
        "/api/v1/nodes": { nodes: [node], has_more: false },
      }) as never,
    );
    const user = userEvent.setup();
    render(
      <LocaleProvider>
        <LogsPage />
      </LocaleProvider>,
    );

    // Liste yerleşmeden selectOptions YAPILMAZ (Faz 06 IT-01 dersi: async yükleme yarışı).
    await screen.findByLabelText("Düğüm");
    await waitFor(() => {
      expect(screen.getByRole("option", { name: "web-01" })).toBeInTheDocument();
    });
    await user.selectOptions(screen.getByLabelText("Düğüm"), node.id);
    await user.type(screen.getByLabelText("Birim (örn. nginx)"), "nginx");

    let createdPayload: Record<string, unknown> | null = null;
    let polled = 0;
    mockFetch.mockImplementation(
      routeJson({
        "/api/v1/tasks": (_p: string, init?: RequestInit) => {
          createdPayload = JSON.parse(String(init?.body ?? "{}"));
          return { id: "task-1", status: "queued" };
        },
        "/api/v1/tasks/task-1": () => {
          polled += 1;
          return { id: "task-1", status: polled >= 2 ? "succeeded" : "running", result: { entries: [
            { timestamp: "2026-11-02T10:00:00Z", severity: 3, severity_name: "err", unit: "nginx.service", message: "upstream failed password=[REDACTED]" },
          ], source: "journald" } };
        },
        "/api/v1/nodes": { nodes: [node], has_more: false },
      }) as never,
    );
    await user.click(screen.getByRole("button", { name: "Ara" }));

    await waitFor(
      () => {
        expect(screen.getByText(/1 kayıt bulundu/)).toBeInTheDocument();
      },
      // pollTask her turda 1 sn bekler (NodeDetailWeb kuralı: timeout 4000).
      { timeout: 4000 },
    );
    expect(createdPayload).toMatchObject({ operation: "logs.search", node_id: node.id });
    expect(screen.getByText(/password=\[REDACTED\]/)).toBeInTheDocument();
    expect(screen.getByText("journald")).toBeInTheDocument();
  });
});

describe("SecurityPage", () => {
  const report: SecurityReport = {
    generated_at: new Date().toISOString(),
    controls: [
      { id: "admin_bootstrapped", state: "ok", evidence: "1 aktif yönetici", checked_at: new Date().toISOString() },
      { id: "node_telemetry_freshness", state: "unknown", evidence: "kanıt yok", checked_at: new Date().toISOString() },
      { id: "agent_cert_expiry", state: "alert", evidence: "1 süresi dolmuş", checked_at: new Date().toISOString() },
    ],
  };

  it("kontrolleri dürüst durumlarla listeler; unknown asla Tamam görünmez", async () => {
    mockFetch.mockImplementation(
      routeJson({ "/api/v1/security/overview": report }) as never,
    );
    render(
      <LocaleProvider>
        <SecurityPage />
      </LocaleProvider>,
    );
    await waitFor(() => {
      expect(screen.getByText("Yönetici kurulumu")).toBeInTheDocument();
    });
    expect(screen.getByText("Bilinmiyor")).toBeInTheDocument();
    expect(screen.getByText("1 süresi dolmuş")).toBeInTheDocument();
  });

  it("stateVariant: unknown asla ok rozetine eşleşmez", () => {
    expect(stateVariant("ok")).toBe("ok");
    expect(stateVariant("unknown")).not.toBe("ok");
    expect(stateVariant("alert")).toBe("error");
  });
});
