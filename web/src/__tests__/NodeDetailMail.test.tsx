import { describe, expect, it, beforeEach, vi } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import { MailPanel } from "../components/MailPanel";
import { LocaleProvider } from "../i18n/LocaleProvider";
import type { TaskLike } from "../components/MailPanel";

// Faz 11 vitest: MailPanel — preflight raporu, deliverability açıklaması
// (kriter 3), kuyruk içeriksizliği (kriter 5) ve tek-seferlik reveal
// disiplini (db-credentials ucunun paylaşımlı kullanımı).

type Submit = (op: string, payload: Record<string, unknown>) => Promise<TaskLike | null>;

const t = (k: string) => k;

function okTask(id: string, result: unknown): TaskLike {
  return { id, status: "succeeded", result };
}

function setup(overrides: {
  submit?: Submit;
  creds?: unknown;
  reveal?: unknown;
} = {}) {
  const submitted: Record<string, { op: string; payload: Record<string, unknown> }> = {};
  const submit: Submit =
    overrides.submit ??
    (async (op, payload) => {
      submitted[op] = { op, payload };
      return { id: `task-${op}`, status: "queued" };
    });

  const fetchMock = vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const url = String(input);
    if (url.includes("/db-credentials") && (!init?.method || init.method === "GET")) {
      return new Response(
        JSON.stringify({
          credentials: overrides.creds ?? [
            { id: "cred-mail-1", kind: "mailbox", username: "ali@mail.example.com", fingerprint: "fpMAIL", revealed_at: null },
            { id: "cred-db-1", kind: "postgres", username: "app_ro", fingerprint: "fpDB", revealed_at: null },
          ],
        }),
        { status: 200, headers: { "content-type": "application/json" } },
      );
    }
    if (url.includes("/reveal") && init?.method === "POST") {
      return new Response(
        JSON.stringify(overrides.reveal ?? { password: "REVEALED-ONCE-PW" }),
        { status: 200, headers: { "content-type": "application/json" } },
      );
    }
    // task poll — varsayılan olarak 'queued' döner (effect 15 denemede durur)
    if (url.startsWith("/api/v1/tasks/")) {
      return new Response(JSON.stringify({ id: "x", status: "queued" }), {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    }
    return new Response("{}", { status: 200, headers: { "content-type": "application/json" } });
  });
  vi.stubGlobal("fetch", fetchMock);
  return { submitted, fetchMock, submit };
}

beforeEach(() => {
  vi.unstubAllGlobals();
});

