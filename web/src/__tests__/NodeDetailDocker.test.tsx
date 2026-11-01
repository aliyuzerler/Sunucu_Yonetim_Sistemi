// Faz 07 vitest: Docker bölümü görünürlüğü + istemci tarafı doğrulamalar
// (ref allowlist, preview etiketleri; gerçek kural sunucu + agent + broker).
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
  id: "22222222-2222-2222-2222-222222222222",
  display_name: "docker-01",
  hostname: "docker01.internal",
  os_family: "linux",
  status: "active",
  tags: [],
  group_ids: [],
  created_at: "2026-11-02T00:00:00Z",
  updated_at: "2026-11-02T00:00:00Z",
};

const dockerCapabilities = {
  capabilities: [
    { node_id: node.id, capability_id: "agent.echo", version: "1.0.0", available: true },
    { node_id: node.id, capability_id: "docker.container.list", version: "1.0.0", available: true },
    { node_id: node.id, capability_id: "docker.container.remove", version: "1.0.0", available: true },
    { node_id: node.id, capability_id: "docker.compose.validate", version: "1.0.0", available: true },
    { node_id: node.id, capability_id: "docker.compose.deploy", version: "1.0.0", available: true },
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

describe("NodeDetail Docker bölümü (Faz 07)", () => {
  it("docker capability varken bölüm görünür; remove önizlemesi 'çalıştırmaz' etiketlidir", async () => {
    mockFetch.mockImplementation(
      routeJson({
        [`/api/v1/nodes/${node.id}/facts/system`]: { node_id: node.id, os_id: "ubuntu", os_version_id: "24.04", os_pretty_name: "Ubuntu 24.04", source: "os-release", collected_at: "2026-11-02T10:00:00Z" },
        [`/api/v1/nodes/${node.id}/metrics/latest`]: null,
        [`/api/v1/nodes/${node.id}/capabilities`]: dockerCapabilities,
        "/api/v1/tasks": { tasks: [] },
      }),
    );
    renderDetail();
    await waitFor(() => {
      expect(screen.getByRole("region", { name: /Docker/ })).toBeInTheDocument();
    });
    const docker = within(screen.getByRole("region", { name: /Docker/ }));
    // Preview etiketleri: silme + compose deploy "çalıştırmaz" vurgusu (ADR-0011).
    expect(docker.getByRole("button", { name: /Silme önizlemesi/ })).toBeInTheDocument();
    expect(docker.getByRole("button", { name: /Deploy planı/ })).toBeInTheDocument();
    expect(docker.getByText(/hiçbir şey silinmez/)).toBeInTheDocument();
  });

  it("geçersiz ref istemcide reddedilir; geçerli ref görev oluşturur", async () => {
    const user = userEvent.setup();
    mockFetch.mockImplementation((path: unknown) => {
      const key = String(path).split("?")[0];
      if (key === "/api/v1/tasks") {
        return Promise.resolve({ id: "task-1", status: "queued", operation: "docker.container.remove" });
      }
      if (key === `/api/v1/nodes/${node.id}/facts/system`) {
        return Promise.resolve({ node_id: node.id, os_id: "ubuntu", os_version_id: "24.04", os_pretty_name: "Ubuntu 24.04", source: "os-release", collected_at: "2026-11-02T10:00:00Z" });
      }
      if (key === `/api/v1/nodes/${node.id}/metrics/latest`) return Promise.resolve(null);
      if (key === `/api/v1/nodes/${node.id}/capabilities`) return Promise.resolve(dockerCapabilities);
      if (key === "/api/v1/tasks/task-1") {
        return Promise.resolve({ id: "task-1", status: "succeeded", result: { valid: true, services: 1, findings: [] } });
      }
      return Promise.reject(new Error(`beklenmeyen: ${String(path)}`));
    });
    renderDetail();
    const docker = within(await screen.findByRole("region", { name: /Docker/ }));
    const refInput = docker.getByLabelText(/Container kimliği\/adı/);
    // Geçersiz ref: istek gitmez.
    await user.type(refInput, "-flag");
    await user.click(docker.getByRole("button", { name: "Başlat" }));
    expect(await screen.findByText("Geçersiz kaynak referansı")).toBeInTheDocument();
    expect(mockFetch).not.toHaveBeenCalledWith("/api/v1/tasks", expect.anything());
    // Geçerli ref + remove preview: görev oluşturulur.
    await user.clear(refInput);
    await user.type(docker.getByLabelText(/Kimlik\/ad/), "app-data");
    await user.click(docker.getByRole("button", { name: /Silme önizlemesi/ }));
    await waitFor(() => {
      expect(mockFetch).toHaveBeenCalledWith("/api/v1/tasks", expect.anything());
    });
  });
});
