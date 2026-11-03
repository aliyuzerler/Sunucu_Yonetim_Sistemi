// UX denetimi m.4.1 + m.4.2 vitest: URL tabanlı hash yönlendirme ve
// NodeDetail adreslenebilirliği.
//   - parseHash/buildHash saf fonksiyon sözleşmeleri.
//   - App kabuğu: derin bağlantı, gezinme → hash senkronu, geri dönüş
//     (history.back), bilinmeyen modül → ComingSoon.
//   - #/nodes/<id>: listedeki node ile detay; listede YOKSA tekil uç
//     noktadan çekme; NodeDetail "back" → URL #/nodes'a döner.
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { App } from "../App";
import { LocaleProvider } from "../i18n/LocaleProvider";
import { AuthProvider } from "../auth/AuthProvider";
import { parseHash, buildHash } from "../navigation/router";
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
  window.localStorage.clear();
  window.location.hash = "";
});

describe("router (saf fonksiyonlar)", () => {
  it("boş hash → overview", () => {
    expect(parseHash("")).toEqual({ page: "overview", nodeId: null });
    expect(parseHash("#")).toEqual({ page: "overview", nodeId: null });
    expect(parseHash("#/")).toEqual({ page: "overview", nodeId: null });
  });

  it("sayfa hash'ini ayrıştırır", () => {
    expect(parseHash("#/dns")).toEqual({ page: "dns", nodeId: null });
    expect(parseHash("#/nodes")).toEqual({ page: "nodes", nodeId: null });
  });

  it("nodes alt segmentini node kimliği olarak okur (m.4.2)", () => {
    expect(parseHash("#/nodes/abc-123")).toEqual({ page: "nodes", nodeId: "abc-123" });
    // Diğer sayfalarda ikinci segment node kimliği DEĞİLDİR.
    expect(parseHash("#/tasks/extra")).toEqual({ page: "tasks", nodeId: null });
  });

  it("bilinmeyen sayfa olduğu gibi döner (ComingSoon App'te karar verir)", () => {
    expect(parseHash("#/docker")).toEqual({ page: "docker", nodeId: null });
  });

  it("buildHash sub'i kodlar", () => {
    expect(buildHash("nodes")).toBe("#/nodes");
    expect(buildHash("nodes", "abc-123")).toBe("#/nodes/abc-123");
    expect(buildHash("nodes", "kimlik/li")).toBe("#/nodes/kimlik%2Fli");
  });
});

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

describe("App hash yönlendirme (m.4.1)", () => {
  it("hash'siz açılış overview'a gider ve overview verisini çeker", async () => {
    mockFetch.mockImplementation(
      routeJson({ ...sessionRoute, "/api/v1/overview": { nodes: 0, tasks: 0 } }),
    );
    renderApp();
    await waitFor(() => {
      expect(window.location.hash).toBe("");
      expect(mockFetch.mock.calls.some(([p]) => String(p).includes("/api/v1/overview"))).toBe(true);
    });
  });

  it("derin bağlantı (#/dns) doğrudan o sayfayı açar", async () => {
    mockFetch.mockImplementation(
      routeJson({ ...sessionRoute, "/api/v1/dns-providers": { providers: [] } }),
    );
    window.location.hash = "#/dns";
    renderApp();
    await waitFor(() => {
      expect(mockFetch.mock.calls.some(([p]) => String(p).includes("/api/v1/dns-providers"))).toBe(true);
    });
    expect(screen.getByRole("heading", { level: 1, name: "Alan Adları ve DNS" })).toBeInTheDocument();
  });

  it("kenar çubuğu tıklaması hash'i günceller (paylaşılabilir adres)", async () => {
    const user = userEvent.setup();
    mockFetch.mockImplementation(
      routeJson({
        ...sessionRoute,
        "/api/v1/overview": { nodes: 0, tasks: 0 },
        "/api/v1/nodes": { nodes: [], has_more: false },
        "/api/v1/node-groups": { groups: [] },
      }),
    );
    renderApp();
    await waitFor(() => {
      expect(screen.getByRole("button", { name: /Sunucular/ })).toBeInTheDocument();
    });
    await user.click(screen.getByRole("button", { name: /Sunucular/ }));
    await waitFor(() => {
      expect(window.location.hash).toBe("#/nodes");
    });
    expect(mockFetch.mock.calls.some(([p]) => String(p).includes("/api/v1/nodes"))).toBe(true);
  });

  it("history.back() önceki görünüme döner (geri tuşu çalışır)", async () => {
    const user = userEvent.setup();
    mockFetch.mockImplementation(
      routeJson({
        ...sessionRoute,
        "/api/v1/overview": { nodes: 0, tasks: 0 },
        "/api/v1/dns-providers": { providers: [] },
      }),
    );
    renderApp();
    await waitFor(() => {
      expect(screen.getByRole("button", { name: /Alan Adları ve DNS/ })).toBeInTheDocument();
    });
    await user.click(screen.getByRole("button", { name: /Alan Adları ve DNS/ }));
    await waitFor(() => {
      expect(window.location.hash).toBe("#/dns");
    });
    window.history.back();
    await waitFor(() => {
      expect(window.location.hash).toBe("");
    });
    await waitFor(() => {
      expect(mockFetch.mock.calls.filter(([p]) => String(p).includes("/api/v1/overview")).length).toBeGreaterThanOrEqual(1);
    });
  });

  it("bilinmeyen modül hash'i ComingSoon sayfasını gösterir", async () => {
    mockFetch.mockImplementation(routeJson(sessionRoute));
    window.location.hash = "#/docker";
    renderApp();
    await waitFor(() => {
      expect(screen.getByText("Bu modül henüz uygulanmadı")).toBeInTheDocument();
    });
  });
});

