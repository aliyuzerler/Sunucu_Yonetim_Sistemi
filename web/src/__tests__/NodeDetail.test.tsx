import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { NodeDetail } from "../pages/NodeDetail";
import { NodesPage } from "../pages/NodesPage";
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
  id: "11111111-1111-1111-1111-111111111111",
  display_name: "web-01",
  hostname: "web01.internal",
  os_family: "linux",
  status: "active",
  tags: [],
  group_ids: [],
  created_at: "2026-11-01T00:00:00Z",
  updated_at: "2026-11-01T00:00:00Z",
};

const facts = {
  node_id: node.id,
  os_id: "ubuntu",
  os_version_id: "24.04",
  os_pretty_name: "Ubuntu 24.04.3 LTS",
  os_home_url: "https://www.ubuntu.com/",
  kernel_release: "6.8.0-49-generic",
  arch: "amd64",
  systemd_version: "255",
  package_manager: "apt",
  systemd_available: true,
  source: "os-release+procfs+systemctl",
  collected_at: "2026-11-01T10:00:00Z",
};

const metrics = {
  collected_at: "2026-11-01T10:05:00Z",
  source: "procfs+statfs",
  cpu: { percent: 12.5, logical_cores: 4, load_1m: 0.4, load_5m: 0.3, load_15m: 0.2 },
  memory: { total_bytes: 16 * 1024 ** 3, available_bytes: 8 * 1024 ** 3, used_bytes: 8 * 1024 ** 3, used_percent: 50 },
  disks: [
    { device: "/dev/vda1", mount_point: "/", fs_type: "ext4", total_bytes: 40 * 1024 ** 3, used_bytes: 10 * 1024 ** 3, available_bytes: 30 * 1024 ** 3 },
  ],
  networks: [{ interface: "ens160", rx_bytes: 1000, tx_bytes: 2000 }],
  uptime_seconds: 90061,
};

const capabilities = {
  capabilities: [
    { node_id: node.id, capability_id: "agent.echo", version: "1.0.0", available: true },
    {
      node_id: node.id,
      capability_id: "linux.systemd.service.restart",
      version: "1.0.0",
      available: false,
      reason_unavailable: "broker_unreachable",
    },
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

describe("NodeDetail (Faz 04)", () => {
  it("sistem bilgilerini ve metrikleri kaynak + zamanla gösterir", async () => {
    mockFetch.mockImplementation(
      routeJson({
        [`/api/v1/nodes/${node.id}/facts/system`]: facts,
        [`/api/v1/nodes/${node.id}/metrics/latest`]: metrics,
        [`/api/v1/nodes/${node.id}/capabilities`]: capabilities,
        "/api/v1/tasks": { tasks: [] },
      }),
    );

    render(
      <LocaleProvider>
        <NodeDetail node={node} onBack={() => {}} />
      </LocaleProvider>,
    );

    await waitFor(() => {
      expect(screen.getByText("Ubuntu 24.04.3 LTS")).toBeInTheDocument();
    });
    expect(screen.getByText("6.8.0-49-generic")).toBeInTheDocument();
    expect(screen.getByText("apt")).toBeInTheDocument();

    // CPU yüzdesi ve kaynak/zaman satırı.
    expect(screen.getByText("12,5%")).toBeInTheDocument();
    expect(screen.getByText(/procfs\+statfs/)).toBeInTheDocument();
    // Disk satırı: aygıt + dosya sistemi.
    expect(screen.getByText(/\/dev\/vda1/)).toBeInTheDocument();
    expect(screen.getByText(/ext4/)).toBeInTheDocument();
    // Ağ arayüzü.
    expect(screen.getByText(/ens160/)).toBeInTheDocument();
  });

  it("servis eylemi için geçersiz birim adını istemci tarafında reddeder", async () => {
    mockFetch.mockImplementation(
      routeJson({
        [`/api/v1/nodes/${node.id}/facts/system`]: facts,
        [`/api/v1/nodes/${node.id}/metrics/latest`]: metrics,
        [`/api/v1/nodes/${node.id}/capabilities`]: capabilities,
        "/api/v1/tasks": { tasks: [] },
      }),
    );

    const user = userEvent.setup();
    render(
      <LocaleProvider>
        <NodeDetail node={node} onBack={() => {}} />
      </LocaleProvider>,
    );
    await waitFor(() => {
      expect(screen.getByText("Ubuntu 24.04.3 LTS")).toBeInTheDocument();
    });

    await user.type(screen.getByLabelText(/Servis birimi/), "rm -rf /");
    await user.click(screen.getByRole("button", { name: "Başlat" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(/Geçersiz birim adı/);
    // API'ye görev isteği GİTMEMELİ.
    const taskPosts = mockFetch.mock.calls.filter(
      ([p, init]) => String(p) === "/api/v1/tasks" && (init as RequestInit | undefined)?.method === "POST",
    );
    expect(taskPosts).toHaveLength(0);
  });

  it("kabiliyet kullanılamıyorsa nedeni gösterir ve görev oluşturmaz", async () => {
    mockFetch.mockImplementation(
      routeJson({
        [`/api/v1/nodes/${node.id}/facts/system`]: facts,
        [`/api/v1/nodes/${node.id}/metrics/latest`]: metrics,
        [`/api/v1/nodes/${node.id}/capabilities`]: capabilities,
        "/api/v1/tasks": { tasks: [] },
      }),
    );

    const user = userEvent.setup();
    render(
      <LocaleProvider>
        <NodeDetail node={node} onBack={() => {}} />
      </LocaleProvider>,
    );
    await waitFor(() => {
      expect(screen.getAllByText(/broker_unreachable/).length).toBeGreaterThan(0);
    });

    await user.type(screen.getByLabelText(/Servis birimi/), "nginx.service");
    await user.click(screen.getByRole("button", { name: "Yeniden başlat" }));
    await waitFor(() => {
      const taskPosts = mockFetch.mock.calls.filter(
        ([p, init]) => String(p) === "/api/v1/tasks" && (init as RequestInit | undefined)?.method === "POST",
      );
      expect(taskPosts.length).toBeGreaterThan(0); // typed task oluşturulur
    });
  });
});

// NodesPage ↔ NodeDetail geçişi: Detay düğmesi detay görünümünü açar.
describe("NodesPage detail navigation", () => {
  it("Detay düğmesi node telemetri görünümünü açar", async () => {
    mockFetch.mockImplementation(
      routeJson({
        "/api/v1/nodes": { nodes: [node], has_more: false },
        "/api/v1/node-groups": { groups: [] },
        [`/api/v1/nodes/${node.id}/facts/system`]: facts,
        [`/api/v1/nodes/${node.id}/metrics/latest`]: metrics,
        [`/api/v1/nodes/${node.id}/capabilities`]: capabilities,
        "/api/v1/tasks": { tasks: [] },
      }),
    );

    const user = userEvent.setup();
    render(
      <LocaleProvider>
        <NodesPage />
      </LocaleProvider>,
    );
    await waitFor(() => {
      expect(screen.getByText("web-01")).toBeInTheDocument();
    });
    await user.click(screen.getByRole("button", { name: "Detay" }));
    await waitFor(() => {
      expect(screen.getByText("Ubuntu 24.04.3 LTS")).toBeInTheDocument();
    });
    expect(screen.getByRole("button", { name: /Node listesine dön/ })).toBeInTheDocument();
  });
});