describe("MailPanel", () => {
  it("deliverability kartı SPF/DKIM/DMARC/PTR gereksinimlerini açıklar (kriter 3)", () => {
    setup();
    render(<LocaleProvider><MailPanel nodeId="n1" submitTask={async () => null} t={t} /></LocaleProvider>);
    expect(screen.getByTestId("mail-deliverability")).toBeInTheDocument();
    expect(screen.getByText("SPF")).toBeInTheDocument();
    expect(screen.getByText("DKIM")).toBeInTheDocument();
    expect(screen.getByText("DMARC")).toBeInTheDocument();
    expect(screen.getByText("PTR / rDNS")).toBeInTheDocument();
    // dış DNS dürüstlük notu
    expect(screen.getByText("nodedetail.mail.deliverability.intro")).toBeInTheDocument();
  });

  it("kuyruk tablosu alıcı adreslerini ASLA göstermez (kriter 5)", () => {
    const { fetchMock, submit } = setup();
    // queue.list görevi succeed olur ve alıcı adresi içeren kayıt döner
    fetchMock.mockImplementation(async (input: string | URL | Request) => {
      const url = String(input);
      if (url.includes("/db-credentials")) {
        return new Response(JSON.stringify({ credentials: [] }), { status: 200 });
      }
      if (url.startsWith("/api/v1/tasks/")) {
        return new Response(
          JSON.stringify(
            okTask("tq", {
              queue: [
                {
                  queue_id: "ABC123",
                  queue_name: "deferred",
                  size_bytes: 2048,
                  age_seconds: 300,
                  sender: "bounce@example.com",
                  reason: "connect to mx: refused",
                },
              ],
            }),
          ),
          { status: 200, headers: { "content-type": "application/json" } },
        );
      }
      return new Response("{}", { status: 200 });
    });
    render(<LocaleProvider><MailPanel nodeId="n1" submitTask={submit} t={t} /></LocaleProvider>);
    fireEvent.click(screen.getByText("nodedetail.mail.queue.refresh"));
    return waitFor(() => {
      const table = screen.getByTestId("mail-queue");
      expect(table.textContent).toContain("ABC123");
      expect(table.textContent).toContain("bounce@example.com");
      expect(table.textContent).not.toContain("secret-rcpt");
      // Gizlilik notu tablonun DIŞINDA yer alır.
      expect(screen.getByText("nodedetail.mail.queue.note")).toBeInTheDocument();
    });
  });

  it("mailbox credential'ları (kind=mailbox) listeler, DB credential'larını filtreler ve tek seferlik reveal eder", async () => {
    const { fetchMock } = setup();
    render(<LocaleProvider><MailPanel nodeId="n1" submitTask={async () => null} t={t} /></LocaleProvider>);
    await waitFor(() => {
      expect(screen.getByText(/ali@mail\.example\.com/)).toBeInTheDocument();
    });
    // postgres credential'ı bu panelde YOK (DbFsPanel'e ait)
    expect(screen.queryByText(/app_ro/)).not.toBeInTheDocument();
    // reveal butonu çalışır
    fireEvent.click(screen.getByText("nodedetail.mail.credentials.reveal"));
    await waitFor(() => {
      expect(screen.getByTestId("mail-revealed-password")).toBeInTheDocument();
      expect(screen.getByText("REVEALED-ONCE-PW")).toBeInTheDocument();
    });
    // reveal sonrası credentials yeniden yüklenir (fetch tekrar çağrıldı)
    const credCalls = fetchMock.mock.calls.filter((c) => String(c[0]).includes("/db-credentials"));
    expect(credCalls.length).toBeGreaterThanOrEqual(2);
  });

  it("preflight raporunu gösterir: notlar + port rozetleri (kriter 1)", () => {
    const { fetchMock, submit } = setup();
    fetchMock.mockImplementation(async (input: string | URL | Request) => {
      const url = String(input);
      if (url.includes("/db-credentials")) {
        return new Response(JSON.stringify({ credentials: [] }), { status: 200 });
      }
      if (url.startsWith("/api/v1/tasks/")) {
        return new Response(
          JSON.stringify(
            okTask("tp", {
              preflight: {
                postfix_installed: false,
                dovecot_installed: true,
                rspamd_installed: false,
                binaries: [],
                ports: [
                  { port: 25, occupied: false, state: "free" },
                  { port: 465, occupied: true, state: "occupied" },
                ],
                vmail_user: false,
                managed_dir_ready: false,
                postfix_wired: false,
                dovecot_wired: false,
                hostname: "node1",
                ptr: { state: "unknown" },
                notes: ["postfix kurulu değil — modül kurulum YAPMAZ; paketi operatör kurar (kriter 1)"],
              },
            }),
          ),
          { status: 200, headers: { "content-type": "application/json" } },
        );
      }
      return new Response("{}", { status: 200 });
    });
    render(<LocaleProvider><MailPanel nodeId="n1" submitTask={submit} t={t} /></LocaleProvider>);
    fireEvent.click(screen.getByText("nodedetail.mail.preflight.run"));
    return waitFor(() => {
      const pf = screen.getByTestId("mail-preflight");
      expect(pf.textContent).toContain("kriter 1");
      expect(pf.textContent).toContain("25:");
      expect(pf.textContent).toContain("nodedetail.mail.preflight.port_occupied");
    });
  });

  it("config check sonuç rozetlerini gösterir (kriter 4 okuma görünümü)", () => {
    const { fetchMock, submit } = setup();
    fetchMock.mockImplementation(async (input: string | URL | Request) => {
      const url = String(input);
      if (url.includes("/db-credentials")) {
        return new Response(JSON.stringify({ credentials: [] }), { status: 200 });
      }
      if (url.startsWith("/api/v1/tasks/")) {
        return new Response(
          JSON.stringify(
            okTask("tc", {
              config_check: {
                postfix: { state: "pass" },
                dovecot: { state: "pass" },
                rspamd: { state: "absent", detail: "rspamadm kurulu değil" },
              },
            }),
          ),
          { status: 200, headers: { "content-type": "application/json" } },
        );
      }
      return new Response("{}", { status: 200 });
    });
    render(<LocaleProvider><MailPanel nodeId="n1" submitTask={submit} t={t} /></LocaleProvider>);
    fireEvent.click(screen.getByText("nodedetail.mail.configcheck.run"));
    return waitFor(() => {
      const cc = screen.getByTestId("mail-configcheck");
      expect(cc.textContent).toContain("postfix: nodedetail.mail.configcheck.pass");
      expect(cc.textContent).toContain("rspamd: nodedetail.mail.configcheck.absent");
    });
  });
});