const node: NodeRecord = {
  id: "55555555-5555-5555-5555-555555555555",
  display_name: "route-01",
  hostname: "route01.internal",
  os_family: "linux",
  status: "active",
  tags: [],
  group_ids: [],
  created_at: "2026-11-02T00:00:00Z",
  updated_at: "2026-11-02T00:00:00Z",
};

const otherNode: NodeRecord = { ...node, id: "99999999-9999-4999-8999-999999999999", display_name: "other-01" };

const nodeDetailRoutes = {
  [`/api/v1/nodes/${node.id}`]: node,
  [`/api/v1/nodes/${node.id}/facts/system`]: { node_id: node.id, os_id: "ubuntu", os_version_id: "24.04", os_pretty_name: "Ubuntu 24.04", source: "os-release", collected_at: "2026-11-02T10:00:00Z" },
  [`/api/v1/nodes/${node.id}/metrics/latest`]: null,
  [`/api/v1/nodes/${node.id}/capabilities`]: {
    capabilities: [{ node_id: node.id, capability_id: "agent.echo", version: "1.0.0", available: true }],
  },
  "/api/v1/tasks": { tasks: [] },
};

describe("NodeDetail adreslenebilirliği (m.4.2)", () => {
  it("derin bağlantı #/nodes/<id> listedeki node için detay açar; back URL'i #/nodes'a döndürür", async () => {
    const user = userEvent.setup();
    mockFetch.mockImplementation(
      routeJson({
        ...sessionRoute,
        ...nodeDetailRoutes,
        "/api/v1/nodes": { nodes: [node], has_more: false },
        "/api/v1/node-groups": { groups: [] },
      }),
    );
    window.location.hash = `#/nodes/${node.id}`;
    renderApp();
    await waitFor(() => {
      expect(screen.getByRole("heading", { name: "route-01" })).toBeInTheDocument();
    });

    await user.click(screen.getByRole("button", { name: /Node listesine dön/ }));
    await waitFor(() => {
      expect(window.location.hash).toBe("#/nodes");
    });
  });

  it("derin bağlantıdaki node ilk sayfada yoksa tekil uç noktadan çekilir", async () => {
    mockFetch.mockImplementation(
      routeJson({
        ...sessionRoute,
        ...nodeDetailRoutes,
        "/api/v1/nodes": { nodes: [otherNode], has_more: false },
        "/api/v1/node-groups": { groups: [] },
      }),
    );
    window.location.hash = `#/nodes/${node.id}`;
    renderApp();
    await waitFor(() => {
      expect(
        mockFetch.mock.calls.some(([p]) => String(p) === `/api/v1/nodes/${node.id}`),
      ).toBe(true);
    });
    await waitFor(() => {
      expect(screen.getByRole("heading", { name: "route-01" })).toBeInTheDocument();
    });
  });

  it("liste görünümünde Detay tıklaması hash'i #/nodes/<id> yapar ve detayı açar", async () => {
    const user = userEvent.setup();
    mockFetch.mockImplementation(
      routeJson({
        ...sessionRoute,
        ...nodeDetailRoutes,
        "/api/v1/nodes": { nodes: [node], has_more: false },
        "/api/v1/node-groups": { groups: [] },
      }),
    );
    window.location.hash = "#/nodes";
    renderApp();
    await waitFor(() => {
      expect(screen.getAllByText("route-01").length).toBeGreaterThan(0);
    });
    await user.click(screen.getByRole("button", { name: "Detay" }));
    await waitFor(() => {
      expect(window.location.hash).toBe(`#/nodes/${node.id}`);
    });
    await waitFor(() => {
      expect(screen.getByRole("heading", { name: "route-01" })).toBeInTheDocument();
    });
  });
});
