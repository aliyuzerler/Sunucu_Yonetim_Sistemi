import { useEffect, useState } from "react";
import { apiFetch, isApiError } from "../api/client";

// Faz 12: KVM/QEMU/libvirt sanallaştırma paneli (NodeDetail bölümü).
//
// Güvenlik notları (ADR-0016):
//   - Aile node'da --vm-enable ile opt-in'dir; panel preflight'ı RAPORLAR,
//     kurulum/etkinleştirme TEKLİF ETMEZ (kriter 1).
//   - Disk görüntü yolu hiçbir formda TOPLANMAZ: kullanıcı yalnız
//     pool/birim adı ve boyut girer; yollar node'da libvirt'ten türetilir
//     (kriter 3). Silme yalnız sc- önekli yönetilen birimlere dokunur.
//   - Snapshot işlemleri qcow2 kapısı node'dadır; panel gerçek format
//     doğrulamasını (format_verified) dürüstçe gösterir (kriter 4).
//   - VM oluşturmak guest OS yönetimi sağlamaz: guest_agent yoksa in-guest
//     modüller unsupported'tur — panel bunu açıkça yazar (kriter 5).
//   - Konsol METADATA (vnc port/dinleme adresi) yalnız görüntülenir;
//     panel konsol akışı/proxy içermez.

export interface TaskLike {
  id: string;
  status: string;
  result?: unknown;
  error_category?: string;
}

interface PoolRow {
  name: string;
  type: string;
  active: boolean;
  capacity_bytes: number;
  free_bytes: number;
}
interface PreflightReport {
  kvm_available: boolean;
  cpu_model?: string;
  cpu_flags?: string[];
  virt_flag?: string;
  mem_total_mib: number;
  mem_available_mib: number;
  libvirt_version?: string;
  hypervisor?: string;
  uri: string;
  qemu_img: boolean;
  pools?: PoolRow[];
  bridges?: string[];
  networks?: string[];
  notes?: string[];
}
interface StatusReport {
  uri: string;
  libvirt_version?: string;
  hypervisor?: string;
  domains_total: number;
  running: number;
  kvm_available: boolean;
}
interface VMRow {
  name: string;
  state: string;
  running: boolean;
}
interface DiskInfo {
  target_dev: string;
  source_path?: string;
  pool?: string;
  volume?: string;
  managed: boolean;
  xml_format?: string;
  actual_format?: string;
  format_verified: boolean;
  virtual_bytes: number;
  actual_bytes: number;
  check_ok: boolean;
  check_detail?: string;
}
interface NicInfo {
  mac?: string;
  bridge?: string;
  network?: string;
  model?: string;
}
interface VMInfo {
  name: string;
  uuid?: string;
  state: string;
  running: boolean;
  vcpu: number;
  memory_mib: number;
  arch?: string;
  guest_agent: boolean;
  disks?: DiskInfo[];
  interfaces?: NicInfo[];
  console: {
    graphics_type?: string;
    listen?: string;
    port?: number;
    local_only: boolean;
    serial: boolean;
  };
}
interface SnapshotRow {
  name: string;
  current: boolean;
}
interface CreateReport {
  name: string;
  volume: string;
  accelerator: string;
  state?: string;
}
interface MutateReport {
  name: string;
  action: string;
  already?: boolean;
  forced?: boolean;
  state?: string;
}
interface DeleteReport {
  name: string;
  undefined: boolean;
  volumes_removed?: string[];
}
interface DiskAddReport {
  name: string;
  pool: string;
  volume: string;
  target_dev: string;
  size_gib: number;
}
interface SnapshotCreateReport {
  name: string;
  domain: string;
  verified_formats?: string[];
}

const inputCls =
  "rounded-lg border border-border bg-surface px-3 py-1.5 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent";
const btnCls =
  "rounded-lg border border-border bg-surface px-3 py-1.5 text-xs font-medium text-ink hover:bg-neutral-soft disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent";
const btnDangerCls =
  "rounded-lg border border-red-300 bg-surface px-3 py-1.5 text-xs font-medium text-red-700 hover:bg-red-50 disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent";

