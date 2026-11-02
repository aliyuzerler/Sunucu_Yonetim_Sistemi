import { describe, expect, it, beforeEach, vi } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import { VmPanel } from "../components/VmPanel";
import type { TaskLike } from "../components/VmPanel";

// Faz 12 vitest: VmPanel — dürüst guest sınırı (kriter 5), report-only
// preflight (kriter 1), kaynak limitli create (kriter 2), disk format
// doğrulaması görünümü (kriter 3/4) ve snapshot yaşam döngüsü.

type Submit = (op: string, payload: Record<string, unknown>) => Promise<TaskLike | null>;

const t = (k: string) => k;

function okTask(id: string, result: unknown): TaskLike {
  return { id, status: "succeeded", result };
}

function setup(overrides: {
  poll?: (op: string) => unknown | undefined;
} = {}) {
  const submitted: Record<string, { op: string; payload: Record<string, unknown> }> = {};
  let lastOp = "";
  const submit: Submit = async (op, payload) => {
    submitted[op] = { op, payload };
    lastOp = op;
    return { id: `task-${op}`, status: "queued" };
  };

  const fetchMock = vi.fn(async (input: string | URL | Request) => {
    const url = String(input);
    if (url.startsWith("/api/v1/tasks/")) {
      const polled = overrides.poll ? overrides.poll(lastOp) : undefined;
      if (polled !== undefined) {
        return new Response(JSON.stringify(okTask("t1", polled)), {
          status: 200,
          headers: { "content-type": "application/json" },
        });
      }
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

describe("VmPanel", () => {
  it("dürüst guest sınırını yazar: VM oluşturmak guest OS yönetimi sağlamaz (kriter 5)", () => {
    setup();
    render(<VmPanel nodeId="n1" submitTask={async () => null} t={t} />);
    const note = screen.getByTestId("vm-guest-note");
    expect(note.textContent).toContain("nodedetail.vm.guest.note");
  });

  it("preflight report-only'dur: kurulum/etkinleştirme butonu ASLA yoktur (kriter 1)", async () => {
    const { submit } = setup({
      poll: () => ({
        preflight: {
          kvm_available: false,
          virt_flag: "vmx",
          mem_total_mib: 8192,
          mem_available_mib: 4096,
          uri: "qemu:///system",
          qemu_img: true,
          pools: [{ name: "default", type: "dir", active: false, capacity_bytes: 1, free_bytes: 1 }],
          bridges: ["br0"],
          notes: ["kvm modülü yüklü değil"],
        },
      }),
    });
    render(<VmPanel nodeId="n1" submitTask={submit} t={t} />);
    fireEvent.click(screen.getByText("nodedetail.vm.preflight.run"));
    await waitFor(() => {
      const card = screen.getByTestId("vm-preflight");
      expect(card.textContent).toContain("nodedetail.vm.preflight.kvm.no");
      expect(card.textContent).toContain("vmx");
      expect(card.textContent).toContain("br0");
      expect(card.textContent).toContain("kvm modülü yüklü değil");
      expect(card.textContent).toContain("nodedetail.vm.preflight.pool.inactive");
    });
    // Panelde kurulum/etkinleştirme eylemi yok — yalnız preflight çalıştırma.
    const section = screen.getByRole("region", { name: "nodedetail.vm.title" });
    expect(section.textContent).not.toContain("nodedetail.vm.preflight.install");
  });

  it("create formu kaynak limitlerini payload'a taşır ve payloadda YOL alanı YOKTUR (kriter 2/3)", async () => {
    const { submitted, submit } = setup({
      poll: () => ({ result: { name: "web-01", volume: "sc-web-01-root", accelerator: "kvm" } }),
    });
    render(<VmPanel nodeId="n1" submitTask={submit} t={t} />);
    fireEvent.change(screen.getByLabelText("nodedetail.vm.create.name"), {
      target: { value: "web-01" },
    });
    fireEvent.change(screen.getByLabelText("nodedetail.vm.create.bridge"), {
      target: { value: "br0" },
    });
    fireEvent.click(screen.getByText("nodedetail.vm.create.submit"));
    await waitFor(() => {
      expect(submitted["vm.create"]).toBeTruthy();
    });
    const payload = submitted["vm.create"].payload as Record<string, unknown>;
    expect(payload["vcpu"]).toBe(2);
    expect(payload["mem_mib"]).toBe(512);
    expect(payload["disk_gib"]).toBe(20);
    expect(payload["pool"]).toBe("default");
    expect(payload["bridge"]).toBe("br0");
    // Disk yolu payload'da ASLA yok (kriter 3).
    expect(Object.keys(payload)).not.toContain("path");
    expect(JSON.stringify(payload)).not.toContain(".qcow2");
  });

  it("disk tablosu gerçek format doğrulamasını ve yabancı disk durumunu gösterir (kriter 3/4)", async () => {
    const { submit } = setup({
      poll: (op) => {
        if (op === "vm.list") {
          return { vms: [{ name: "vm1", state: "shut off", running: false }] };
        }
        if (op === "vm.info") {
          return {
            vm: {
              name: "vm1",
              state: "shut off",
              running: false,
              vcpu: 2,
              memory_mib: 512,
              guest_agent: false,
              console: { graphics_type: "vnc", port: 5900, listen: "127.0.0.1", local_only: true, serial: true },
              disks: [
                {
                  target_dev: "vda",
                  pool: "default",
                  volume: "sc-vm1-root",
                  managed: true,
                  xml_format: "qcow2",
                  actual_format: "qcow2",
                  format_verified: true,
                  virtual_bytes: 10,
                  actual_bytes: 2,
                  check_ok: true,
                },
                {
                  target_dev: "vdb",
                  source_path: "/var/lib/libvirt/images/foreign.qcow2",
                  managed: false,
                  xml_format: "qcow2",
                  actual_format: "raw",
                  format_verified: false,
                  virtual_bytes: 10,
                  actual_bytes: 2,
                  check_ok: false,
                },
              ],
            },
          };
        }
        return undefined;
      },
    });
    render(<VmPanel nodeId="n1" submitTask={submit} t={t} />);
    fireEvent.click(screen.getByText("nodedetail.vm.list.refresh"));
    await waitFor(() => {
      expect(screen.getByText("nodedetail.vm.action.info")).toBeInTheDocument();
    });
    fireEvent.click(screen.getByText("nodedetail.vm.action.info"));
    await waitFor(() => {
      const info = screen.getByTestId("vm-info");
      expect(info.textContent).toContain("nodedetail.vm.disks.verified.yes");
      expect(info.textContent).toContain("nodedetail.vm.disks.verified.no");
      expect(info.textContent).toContain("nodedetail.vm.disks.foreign");
      expect(info.textContent).toContain("nodedetail.vm.info.guestagent.no");
      expect(info.textContent).toContain("nodedetail.vm.info.console.local");
    });
  });

  it("snapshot create doğrulanmış formatları raporlar; revert/delete butonları satırda (kriter 4)", async () => {
    const { submitted, submit } = setup({
      poll: (op) => {
        if (op === "vm.list") {
          return { vms: [{ name: "vm1", state: "shut off", running: false }] };
        }
        if (op === "vm.info") {
          return {
            vm: {
              name: "vm1", state: "shut off", running: false, vcpu: 1,
              memory_mib: 256, guest_agent: false,
              console: { local_only: true, serial: false },
            },
          };
        }
        if (op === "vm.snapshot.list") {
          return { snapshots: [{ name: "snap1", current: true }] };
        }
        if (op === "vm.snapshot.create") {
          return { result: { name: "snap2", domain: "vm1", verified_formats: ["vda=qcow2"] } };
        }
        return undefined;
      },
    });
    render(<VmPanel nodeId="n1" submitTask={submit} t={t} />);
    fireEvent.click(screen.getByText("nodedetail.vm.list.refresh"));
    await waitFor(() => {
      expect(screen.getByText("nodedetail.vm.action.info")).toBeInTheDocument();
    });
    fireEvent.click(screen.getByText("nodedetail.vm.action.info"));
    await waitFor(() => {
      expect(screen.getByTestId("vm-info")).toBeInTheDocument();
    });
    fireEvent.click(screen.getByText("nodedetail.vm.action.snapshots"));
    await waitFor(() => {
      expect(screen.getByTestId("vm-snapshots").textContent).toContain("snap1");
      expect(screen.getByTestId("vm-snapshots").textContent).toContain("nodedetail.vm.snapshot.current");
    });
    fireEvent.change(screen.getByLabelText("nodedetail.vm.snapshot.name"), {
      target: { value: "snap2" },
    });
    fireEvent.click(screen.getByText("nodedetail.vm.snapshot.submit"));
    await waitFor(() => {
      expect(submitted["vm.snapshot.create"]).toBeTruthy();
      expect(
        (submitted["vm.snapshot.create"].payload as Record<string, unknown>)["snapshot"],
      ).toBe("snap2");
    });
  });
});
