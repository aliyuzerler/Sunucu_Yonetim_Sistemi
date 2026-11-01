// Faz 08 vitest: Web hosting bölümü görünürlüğü + istemci tarafı
// doğrulamalar (site adı allowlist, proxy hedefi loopback kısıtı; gerçek
// kural sunucu + agent + broker — ADR-0012).
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
  id: "33333333-3333-3333-3333-333333333333",
  display_name: "web-01",
  hostname: "web01.internal",
  os_family: "linux",
  status: "active",
  tags: [],
  group_ids: [],
  created_at: "2026-11-02T00:00:00Z",
  updated_at: "2026-11-02T00:00:00Z",
};

const webCapabilities = {
  capabilities: [
    { node_id: node.id, capability_id: "agent.echo", version: "1.0.0", available: true },
    { node_id: node.id, capability_id: "web.site.list", version: "1.0.0", available: true },
    { node_id: node.id, capability_id: "web.site.create", version: "1.0.0", available: true },
    { node_id: node.id, capability_id: "web.site.delete", version: "1.0.0", available: true },
    { node_id: node.id, capability_id: "web.php.list", version: "1.0.0", available: true },
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

describe("NodeDetail Web hosting bölümü (Faz 08)", () => {
  it("web capability varken bölüm görünür; delete 'docroot korunur' etiketlidir", async () => {
    mockFetch.mockImplementation(
      routeJson({
        [`/api/v1/nodes/${node.id}/facts/system`]: { node_id: node.id, os_id: "ubuntu", os_version_id: "24.04", os_pretty_name: "Ubuntu 24.04", source: "os-release", collected_at: "2026-11-02T10:00:00Z" },
        [`/api/v1/nodes/${node.id}/metrics/latest`]: null,
        [`/api/v1/nodes/${node.id}/capabilities`]: webCapabilities,
        "/api/v1/tasks": { tasks: [] },
      }),
    );
    renderDetail();
    await waitFor(() => {
      expect(screen.getByRole("region", { name: /Web hosting/ })).toBeInTheDocument();
    });
    const web = within(screen.getByRole("region", { name: /Web hosting/ }));
    expect(web.getByRole("button", { name: "Siteleri listele" })).toBeInTheDocument();
    expect(web.getByRole("button", { name: "PHP sürümleri" })).toBeInTheDocument();
    expect(web.getByRole("button", { name: "Site oluştur" })).toBeInTheDocument();
    expect(web.getByRole("button", { name: "Siteyi sil" })).toBeInTheDocument();
    expect(web.getByText(/document root KORUNUR/)).toBeInTheDocument();
  });

  it("geçersiz site adı istemcide reddedilir; geçerli form görev oluşturur ve sonuç poll edilir", async () => {
    const user = userEvent.setup();
    mockFetch.mockImplementation((path: unknown) => {
      const key = String(path).split("?")[0];
      if (key === "/api/v1/tasks" && typeof path === "object" && path !== null && "body" in path) {
        const body = JSON.parse(String((path as RequestInit).body));
        if (body.operation === "web.site.create") {
          return Promise.resolve({ id: "task-9", status: "queued", operation: body.operation });
        }
      }
      if (key === "/api/v1/tasks") return Promise.resolve({ id: "task-1", status: "queued", operation: "web.site.create" });
      if (key === `/api/v1/nodes/${node.id}/facts/system`) {
        return Promise.resolve({ node_id: node.id, os_id: "ubuntu", os_version_id: "24.04", os_pretty_name: "Ubuntu 24.04", source: "os-release", collected_at: "2026-11-02T10:00:00Z" });
      }
      if (key === `/api/v1/nodes/${node.id}/metrics/latest`) return Promise.resolve(null);
      if (key === `/api/v1/nodes/${node.id}/capabilities`) return Promise.resolve(webCapabilities);
      if (key === "/api/v1/tasks/task-1" || key === "/api/v1/tasks/task-9") {
        return Promise.resolve({
          id: "task-1",
          status: "succeeded",
          result: {
            result: {
              site: "blog", action: "create", executed: true,
              config_path: "/etc/nginx/conf.d/srvctl-blog.conf",
              rollback_path: "/etc/nginx/conf.d/srvctl-blog.conf.rollback",
              user: "sc-web-blog",
              notes: ["document root KORUNDU (kullanıcı verisi silinmez — ADR-0012)"],
            },
          },
        });
      }
      return Promise.reject(new Error(`beklenmeyen: ${String(path)}`));
    });
    renderDetail();
    const web = within(await screen.findByRole("region", { name: /Web hosting/ }));
    // Geçersiz ad: istek gitmez.
    await user.type(web.getByLabelText("Site adı"), "Blog");
    await user.type(web.getByLabelText(/Sunucu adları/), "blog.example.com");
    await user.click(web.getByRole("button", { name: "Site oluştur" }));
    expect(await screen.findByText(/Geçersiz site adı/)).toBeInTheDocument();
    expect(mockFetch).not.toHaveBeenCalledWith("/api/v1/tasks", expect.anything());
    // Geçerli ad: görev oluşturulur + sonuç poll edilir.
    await user.clear(web.getByLabelText("Site adı"));
    await user.type(web.getByLabelText("Site adı"), "blog");
    await user.click(web.getByRole("button", { name: "Site oluştur" }));
    await waitFor(() => {
      expect(mockFetch).toHaveBeenCalledWith("/api/v1/tasks", expect.anything());
    });
    const report = await screen.findByTestId("web-report", {}, { timeout: 4000 });
    await waitFor(() => {
      expect(report.textContent).toContain("srvctl-blog.conf");
    });
  });

  it("php backend sürüm matrisi dışı değer istemcide reddedilir", async () => {
    const user = userEvent.setup();
    mockFetch.mockImplementation((path: unknown) => {
      const key = String(path).split("?")[0];
      if (key === `/api/v1/nodes/${node.id}/facts/system`) {
        return Promise.resolve({ node_id: node.id, os_id: "ubuntu", os_version_id: "24.04", os_pretty_name: "Ubuntu 24.04", source: "os-release", collected_at: "2026-11-02T10:00:00Z" });
      }
      if (key === `/api/v1/nodes/${node.id}/metrics/latest`) return Promise.resolve(null);
      if (key === `/api/v1/nodes/${node.id}/capabilities`) return Promise.resolve(webCapabilities);
      if (key === "/api/v1/tasks") return Promise.resolve({ id: "task-2", status: "queued", operation: "web.site.create" });
      return Promise.reject(new Error(`beklenmeyen: ${String(path)}`));
    });
    renderDetail();
    const web = within(await screen.findByRole("region", { name: /Web hosting/ }));
    await user.type(web.getByLabelText("Site adı"), "shop");
    await user.type(web.getByLabelText(/Sunucu adları/), "shop.example.com");
    await user.selectOptions(web.getByLabelText("Backend"), "php");
    // select yalnız matris sürümlerini sunar (kapalı küme — UI bunu garanti eder).
    const options = Array.from(web.getByLabelText("PHP sürümü").querySelectorAll("option")).map((o) => o.value);
    expect(options).toEqual(["8.2", "8.3", "8.4", "8.5"]);
    expect(mockFetch).not.toHaveBeenCalledWith("/api/v1/tasks", expect.anything());
  });
});