export function VmPanel(props: {
  nodeId: string;
  submitTask: (op: string, payload: Record<string, unknown>) => Promise<TaskLike | null>;
  t: (k: string) => string;
}) {
  const { submitTask, t } = props;

  const [pending, setPending] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [report, setReport] = useState<Record<string, unknown> | null>(null);
  const [taskId, setTaskId] = useState<string | null>(null);
  const [taskOp, setTaskOp] = useState<string>("");

  // form state
  const [cName, setCName] = useState("");
  const [cVcpu, setCVcpu] = useState("2");
  const [cMem, setCMem] = useState("512");
  const [cDisk, setCDisk] = useState("20");
  const [cPool, setCPool] = useState("default");
  const [cBridge, setCBridge] = useState("");
  const [clSource, setClSource] = useState("");
  const [clName, setClName] = useState("");
  const [dPool, setDPool] = useState("default");
  const [dSize, setDSize] = useState("10");
  const [sName, setSName] = useState("");
  const [sDesc, setSDesc] = useState("");
  const [force, setForce] = useState(false);
  const [confirmName, setConfirmName] = useState("");

  // report state
  const [preflight, setPreflight] = useState<PreflightReport | null>(null);
  const [status, setStatus] = useState<StatusReport | null>(null);
  const [vms, setVms] = useState<VMRow[]>([]);
  const [info, setInfo] = useState<VMInfo | null>(null);
  const [snapshots, setSnapshots] = useState<SnapshotRow[]>([]);
  const [lastCreate, setLastCreate] = useState<CreateReport | null>(null);
  const [lastMutate, setLastMutate] = useState<MutateReport | null>(null);
  const [lastDelete, setLastDelete] = useState<DeleteReport | null>(null);
  const [lastDiskAdd, setLastDiskAdd] = useState<DiskAddReport | null>(null);
  const [lastSnapshot, setLastSnapshot] = useState<SnapshotCreateReport | null>(null);

  async function run(op: string, payload: Record<string, unknown>) {
    setError(null);
    setReport(null);
    setPending(op);
    const created = await submitTask(op, payload);
    setPending(null);
    if (!created) return;
    setTaskId(created.id);
    setTaskOp(op);
  }

  useEffect(() => {
    if (!taskId) return;
    let cancelled = false;
    let attempts = 0;
    const tick = async () => {
      attempts += 1;
      try {
        const task = await apiFetch<TaskLike>(`/api/v1/tasks/${encodeURIComponent(taskId)}`);
        if (cancelled) return;
        if (task.status === "succeeded") {
          const result = (task.result ?? null) as Record<string, unknown> | null;
          setReport(result);
          applyResult(taskOp, result);
          setTaskId(null);
          return;
        }
        if (task.status === "failed" || task.status === "rejected" || task.status === "cancelled") {
          setError(
            `${t("nodedetail.vm.error.task")}: ${task.error_category ?? task.status}`,
          );
          setTaskId(null);
          return;
        }
        if (attempts < 60) {
          setTimeout(tick, 700);
        } else {
          if (!cancelled) setError(t("nodedetail.vm.error.timeout"));
          setTaskId(null);
        }
      } catch (e) {
        if (!cancelled && attempts < 60) {
          setTimeout(tick, 900);
        } else if (!cancelled) {
          setError(isApiError(e) ? e.message : t("nodedetail.vm.error.task"));
          setTaskId(null);
        }
      }
    };
    const h = setTimeout(tick, 400);
    return () => {
      cancelled = true;
      clearTimeout(h);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [taskId]);

  function applyResult(op: string, result: Record<string, unknown> | null) {
    if (!result) return;
    switch (op) {
      case "vm.preflight":
        setPreflight(result.preflight as PreflightReport);
        break;
      case "vm.status":
        setStatus(result.status as StatusReport);
        break;
      case "vm.list":
        setVms((result.vms as VMRow[]) ?? []);
        break;
      case "vm.info":
        setInfo(result.vm as VMInfo);
        break;
      case "vm.snapshot.list":
        setSnapshots((result.snapshots as SnapshotRow[]) ?? []);
        break;
      case "vm.create":
        setLastCreate(result.result as CreateReport);
        void run("vm.list", {}); // yeni VM listede görünsün
        break;
      case "vm.clone":
        setLastMutate(result.result as MutateReport);
        void run("vm.list", {});
        break;
      case "vm.start":
        setLastMutate(result.result as MutateReport);
        void run("vm.list", {}); // durum değişimi listede güncellensin
        break;
      case "vm.stop":
        setLastMutate(result.result as MutateReport);
        void run("vm.list", {});
        break;
      case "vm.reboot":
        setLastMutate(result.result as MutateReport);
        break;
      case "vm.delete":
        setLastDelete(result.result as DeleteReport);
        void run("vm.list", {}); // silinen VM listeden düşsün
        break;
      case "vm.disk.add":
        setLastDiskAdd(result.result as DiskAddReport);
        break;
      case "vm.snapshot.create": {
        const sc = result.result as SnapshotCreateReport;
        setLastSnapshot(sc);
        if (sc?.domain) void run("vm.snapshot.list", { name: sc.domain }); // yeni snapshot listede görünsün
        break;
      }
      case "vm.snapshot.revert":
      case "vm.snapshot.delete":
        setLastMutate(result.result as MutateReport);
        break;
    }
  }

  function errText(e: unknown): string {
    if (isApiError(e)) return e.message;
    return t("nodedetail.vm.error.task");
  }

  const busy = pending !== null;

  return (
    <section
      aria-label={t("nodedetail.vm.title")}
      className="rounded-xl border border-border bg-surface p-4"
    >
      <h3 className="text-sm font-semibold text-ink">{t("nodedetail.vm.title")}</h3>
      <p className="mt-1 text-xs text-neutral">{t("nodedetail.vm.note")}</p>

      {/* Kriter 5: VM oluşturmak guest OS yönetimi sağlamaz — dürüst sınır. */}
      <div
        data-testid="vm-guest-note"
        role="note"
        className="mt-2 rounded-lg border border-amber-300 bg-amber-50 px-3 py-2 text-xs text-amber-900"
      >
        {t("nodedetail.vm.guest.note")}
      </div>

      {error && (
        <p role="alert" className="mt-2 text-xs text-err">
          {error}
        </p>
      )}

      {/* Preflight — yalnız rapor (kriter 1). */}
      <div data-testid="vm-preflight" className="mt-3 rounded-lg border border-border p-3">
        <div className="flex items-center justify-between">
          <h4 className="text-xs font-semibold text-ink">
            {t("nodedetail.vm.preflight.title")}
          </h4>
          <button
            type="button"
            className={btnCls}
            disabled={busy}
            onClick={() => {
              run("vm.preflight", {}).catch((e) => setError(errText(e)));
            }}
          >
            {t("nodedetail.vm.preflight.run")}
          </button>
        </div>
        {preflight && (
          <div className="mt-2 space-y-1 text-xs text-neutral">
            <p>
              <span className="font-medium text-ink">KVM:</span>{" "}
              {preflight.kvm_available
                ? t("nodedetail.vm.preflight.kvm.yes")
                : t("nodedetail.vm.preflight.kvm.no")}
              {preflight.virt_flag ? ` (${preflight.virt_flag})` : ""}
            </p>
            <p>
              <span className="font-medium text-ink">{t("nodedetail.vm.preflight.mem")}:</span>{" "}
              {preflight.mem_available_mib} / {preflight.mem_total_mib} MiB
            </p>
            <p>
              <span className="font-medium text-ink">libvirt:</span>{" "}
              {preflight.libvirt_version || t("nodedetail.vm.preflight.libvirt.missing")} ·{" "}
              {preflight.hypervisor || "—"} · {preflight.uri}
            </p>
            <p>
              <span className="font-medium text-ink">qemu-img:</span>{" "}
              {preflight.qemu_img
                ? t("nodedetail.vm.preflight.qemuimg.yes")
                : t("nodedetail.vm.preflight.qemuimg.no")}
            </p>
            {preflight.pools && preflight.pools.length > 0 && (
              <p>
                <span className="font-medium text-ink">{t("nodedetail.vm.preflight.pools")}:</span>{" "}
                {preflight.pools
                  .map((p) => `${p.name}${p.active ? "" : " (" + t("nodedetail.vm.preflight.pool.inactive") + ")"}`)
                  .join(", ")}
              </p>
            )}
            {preflight.bridges && preflight.bridges.length > 0 && (
              <p>
                <span className="font-medium text-ink">{t("nodedetail.vm.preflight.bridges")}:</span>{" "}
                {preflight.bridges.join(", ")}
              </p>
            )}
            {preflight.networks && preflight.networks.length > 0 && (
              <p>
                <span className="font-medium text-ink">{t("nodedetail.vm.preflight.networks")}:</span>{" "}
                {preflight.networks.join(", ")}
              </p>
            )}
            {preflight.notes && preflight.notes.length > 0 && (
              <ul className="list-disc pl-4">
                {preflight.notes.map((n, i) => (
                  <li key={i}>{n}</li>
                ))}
              </ul>
            )}
          </div>
        )}
      </div>

      {/* Status + liste. */}
      <div className="mt-3 rounded-lg border border-border p-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h4 className="text-xs font-semibold text-ink">{t("nodedetail.vm.list.title")}</h4>
          <div className="flex gap-2">
            <button
              type="button"
              className={btnCls}
              disabled={busy}
              onClick={() => {
                run("vm.status", {}).catch((e) => setError(errText(e)));
              }}
            >
              {t("nodedetail.vm.status.run")}
            </button>
            <button
              type="button"
              className={btnCls}
              disabled={busy}
              onClick={() => {
                run("vm.list", {}).catch((e) => setError(errText(e)));
              }}
            >
              {t("nodedetail.vm.list.refresh")}
            </button>
          </div>
        </div>
        {status && (
          <p className="mt-2 text-xs text-neutral">
            libvirt {status.libvirt_version || "—"} · {status.hypervisor || "—"} ·{" "}
            {status.domains_total} VM · {status.running}{" "}
            {t("nodedetail.vm.list.running")}
          </p>
        )}
        <div data-testid="vm-list" className="mt-2 overflow-x-auto">
          {vms.length === 0 ? (
            <p className="text-xs text-neutral">{t("nodedetail.vm.list.empty")}</p>
          ) : (
            <table className="w-full text-left text-xs">
              <thead>
                <tr className="text-neutral">
                  <th className="py-1 pr-3">{t("nodedetail.vm.list.name")}</th>
                  <th className="py-1 pr-3">{t("nodedetail.vm.list.state")}</th>
                  <th className="py-1 pr-3">{t("nodedetail.vm.list.actions")}</th>
                </tr>
              </thead>
              <tbody>
                {vms.map((vm) => (
                  <tr key={vm.name} className="border-t border-border">
                    <td className="py-1 pr-3 font-medium text-ink">{vm.name}</td>
                    <td className="py-1 pr-3">{vm.state}</td>
                    <td className="py-1 pr-3">
                      <div className="flex flex-wrap gap-1">
                        <button
                          type="button"
                          className={btnCls}
                          disabled={busy}
                          onClick={() => {
                            run("vm.info", { name: vm.name }).catch((e) => setError(errText(e)));
                          }}
                        >
                          {t("nodedetail.vm.action.info")}
                        </button>
                        {!vm.running && (
                          <button
                            type="button"
                            className={btnCls}
                            disabled={busy}
                            onClick={() => {
                              run("vm.start", { name: vm.name }).catch((e) => setError(errText(e)));
                            }}
                          >
                            {t("nodedetail.vm.action.start")}
                          </button>
                        )}
                        {vm.running && (
                          <>
                            <button
                              type="button"
                              className={btnCls}
                              disabled={busy}
                              onClick={() => {
                                run("vm.stop", { name: vm.name, force }).catch((e) =>
                                  setError(errText(e)),
                                );
                              }}
                            >
                              {force
                                ? t("nodedetail.vm.action.stopforce")
                                : t("nodedetail.vm.action.stop")}
                            </button>
                            <button
                              type="button"
                              className={btnCls}
                              disabled={busy}
                              onClick={() => {
                                run("vm.reboot", { name: vm.name }).catch((e) =>
                                  setError(errText(e)),
                                );
                              }}
                            >
                              {t("nodedetail.vm.action.reboot")}
                            </button>
                          </>
                        )}
                        <button
                          type="button"
                          className={btnCls}
                          disabled={busy}
                          onClick={() => {
                            run("vm.disk.list", { name: vm.name }).catch((e) =>
                              setError(errText(e)),
                            );
                          }}
                        >
                          {t("nodedetail.vm.action.disks")}
                        </button>
                        <button
                          type="button"
                          className={btnCls}
                          disabled={busy}
                          onClick={() => {
                            run("vm.snapshot.list", { name: vm.name }).catch((e) =>
                              setError(errText(e)),
                            );
                          }}
                        >
                          {t("nodedetail.vm.action.snapshots")}
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
        <label className="mt-2 flex items-center gap-2 text-xs text-neutral">
          <input
            type="checkbox"
            checked={force}
            onChange={(e) => setForce(e.target.checked)}
          />
          {t("nodedetail.vm.list.force")}
        </label>
        {lastMutate && (
          <p className="mt-1 text-xs text-neutral">
            {lastMutate.action}
            {lastMutate.forced ? " (destroy)" : ""}
            {lastMutate.already ? ` · ${t("nodedetail.vm.mutate.already")}` : ""}
            {lastMutate.state ? ` · ${lastMutate.state}` : ""}
          </p>
        )}
      </div>

      {/* VM info + diskler (kriter 3/4 doğrulaması görünür). */}
      {info && (
        <div data-testid="vm-info" className="mt-3 rounded-lg border border-border p-3">
          <h4 className="text-xs font-semibold text-ink">
            {t("nodedetail.vm.info.title")} — {info.name}
          </h4>
          <div className="mt-2 space-y-1 text-xs text-neutral">
            <p>
              {info.uuid || "—"} · {info.arch || "—"} · {info.vcpu} vCPU ·{" "}
              {info.memory_mib} MiB · {info.state}
            </p>
            <p>
              <span className="font-medium text-ink">{t("nodedetail.vm.info.guestagent")}:</span>{" "}
              {info.guest_agent
                ? t("nodedetail.vm.info.guestagent.yes")
                : t("nodedetail.vm.info.guestagent.no")}
            </p>
            <p>
              <span className="font-medium text-ink">{t("nodedetail.vm.info.console")}:</span>{" "}
              {info.console.graphics_type || "—"}
              {info.console.port ? ` :${info.console.port}` : ""} ·{" "}
              {info.console.listen || "—"} ·{" "}
              {info.console.local_only
                ? t("nodedetail.vm.info.console.local")
                : t("nodedetail.vm.info.console.remote")}
            </p>
            {info.interfaces && info.interfaces.length > 0 && (
              <p>
                <span className="font-medium text-ink">{t("nodedetail.vm.info.nic")}:</span>{" "}
                {info.interfaces
                  .map((n) => n.network ? `net:${n.network}` : n.bridge ? `br:${n.bridge}` : "—")
                  .join(", ")}
              </p>
            )}
          </div>
          <div data-testid="vm-disks" className="mt-2 overflow-x-auto">
            <table className="w-full text-left text-xs">
              <thead>
                <tr className="text-neutral">
                  <th className="py-1 pr-3">{t("nodedetail.vm.disks.dev")}</th>
                  <th className="py-1 pr-3">{t("nodedetail.vm.disks.volume")}</th>
                  <th className="py-1 pr-3">{t("nodedetail.vm.disks.format")}</th>
                  <th className="py-1 pr-3">{t("nodedetail.vm.disks.verified")}</th>
                </tr>
              </thead>
              <tbody>
                {(info.disks ?? []).map((d) => (
                  <tr key={d.target_dev} className="border-t border-border">
                    <td className="py-1 pr-3">{d.target_dev}</td>
                    <td className="py-1 pr-3">
                      {d.managed
                        ? `${d.pool}/${d.volume}`
                        : d.source_path || "—"}
                      {!d.managed && (
                        <span className="ml-1 text-amber-700">
                          ({t("nodedetail.vm.disks.foreign")})
                        </span>
                      )}
                    </td>
                    <td className="py-1 pr-3">
                      {d.xml_format || "—"} / {d.actual_format || "—"}
                    </td>
                    <td className="py-1 pr-3">
                      {d.format_verified
                        ? t("nodedetail.vm.disks.verified.yes")
                        : t("nodedetail.vm.disks.verified.no")}
                      {d.check_detail ? ` · ${d.check_detail}` : ""}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Create form — kaynak limitleri kapalı aralık (kriter 2). */}
      <form
        data-testid="vm-create"
        className="mt-3 rounded-lg border border-border p-3"
        onSubmit={(e) => {
          e.preventDefault();
          setLastCreate(null);
          const vcpu = Number(cVcpu);
          const mem = Number(cMem);
          const disk = Number(cDisk);
          if (!cName || !Number.isFinite(vcpu) || !Number.isFinite(mem) || !Number.isFinite(disk)) {
            setError(t("nodedetail.vm.create.invalid"));
            return;
          }
          run("vm.create", {
            name: cName.trim(),
            vcpu,
            mem_mib: mem,
            disk_gib: disk,
            pool: cPool.trim(),
            bridge: cBridge.trim(),
          }).catch((e) => setError(errText(e)));
        }}
      >
        <h4 className="text-xs font-semibold text-ink">{t("nodedetail.vm.create.title")}</h4>
        <div className="mt-2 grid grid-cols-2 gap-2 md:grid-cols-3">
          <label className="text-xs text-neutral">
            {t("nodedetail.vm.create.name")}
            <input
              className={`${inputCls} mt-1 w-full`}
              value={cName}
              onChange={(e) => setCName(e.target.value)}
              placeholder="web-01"
              required
            />
          </label>
          <label className="text-xs text-neutral">
            vCPU (1–64)
            <input
              className={`${inputCls} mt-1 w-full`}
              type="number"
              min={1}
              max={64}
              value={cVcpu}
              onChange={(e) => setCVcpu(e.target.value)}
            />
          </label>
          <label className="text-xs text-neutral">
            {t("nodedetail.vm.create.mem")} (MiB, 128–262144)
            <input
              className={`${inputCls} mt-1 w-full`}
              type="number"
              min={128}
              max={262144}
              value={cMem}
              onChange={(e) => setCMem(e.target.value)}
            />
          </label>
          <label className="text-xs text-neutral">
            {t("nodedetail.vm.create.disk")} (GiB, 1–4096)
            <input
              className={`${inputCls} mt-1 w-full`}
              type="number"
              min={1}
              max={4096}
              value={cDisk}
              onChange={(e) => setCDisk(e.target.value)}
            />
          </label>
          <label className="text-xs text-neutral">
            {t("nodedetail.vm.create.pool")}
            <input
              className={`${inputCls} mt-1 w-full`}
              value={cPool}
              onChange={(e) => setCPool(e.target.value)}
            />
          </label>
          <label className="text-xs text-neutral">
            {t("nodedetail.vm.create.bridge")}
            <input
              className={`${inputCls} mt-1 w-full`}
              value={cBridge}
              onChange={(e) => setCBridge(e.target.value)}
              placeholder="br0"
              required
            />
          </label>
        </div>
        <p className="mt-1 text-xs text-neutral">{t("nodedetail.vm.create.hint")}</p>
        <button type="submit" className={`${btnCls} mt-2`} disabled={busy}>
          {t("nodedetail.vm.create.submit")}
        </button>
        {lastCreate && (
          <p className="mt-1 text-xs text-neutral">
            {t("nodedetail.vm.create.done")}: {lastCreate.name} · {lastCreate.volume} ·{" "}
            {lastCreate.accelerator}
          </p>
        )}
      </form>

      {/* Clone + silme. */}
      <div className="mt-3 grid gap-3 md:grid-cols-2">
        <form
          className="rounded-lg border border-border p-3"
          onSubmit={(e) => {
            e.preventDefault();
            setLastMutate(null);
            run("vm.clone", { source: clSource.trim(), name: clName.trim() }).catch((e2) =>
              setError(errText(e2)),
            );
          }}
        >
          <h4 className="text-xs font-semibold text-ink">{t("nodedetail.vm.clone.title")}</h4>
          <p className="mt-1 text-xs text-neutral">{t("nodedetail.vm.clone.hint")}</p>
          <div className="mt-2 flex gap-2">
            <input
              className={`${inputCls} w-full`}
              value={clSource}
              onChange={(e) => setClSource(e.target.value)}
              placeholder="src-vm"
              aria-label={t("nodedetail.vm.clone.source")}
              required
            />
            <input
              className={`${inputCls} w-full`}
              value={clName}
              onChange={(e) => setClName(e.target.value)}
              placeholder="clone-vm"
              aria-label={t("nodedetail.vm.clone.name")}
              required
            />
          </div>
          <button type="submit" className={`${btnCls} mt-2`} disabled={busy}>
            {t("nodedetail.vm.clone.submit")}
          </button>
        </form>
        <form
          className="rounded-lg border border-red-200 p-3"
          onSubmit={(e) => {
            e.preventDefault();
            if (confirmName.trim() === "") {
              setError(t("nodedetail.vm.delete.confirmfirst"));
              return;
            }
            setLastDelete(null);
            run("vm.delete", { name: confirmName.trim() }).catch((e2) => setError(errText(e2)));
          }}
        >
          <h4 className="text-xs font-semibold text-ink">{t("nodedetail.vm.delete.title")}</h4>
          <p className="mt-1 text-xs text-neutral">{t("nodedetail.vm.delete.hint")}</p>
          <input
            className={`${inputCls} mt-2 w-full`}
            value={confirmName}
            onChange={(e) => setConfirmName(e.target.value)}
            placeholder="vm-adi"
            aria-label={t("nodedetail.vm.delete.name")}
          />
          <button type="submit" className={`${btnDangerCls} mt-2`} disabled={busy}>
            {t("nodedetail.vm.delete.submit")}
          </button>
          {lastDelete && (
            <p className="mt-1 text-xs text-neutral">
              {t("nodedetail.vm.delete.done")}: {(lastDelete.volumes_removed ?? []).join(", ") || "—"}
            </p>
          )}
        </form>
      </div>

      {/* Disk ekle (yol YOK — pool+boyut; kriter 3). */}
      <form
        className="mt-3 rounded-lg border border-border p-3"
        onSubmit={(e) => {
          e.preventDefault();
          if (!info) {
            setError(t("nodedetail.vm.disks.selectfirst"));
            return;
          }
          setLastDiskAdd(null);
          const size = Number(dSize);
          if (!Number.isFinite(size)) {
            setError(t("nodedetail.vm.create.invalid"));
            return;
          }
          run("vm.disk.add", { name: info.name, pool: dPool.trim(), size_gib: size }).catch((e2) =>
            setError(errText(e2)),
          );
        }}
      >
        <h4 className="text-xs font-semibold text-ink">{t("nodedetail.vm.disks.add.title")}</h4>
        <p className="mt-1 text-xs text-neutral">{t("nodedetail.vm.disks.add.hint")}</p>
        <div className="mt-2 flex flex-wrap gap-2">
          <input
            className={inputCls}
            value={dPool}
            onChange={(e) => setDPool(e.target.value)}
            aria-label={t("nodedetail.vm.disks.add.pool")}
          />
          <input
            className={inputCls}
            type="number"
            min={1}
            max={4096}
            value={dSize}
            onChange={(e) => setDSize(e.target.value)}
            aria-label={t("nodedetail.vm.disks.add.size")}
          />
          <button type="submit" className={btnCls} disabled={busy || !info}>
            {t("nodedetail.vm.disks.add.submit")}
          </button>
        </div>
        {lastDiskAdd && (
          <p className="mt-1 text-xs text-neutral">
            {lastDiskAdd.target_dev} · {lastDiskAdd.pool}/{lastDiskAdd.volume} ·{" "}
            {lastDiskAdd.size_gib} GiB
          </p>
        )}
      </form>

      {/* Snapshot yönetimi (qcow2 kapısı node'da — kriter 4). */}
      <div className="mt-3 rounded-lg border border-border p-3">
        <h4 className="text-xs font-semibold text-ink">{t("nodedetail.vm.snapshot.title")}</h4>
        <p className="mt-1 text-xs text-neutral">{t("nodedetail.vm.snapshot.hint")}</p>
        <div data-testid="vm-snapshots" className="mt-2">
          {snapshots.length === 0 ? (
            <p className="text-xs text-neutral">{t("nodedetail.vm.snapshot.empty")}</p>
          ) : (
            <ul className="space-y-1 text-xs">
              {snapshots.map((s) => (
                <li key={s.name} className="flex flex-wrap items-center gap-2">
                  <span className="font-medium text-ink">{s.name}</span>
                  {s.current && (
                    <span className="rounded bg-neutral-soft px-1 text-neutral">
                      {t("nodedetail.vm.snapshot.current")}
                    </span>
                  )}
                  <button
                    type="button"
                    className={btnCls}
                    disabled={busy || !info}
                    onClick={() => {
                      setLastMutate(null);
                      if (!info) return;
                      run("vm.snapshot.revert", { name: info.name, snapshot: s.name }).catch((e) =>
                        setError(errText(e)),
                      );
                    }}
                  >
                    {t("nodedetail.vm.snapshot.revert")}
                  </button>
                  <button
                    type="button"
                    className={btnDangerCls}
                    disabled={busy || !info}
                    onClick={() => {
                      setLastMutate(null);
                      if (!info) return;
                      run("vm.snapshot.delete", { name: info.name, snapshot: s.name }).catch((e) =>
                        setError(errText(e)),
                      );
                    }}
                  >
                    {t("nodedetail.vm.snapshot.delete")}
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
        <form
          className="mt-2 flex flex-wrap items-end gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (!info) {
              setError(t("nodedetail.vm.snapshot.selectfirst"));
              return;
            }
            setLastSnapshot(null);
            run("vm.snapshot.create", {
              name: info.name,
              snapshot: sName.trim(),
              description: sDesc.trim(),
            }).catch((e2) => setError(errText(e2)));
          }}
        >
          <label className="text-xs text-neutral">
            {t("nodedetail.vm.snapshot.name")}
            <input
              className={`${inputCls} mt-1 block w-40`}
              value={sName}
              onChange={(e) => setSName(e.target.value)}
              required
            />
          </label>
          <label className="text-xs text-neutral">
            {t("nodedetail.vm.snapshot.desc")}
            <input
              className={`${inputCls} mt-1 block w-56`}
              value={sDesc}
              onChange={(e) => setSDesc(e.target.value)}
            />
          </label>
          <button type="submit" className={btnCls} disabled={busy || !info}>
            {t("nodedetail.vm.snapshot.submit")}
          </button>
        </form>
        {lastSnapshot && (
          <p className="mt-1 text-xs text-neutral">
            {t("nodedetail.vm.snapshot.done")}: {lastSnapshot.name} ·{" "}
            {(lastSnapshot.verified_formats ?? []).join(", ")}
          </p>
        )}
        {report && !lastCreate && !lastDelete && !lastDiskAdd && !lastSnapshot && !lastMutate && (
          <pre className="mt-2 overflow-x-auto rounded bg-neutral-soft p-2 text-xs">
            {JSON.stringify(report)}
          </pre>
        )}
      </div>
    </section>
  );
}
