import { useCallback, useEffect, useState } from "react";
import type { FormEvent } from "react";
import { apiFetch, isApiError } from "../api/client";
import { DbFsPanel } from "../components/DbFsPanel";
import { MailPanel } from "../components/MailPanel";
import type {
  CapabilityRecord,
  MetricsSnapshot,
  NodeRecord,
  SystemFactsRecord,
  TaskRecord,
} from "../api/client";
import { StatusBadge } from "../components/StatusBadge";
import type { BadgeVariant } from "../components/StatusBadge";
import { useI18n } from "../i18n/LocaleProvider";

// Faz 04/05 allowlist'leri — istemci tarafında da uygulanır; gerçek kural
// sunucu + agent + broker tarafında zorlanır (gizleme yetkilendirme değildir).
const UNIT_PATTERN = /^[A-Za-z0-9][A-Za-z0-9@._-]{0,246}\.service$/;
const SCM_PATTERN = /^[A-Za-z0-9][A-Za-z0-9 ._-]{0,79}$/;

function validScmName(name: string): boolean {
  if (!SCM_PATTERN.test(name)) return false;
  const last = name[name.length - 1];
  return last !== "." && last !== " ";
}

const LINUX_SERVICE_OPS = [
  "linux.systemd.service.list",
  "linux.systemd.service.status",
  "linux.systemd.service.start",
  "linux.systemd.service.stop",
  "linux.systemd.service.restart",
] as const;

const WINDOWS_SERVICE_OPS = [
  "windows.scm.service.list",
  "windows.scm.service.status",
  "windows.scm.service.start",
  "windows.scm.service.stop",
  "windows.scm.service.restart",
] as const;

const ALL_SERVICE_OPS = [...LINUX_SERVICE_OPS, ...WINDOWS_SERVICE_OPS];

type ServiceOp = (typeof ALL_SERVICE_OPS)[number];

// Faz 06: salt-okuma dağıtım işlemleri (paket envanteri + firewall sörveyi
// ve önizleme). Önizleme hiçbir komut çalıştırmaz (ADR-0010); UI'da da
// açıkça etiketlenir.
const SURVEY_OPS = [
  "linux.pkg.list",
  "linux.firewall.status",
  "linux.firewall.rules.list",
  "linux.firewall.rule.preview",
] as const;
type SurveyOp = (typeof SURVEY_OPS)[number];

// FW_ZONE_PATTERN mirrors agent/internal/fwops + backend validator.
const FW_ZONE_PATTERN = /^[A-Za-z0-9_.-]{0,32}$/;
const FW_ACTIONS = ["allow", "revoke"] as const;
type FwAction = (typeof FW_ACTIONS)[number];

// Faz 07: Docker Engine ailesi (broker-tekil erişim; remove ailesi ve
// compose.deploy PREVIEW-ONLY'dir — ADR-0011; UI da "çalıştırmaz" etiketli).
const DOCKER_LIST_OPS = [
  "docker.container.list",
  "docker.image.list",
  "docker.network.list",
  "docker.volume.list",
] as const;
const DOCKER_ACTION_OPS = [
  "docker.container.start",
  "docker.container.stop",
  "docker.container.restart",
] as const;
const DOCKER_REMOVE_OPS = [
  "docker.container.remove",
  "docker.image.remove",
  "docker.volume.remove",
  "docker.network.remove",
] as const;
const COMPOSE_OPS = ["docker.compose.validate", "docker.compose.deploy"] as const;
const ALL_DOCKER_OPS = [...DOCKER_LIST_OPS, ...DOCKER_ACTION_OPS, ...DOCKER_REMOVE_OPS, ...COMPOSE_OPS] as const;
type DockerListOp = (typeof DOCKER_LIST_OPS)[number];
type DockerRemoveOp = (typeof DOCKER_REMOVE_OPS)[number];
type ComposeOp = (typeof COMPOSE_OPS)[number];

// DOCKER_REF/IMAGE_REF patterns mirror agent/internal/dockerx + backend.
const DOCKER_REF_PATTERN = /^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$/;
const DOCKER_IMAGE_REF_PATTERN = /^[A-Za-z0-9][A-Za-z0-9_.:@/-]{0,199}$/;
function validDockerRef(ref: string, image?: boolean): boolean {
  const base = (image ?? false) ? DOCKER_IMAGE_REF_PATTERN : DOCKER_REF_PATTERN;
  if (!base.test(ref)) return false;
  return !ref.includes("..");
}
const REMOVE_KINDS = ["container", "image", "volume", "network"] as const;
type RemoveKind = (typeof REMOVE_KINDS)[number];

// Faz 08: Web hosting ailesi (nginx + PHP-FPM). Gerçek yürütme yalnız
// broker'da --web-enable arkasındadır (ADR-0012); delete dosya-koruyucudur
// (docroot korunur) ve UI da bunu açıkça etiketler.
// WEB_SITE_PATTERN mirrors agent/internal/webops + backend validator.
const WEB_SITE_PATTERN = /^[a-z0-9](?:[a-z0-9-]{0,22}[a-z0-9])?$/;
const WEB_LABEL_PATTERN = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;
const WEB_PHP_VERSIONS = ["8.2", "8.3", "8.4", "8.5"] as const;
const WEB_BACKENDS = ["static", "php", "proxy"] as const;
type WebBackend = (typeof WEB_BACKENDS)[number];

function validWebServerName(host: string): boolean {
  if (!host || host.length > 253 || host.includes("..")) return false;
  return host.split(".").every((l) => WEB_LABEL_PATTERN.test(l));
}
function validWebProxyTarget(target: string): boolean {
  const v4 = /^127\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}:[0-9]{1,5}$/;
  const v6 = /^\[::1\]:[0-9]{1,5}$/;
  const sock = /^unix:\/run\/[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$/;
  if (v4.test(target)) {
    const [host, port] = target.split(":");
    if (!port || Number(port) < 1 || Number(port) > 65535) return false;
    const octets = host.split(".");
    if (octets.length !== 4 || octets[0] !== "127") return false;
    return octets.every((o) => o !== "" && Number(o) <= 255);
  }
  if (v6.test(target)) return Number(target.split("]:")[1]) >= 1 && Number(target.split("]:")[1]) <= 65535;
  if (sock.test(target)) return !target.includes("..");
  return false;
}

// Task result shapes (mirrors webops).
interface WebSiteInfo {
  name: string;
  backend?: string;
  php_version?: string;
  port?: number;
  enabled?: boolean;
  user?: string;
  user_exists?: boolean;
  docroot?: string;
  docroot_exists?: boolean;
  server_names?: string[];
}
interface WebSiteResult {
  site: string;
  action: string;
  executed?: boolean;
  config_path?: string;
  rollback_path?: string;
  docroot?: string;
  docroot_preserved?: boolean;
  user?: string;
  php_version?: string;
  pool_path?: string;
  notes?: string[];
}
interface WebSiteStatus {
  site: WebSiteInfo;
  config_path?: string;
  rollback_path?: string;
  has_rollback?: boolean;
  pool_path?: string;
  pool_installed?: boolean;
  listener?: string;
}
interface WebPoolInfo { site: string; version: string; path: string }
interface WebPHPMatrix {
  supported: string[];
  installed: { version: string; unit: string; pool_dir: string }[];
  pools?: WebPoolInfo[];
}
interface WebReport {
  sites?: WebSiteInfo[];
  status?: WebSiteStatus;
  result?: WebSiteResult;
  php?: WebPHPMatrix;
}

// Compose analizi sonucu (agent composeops raporu — task result içinden).
interface ComposeFinding { severity: string; rule: string; service?: string; message: string }
interface ComposeReport {
  valid: boolean;
  errors?: string[];
  findings?: ComposeFinding[];
  services?: number;
  actions?: string[];
  executed?: boolean;
  note?: string;
}

// EVENTLOG_CHANNELS: kapalı enum — Security kanalı tasarımdan hariçtir
// (ADR-0009); gerçek kural sunucu + agent tarafında zorlanır.
const EVENTLOG_CHANNELS = ["Application", "System", "Setup"] as const;
type EventlogChannel = (typeof EVENTLOG_CHANNELS)[number];

function bytesLabel(n: number, locale: string): string {
  if (!Number.isFinite(n) || n <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  const i = Math.min(units.length - 1, Math.floor(Math.log(n) / Math.log(1024)));
  const v = n / Math.pow(1024, i);
  return `${new Intl.NumberFormat(locale === "tr" ? "tr-TR" : "en-US", { maximumFractionDigits: 1 }).format(v)} ${units[i]}`;
}

function percentLabel(n: number, locale: string): string {
  return `${new Intl.NumberFormat(locale === "tr" ? "tr-TR" : "en-US", { maximumFractionDigits: 1 }).format(n)}%`;
}

function uptimeLabel(seconds: number, t: ReturnType<typeof useI18n>["t"]): string {
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (d > 0) return t("nodedetail.uptime.days", { days: d, hours: h, minutes: m });
  if (h > 0) return t("nodedetail.uptime.hours", { hours: h, minutes: m });
  return t("nodedetail.uptime.minutes", { minutes: m });
}

/**
 * NodeDetail: tek node'un Faz 04 telemetri görünümü — agent'ın bildirdiği
 * sistem bilgileri, en yeni metrik anlık görüntüsü (kaynak + timestamp ile),
 * kabiliyet beyanları ve tipli servis işlemleri (start/stop/restart görevleri).
 * UI yalnızca kolaylıktır: yetki sunucu tarafında RBAC ile zorlanır.
 */
export function NodeDetail({ node, onBack }: { node: NodeRecord; onBack: () => void }) {
  const { t, locale } = useI18n();
  const [facts, setFacts] = useState<SystemFactsRecord | null>(null);
  const [metrics, setMetrics] = useState<MetricsSnapshot | null>(null);
  const [capabilities, setCapabilities] = useState<CapabilityRecord[]>([]);
  const [tasks, setTasks] = useState<TaskRecord[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [reloadKey, setReloadKey] = useState(0);
  const [unit, setUnit] = useState("");
  const [unitError, setUnitError] = useState<string | null>(null);
  const [pendingOp, setPendingOp] = useState<ServiceOp | null>(null);
  const [lastTaskId, setLastTaskId] = useState<string | null>(null);
  // Faz 05: event-log formu (yalnız windows node'larında görünür).
  const [evChannel, setEvChannel] = useState<EventlogChannel>("System");
  const [evLevel, setEvLevel] = useState(0);
  const [evCount, setEvCount] = useState(50);
  const [evError, setEvError] = useState<string | null>(null);
  const [evPending, setEvPending] = useState(false);
  // Faz 06: dağıtım işlemleri (yalnız linux node'larında görünür).
  const [pkgLimit, setPkgLimit] = useState(200);
  const [pkgError, setPkgError] = useState<string | null>(null);
  const [fwPending, setFwPending] = useState<string | null>(null);
  const [fwZone, setFwZone] = useState("");
  const [fwPort, setFwPort] = useState(8080);
  const [fwProto, setFwProto] = useState<"tcp" | "udp">("tcp");
  const [fwAction, setFwAction] = useState<FwAction>("allow");
  const [fwError, setFwError] = useState<string | null>(null);
  // Faz 07: docker işlemleri (yalnız docker capability'li node'larda).
  const [dockerLimit, setDockerLimit] = useState(100);
  const [dockerContainerRef, setDockerContainerRef] = useState("");
  const [dockerLogTail, setDockerLogTail] = useState(100);
  const [dockerStopTimeout, setDockerStopTimeout] = useState(10);
  const [dockerPending, setDockerPending] = useState<string | null>(null);
  const [dockerError, setDockerError] = useState<string | null>(null);
  const [removeKind, setRemoveKind] = useState<RemoveKind>("container");
  const [removeRef, setRemoveRef] = useState("");
  const [removeForce, setRemoveForce] = useState(false);
  // Compose: YAML metni + görev sonucu yoklaması (bulguları göstermek için).
  const [composeYaml, setComposeYaml] = useState("");
  const [composeTaskId, setComposeTaskId] = useState<string | null>(null);
  const [composeReport, setComposeReport] = useState<ComposeReport | null>(null);
  const [composeError, setComposeError] = useState<string | null>(null);

  // Faz 08: web hosting işlemleri (web.site.* capability'li node'larda).
  const [webLimit, setWebLimit] = useState(200);
  const [webPending, setWebPending] = useState<string | null>(null);
  const [webError, setWebError] = useState<string | null>(null);
  const [webTaskId, setWebTaskId] = useState<string | null>(null);
  const [webReport, setWebReport] = useState<WebReport | null>(null);
  const [webTargetSite, setWebTargetSite] = useState("");
  const [webName, setWebName] = useState("");
  const [webServerNames, setWebServerNames] = useState("");
  const [webBackend, setWebBackend] = useState<WebBackend>("static");
  const [webPHPVersion, setWebPHPVersion] = useState<string>("8.3");
  const [webPort, setWebPort] = useState(80);
  const [webMaxChildren, setWebMaxChildren] = useState(10);
  const [webProxyTarget, setWebProxyTarget] = useState("127.0.0.1:3000");

  const reload = useCallback(() => setReloadKey((k) => k + 1), []);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const [f, m, caps] = await Promise.all([
          apiFetch<SystemFactsRecord>(`/api/v1/nodes/${node.id}/facts/system`).catch(() => null),
          apiFetch<MetricsSnapshot>(`/api/v1/nodes/${node.id}/metrics/latest`).catch(() => null),
          apiFetch<{ capabilities: CapabilityRecord[] }>(`/api/v1/nodes/${node.id}/capabilities`)
            .then((r) => r.capabilities ?? [])
            .catch(() => []),
        ]);
        if (cancelled) return;
        setFacts(f);
        setMetrics(m);
        setCapabilities(caps);
        setError(null);
      } catch (err) {
        if (!cancelled) setError(isApiError(err) ? err.message : t("common.error.generic"));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [node.id, reloadKey, t]);

  // Servis görevleri: son 10 görev, bu node ve servis operasyonları.
  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const page = await apiFetch<{ tasks: TaskRecord[] }>(
          `/api/v1/tasks?node_id=${encodeURIComponent(node.id)}&limit=20`,
        );
        if (cancelled) return;
        setTasks(page.tasks.filter((x) => (ALL_SERVICE_OPS as readonly string[]).includes(x.operation) || (SURVEY_OPS as readonly string[]).includes(x.operation) || (ALL_DOCKER_OPS as readonly string[]).includes(x.operation)).slice(0, 10));
      } catch {
        // Görev listesi isteğe bağlıdır; hata görünümü bozmaz.
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [node.id, reloadKey]);

  // Faz 05: aile seçimi — node'un beyan ettiği kabiliyete göre linux
  // systemd ya da windows SCM ailesi kullanılır.
  const isWindows = capabilities.some((c) => c.capability_id.startsWith("windows.scm."));
  const serviceOps: readonly string[] = isWindows ? WINDOWS_SERVICE_OPS : LINUX_SERVICE_OPS;
  const familyRestartCap = capabilities.find((c) =>
    c.capability_id === (isWindows ? "windows.scm.service.restart" : "linux.systemd.service.restart"),
  );

  function validateTarget(): boolean {
    const value = unit.trim();
    const ok = isWindows ? validScmName(value) : UNIT_PATTERN.test(value);
    if (!ok) {
      setUnitError(t(isWindows ? "nodedetail.services.serviceInvalid" : "nodedetail.services.unitInvalid"));
      return false;
    }
    return true;
  }

  async function submitServiceOp(op: ServiceOp) {
    if (op !== serviceOps[0] && !validateTarget()) {
      return;
    }
    setUnitError(null);
    setPendingOp(op);
    try {
      const value = unit.trim();
      const payload =
        op === serviceOps[0]
          ? {}
          : isWindows
            ? { service: value }
            : { unit: value };
      const created = await apiFetch<TaskRecord>("/api/v1/tasks", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          operation: op,
          node_id: node.id,
          payload,
        }),
      });
      setLastTaskId(created.id);
      setPendingOp(null);
      reload();
    } catch (err) {
      setPendingOp(null);
      setError(isApiError(err) ? err.message : t("common.error.generic"));
    }
  }

  function onUnitSubmit(e: FormEvent) {
    e.preventDefault();
    void submitServiceOp(serviceOps[1] as ServiceOp);
  }

  // Faz 05: event-log okuma görevi (yalnız windows; kapalı enum + sınırlı
  // sayılar; gerçek kural sunucu + agent tarafında zorlanır).
  async function submitEventlogRead() {
    if (evCount < 1 || evCount > 200) {
      setEvError(t("nodedetail.eventlog.countInvalid"));
      return;
    }
    setEvError(null);
    setEvPending(true);
    try {
      const created = await apiFetch<TaskRecord>("/api/v1/tasks", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          operation: "windows.eventlog.read",
          node_id: node.id,
          payload: { channel: evChannel, level: evLevel, count: evCount },
        }),
      });
      setLastTaskId(created.id);
      setEvPending(false);
      reload();
    } catch (err) {
      setEvPending(false);
      setError(isApiError(err) ? err.message : t("common.error.generic"));
    }
  }

  // Faz 06: salt-okuma dağıtım işlemleri (paket listesi + firewall
  // sörveyi + kural önizlemesi — önizleme asla çalıştırmaz, ADR-0010).
  type SurveyErrorKey = "nodedetail.survey.limitInvalid" | "nodedetail.survey.zoneInvalid" | "nodedetail.survey.portInvalid";
  async function submitSurveyOp(op: SurveyOp, payload: Record<string, unknown>, errorKey: SurveyErrorKey | null, setError: (v: string | null) => void, setPending: (v: string | null) => void) {
    if (errorKey) {
      setError(t(errorKey));
      return;
    }
    setError(null);
    setPending(op);
    try {
      const created = await apiFetch<TaskRecord>("/api/v1/tasks", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ operation: op, node_id: node.id, payload }),
      });
      setLastTaskId(created.id);
      setPending(null);
      reload();
    } catch (err) {
      setPending(null);
      setError(isApiError(err) ? err.message : t("common.error.generic"));
    }
  }

  function submitPkgList() {
    const limit = pkgLimit || 200;
    if (limit < 1 || limit > 500) {
      void submitSurveyOp("linux.pkg.list", {}, "nodedetail.survey.limitInvalid", setPkgError, setFwPending);
      return;
    }
    void submitSurveyOp("linux.pkg.list", { limit }, null, setPkgError, setFwPending);
  }

  function submitFirewall(op: "linux.firewall.status" | "linux.firewall.rules.list" | "linux.firewall.rule.preview") {
    if (op === "linux.firewall.rule.preview") {
      const zone = fwZone.trim();
      if (!FW_ZONE_PATTERN.test(zone)) {
        void submitSurveyOp(op, {}, "nodedetail.survey.zoneInvalid", setFwError, setFwPending);
        return;
      }
      const port = fwPort;
      if (!Number.isInteger(port) || port < 1 || port > 65535) {
        void submitSurveyOp(op, {}, "nodedetail.survey.portInvalid", setFwError, setFwPending);
        return;
      }
      void submitSurveyOp(op, { zone, port, protocol: fwProto, action: fwAction }, null, setFwError, setFwPending);
      return;
    }
    const limit = pkgLimit || 200;
    if (limit < 1 || limit > 500) {
      void submitSurveyOp(op, {}, "nodedetail.survey.limitInvalid", setFwError, setFwPending);
      return;
    }
    void submitSurveyOp(op, { limit }, null, setFwError, setFwPending);
  }

  // Faz 07: docker capability tespiti.
  const isDocker = capabilities.some((c) => c.capability_id.startsWith("docker.container."));

  // Faz 08: web hosting capability tespiti.
  const isWeb = capabilities.some((c) => c.capability_id.startsWith("web.site."));

  // Faz 10: veritabanı + scoped dosya yöneticisi capability tespiti.
  const isDb = capabilities.some((c) => c.capability_id.startsWith("db."));
  const isFs = capabilities.some((c) => c.capability_id.startsWith("fs."));
  // Faz 11: e-posta ailesi (--mail-enable ile opt-in; ADR-0015).
  const isMail = capabilities.some((c) => c.capability_id.startsWith("mail."));

  async function submitTask(op: string, payload: Record<string, unknown>): Promise<TaskRecord | null> {
    try {
      const created = await apiFetch<TaskRecord>("/api/v1/tasks", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ operation: op, node_id: node.id, payload }),
      });
      setLastTaskId(created.id);
      reload();
      return created;
    } catch (err) {
      setError(isApiError(err) ? err.message : t("common.error.generic"));
      return null;
    }
  }

  // docker list ailesi: {limit}
  async function submitDockerList(op: DockerListOp) {
    const limit = dockerLimit || 100;
    if (limit < 1 || limit > 500) {
      setDockerError(t("nodedetail.docker.limitInvalid"));
      return;
    }
    setDockerError(null);
    setDockerPending(op);
    await submitTask(op, { limit });
    setDockerPending(null);
  }

  // docker ref ailesi: inspect/{ref}, logs/{ref,tail}, start/stop/restart
  async function submitDockerContainer(op: string) {
    const ref = dockerContainerRef.trim();
    if (!validDockerRef(ref)) {
      setDockerError(t("nodedetail.docker.refInvalid"));
      return;
    }
    setDockerError(null);
    setDockerPending(op);
    if (op === "docker.container.inspect") {
      await submitTask(op, { ref });
    } else if (op === "docker.container.logs") {
      const tail = dockerLogTail || 100;
      if (tail < 1 || tail > 500) {
        setDockerError(t("nodedetail.docker.limitInvalid"));
        setDockerPending(null);
        return;
      }
      await submitTask(op, { ref, tail });
    } else {
      const timeout = dockerStopTimeout || 10;
      if (timeout < 1 || timeout > 120) {
        setDockerError(t("nodedetail.docker.timeoutInvalid"));
        setDockerPending(null);
        return;
      }
      await submitTask(op, { ref, timeout });
    }
    setDockerPending(null);
  }

  // PREVIEW-ONLY: remove ailesi komutu DATA olarak üretir — çalıştırmaz (ADR-0011).
  async function submitDockerRemove(op: DockerRemoveOp) {
    const ref = removeRef.trim();
    if (!validDockerRef(ref, removeKind === "image")) {
      setDockerError(t("nodedetail.docker.refInvalid"));
      return;
    }
    setDockerError(null);
    setDockerPending(op);
    const payload: Record<string, unknown> = { ref };
    if (removeForce) payload.force = true;
    if (removeKind === "container" && removeForce) payload.v = true;
    await submitTask(op, payload);
    setDockerPending(null);
  }

  // Compose analizi: görev sonucu poll edilir ve bulgular UI'da gösterilir
  // (kriter: deploy öncesi riskler GÖSTERİLMELİ).
  async function submitCompose(op: ComposeOp) {
    if (!composeYaml.trim() || composeYaml.length > 65536) {
      setComposeError(t("nodedetail.compose.sizeInvalid"));
      return;
    }
    setComposeError(null);
    setComposeReport(null);
    setComposeTaskId(null);
    setDockerPending(op);
    const created = await submitTask(op, { compose: composeYaml });
    setDockerPending(null);
    if (!created) return;
    setComposeTaskId(created.id);
  }

  useEffect(() => {
    if (!composeTaskId) return;
    let cancelled = false;
    let attempts = 0;
    const tick = async () => {
      attempts += 1;
      try {
        const task = await apiFetch<TaskRecord>(`/api/v1/tasks/${encodeURIComponent(composeTaskId)}`);
        if (cancelled) return;
        if (task.status === "succeeded") {
          setComposeReport((task.result ?? null) as ComposeReport | null);
          return; // dur
        }
        if (task.status === "failed" || task.status === "cancelled" || task.status === "expired") {
          setComposeError(task.error_category ?? t("common.error.generic"));
          return; // dur
        }
        if (attempts < 12) {
          window.setTimeout(() => void tick(), 1000);
        }
      } catch {
        if (!cancelled && attempts < 12) window.setTimeout(() => void tick(), 1000);
      }
    };
    window.setTimeout(() => void tick(), 800);
    return () => {
      cancelled = true;
    };
  }, [composeTaskId, t]);

  // ---- Faz 08: web hosting işlemleri ----
  // Tüm web işlemleri görev olarak kuyruğa girer; sonuç poll edilip
  // webReport'ta gösterilir (compose kalıbı).
  async function submitWeb(op: string, payload: Record<string, unknown>) {
    setWebError(null);
    setWebReport(null);
    setWebPending(op);
    const created = await submitTask(op, payload);
    setWebPending(null);
    if (!created) return;
    setWebTaskId(created.id);
  }

  function submitWebList() {
    const limit = webLimit || 200;
    if (limit < 1 || limit > 500) {
      setWebError(t("nodedetail.web.limitInvalid"));
      return;
    }
    void submitWeb("web.site.list", { limit });
  }

  function submitWebPHP() {
    void submitWeb("web.php.list", {});
  }

  function submitWebStatus() {
    const site = webTargetSite.trim();
    if (!WEB_SITE_PATTERN.test(site)) {
      setWebError(t("nodedetail.web.nameInvalid"));
      return;
    }
    void submitWeb("web.site.status", { site });
  }

  function webFormPayload(): Record<string, unknown> | null {
    const name = webName.trim();
    if (!WEB_SITE_PATTERN.test(name)) {
      setWebError(t("nodedetail.web.nameInvalid"));
      return null;
    }
    const names = webServerNames
      .split(/[\s,]+/)
      .map((h) => h.trim().toLowerCase())
      .filter(Boolean);
    if (names.length < 1 || names.length > 8 || !names.every(validWebServerName)) {
      setWebError(t("nodedetail.web.serverNamesInvalid"));
      return null;
    }
    if (names.length !== new Set(names).size) {
      setWebError(t("nodedetail.web.serverNamesInvalid"));
      return null;
    }
    const port = webPort || 80;
    if (port < 1 || port > 65535) {
      setWebError(t("nodedetail.web.portInvalid"));
      return null;
    }
    const payload: Record<string, unknown> = { name, server_names: names, backend: webBackend, listen_port: port };
    if (webBackend === "php") {
      if (!WEB_PHP_VERSIONS.includes(webPHPVersion as (typeof WEB_PHP_VERSIONS)[number])) {
        setWebError(t("nodedetail.web.phpInvalid"));
        return null;
      }
      payload.php_version = webPHPVersion;
      const mc = webMaxChildren || 10;
      if (mc < 1 || mc > 200) {
        setWebError(t("nodedetail.web.maxChildrenInvalid"));
        return null;
      }
      payload.max_children = mc;
    }
    if (webBackend === "proxy") {
      if (!validWebProxyTarget(webProxyTarget.trim())) {
        setWebError(t("nodedetail.web.proxyInvalid"));
        return null;
      }
      payload.proxy_target = webProxyTarget.trim();
    }
    return payload;
  }

  function submitWebForm(op: "web.site.create" | "web.site.update") {
    const payload = webFormPayload();
    if (payload === null) return;
    void submitWeb(op, payload);
  }

  function submitWebSiteAction(op: "web.site.disable" | "web.site.delete") {
    const site = webTargetSite.trim();
    if (!WEB_SITE_PATTERN.test(site)) {
      setWebError(t("nodedetail.web.nameInvalid"));
      return;
    }
    void submitWeb(op, { site });
  }

  useEffect(() => {
    if (!webTaskId) return;
    let cancelled = false;
    let attempts = 0;
    const tick = async () => {
      attempts += 1;
      try {
        const task = await apiFetch<TaskRecord>(`/api/v1/tasks/${encodeURIComponent(webTaskId)}`);
        if (cancelled) return;
        if (task.status === "succeeded") {
          setWebReport((task.result ?? null) as WebReport | null);
          return; // dur
        }
        if (task.status === "failed" || task.status === "cancelled" || task.status === "expired") {
          setWebError(task.error_category ?? t("common.error.generic"));
          return; // dur
        }
        if (attempts < 12) {
          window.setTimeout(() => void tick(), 1000);
        }
      } catch {
        if (!cancelled && attempts < 12) window.setTimeout(() => void tick(), 1000);
      }
    };
    window.setTimeout(() => void tick(), 800);
    return () => {
      cancelled = true;
    };
  }, [webTaskId, t]);

  const taskBadge: Record<TaskRecord["status"], BadgeVariant> = {
    queued: "info",
    dispatched: "info",
    accepted: "info",
    running: "info",
    succeeded: "ok",
    failed: "error",
    cancelled: "neutral",
    expired: "warn",
    unknown: "neutral",
    partial: "warn",
  };

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <div>
          <button
            type="button"
            onClick={onBack}
            className="text-xs text-accent hover:underline focus-visible:outline-2 focus-visible:outline-accent"
          >
            ← {t("nodedetail.back")}
          </button>
          <h2 className="text-lg font-semibold text-ink">{node.display_name}</h2>
          {node.hostname && <p className="text-[11px] text-ink-soft">{node.hostname}</p>}
        </div>
        <button
          type="button"
          onClick={reload}
          className="rounded-lg border border-border px-3 py-1.5 text-xs text-ink hover:bg-neutral-soft focus-visible:outline-2 focus-visible:outline-accent"
        >
          {t("nodedetail.refresh")}
        </button>
      </div>

      {error && (
        <p role="alert" className="rounded-lg bg-err-soft px-3 py-2 text-xs text-err">
          {error}
        </p>
      )}

      {/* Sistem bilgileri */}
      <section aria-label={t("nodedetail.facts.title")} className="rounded-xl border border-border bg-panel p-4 shadow-sm">
        <h3 className="text-sm font-semibold text-ink">{t("nodedetail.facts.title")}</h3>
        {!facts ? (
          <p className="mt-2 text-xs text-ink-soft">{t("nodedetail.facts.none")}</p>
        ) : (
          <dl className="mt-2 grid grid-cols-2 gap-x-4 gap-y-1.5 text-xs md:grid-cols-4">
            <Fact label={t("nodedetail.facts.distro")} value={facts.os_pretty_name || facts.os_id || "—"} />
            <Fact label={t("nodedetail.facts.version")} value={facts.os_version_id || "—"} />
            <Fact label={t("nodedetail.facts.kernel")} value={facts.kernel_release || "—"} />
            <Fact label={t("nodedetail.facts.arch")} value={facts.arch || "—"} />
            <Fact
              label={t("nodedetail.facts.systemd")}
              value={facts.systemd_available ? facts.systemd_version || "✓" : t("nodedetail.facts.systemdNo")}
            />
            <Fact label={t("nodedetail.facts.pkgManager")} value={facts.package_manager || "—"} />
            <Fact label={t("nodedetail.facts.distroFamily")} value={facts.distro_family || "—"} />
            <Fact
              label={t("nodedetail.facts.support")}
              value={supportStateLabel(facts.support_state, t)}
              hint={facts.support_reason ? facts.support_reason : undefined}
            />
            <Fact label={t("nodedetail.facts.source")} value={facts.source || "—"} />
            <Fact label={t("nodedetail.facts.collectedAt")} value={new Date(facts.collected_at).toLocaleString(locale === "tr" ? "tr-TR" : "en-US")} />
          </dl>
        )}
      </section>

      {/* Metrikler */}
      <section aria-label={t("nodedetail.metrics.title")} className="rounded-xl border border-border bg-panel p-4 shadow-sm">
        <h3 className="text-sm font-semibold text-ink">{t("nodedetail.metrics.title")}</h3>
        {!metrics ? (
          <p className="mt-2 text-xs text-ink-soft">{t("nodedetail.metrics.none")}</p>
        ) : (
          <div className="mt-2 flex flex-col gap-3 text-xs">
            <div className="grid grid-cols-2 gap-x-4 gap-y-1.5 md:grid-cols-4">
              <Metric
                label={t("nodedetail.metrics.cpu")}
                value={percentLabel(metrics.cpu.percent, locale)}
                hint={t("nodedetail.metrics.cores", { count: metrics.cpu.logical_cores })}
              />
              <Metric
                label={t("nodedetail.metrics.memory")}
                value={percentLabel(metrics.memory.used_percent, locale)}
                hint={`${bytesLabel(metrics.memory.used_bytes, locale)} / ${bytesLabel(metrics.memory.total_bytes, locale)}`}
              />
              <Metric label={t("nodedetail.metrics.load")} value={`${metrics.cpu.load_1m} · ${metrics.cpu.load_5m} · ${metrics.cpu.load_15m}`} />
              <Metric label={t("nodedetail.metrics.uptime")} value={uptimeLabel(metrics.uptime_seconds, t)} />
            </div>

            {metrics.disks.length > 0 && (
              <div>
                <p className="mb-1 font-medium text-ink">{t("nodedetail.metrics.disks")}</p>
                <ul className="flex flex-col gap-1">
                  {metrics.disks.map((d) => (
                    <li key={`${d.device}-${d.mount_point}`} className="flex items-center gap-2">
                      <span className="w-40 shrink-0 truncate text-ink-soft" title={d.mount_point}>
                        {d.device} → {d.mount_point}
                      </span>
                      <div
                        role="progressbar"
                        aria-label={`${d.device} ${d.mount_point}`}
                        aria-valuenow={Math.round(d.total_bytes > 0 ? ((d.total_bytes - d.available_bytes) / d.total_bytes) * 100 : 0)}
                        aria-valuemin={0}
                        aria-valuemax={100}
                        className="h-1.5 w-40 overflow-hidden rounded-full bg-neutral-soft"
                      >
                        <div
                          className="h-full bg-accent"
                          style={{
                            width: `${d.total_bytes > 0 ? Math.min(100, ((d.total_bytes - d.available_bytes) / d.total_bytes) * 100) : 0}%`,
                          }}
                        />
                      </div>
                      <span className="text-ink-soft">
                        {bytesLabel(d.total_bytes - d.available_bytes, locale)} / {bytesLabel(d.total_bytes, locale)} ({d.fs_type})
                      </span>
                    </li>
                  ))}
                </ul>
              </div>
            )}

            {metrics.networks.length > 0 && (
              <div>
                <p className="mb-1 font-medium text-ink">{t("nodedetail.metrics.network")}</p>
                <ul className="flex flex-wrap gap-3">
                  {metrics.networks.map((n) => (
                    <li key={n.interface} className="text-ink-soft">
                      {n.interface}: ↓{bytesLabel(n.rx_bytes, locale)} ↑{bytesLabel(n.tx_bytes, locale)}
                    </li>
                  ))}
                </ul>
              </div>
            )}

            <p className="text-[11px] text-ink-soft">
              {t("nodedetail.metrics.provenance", {
                source: metrics.source,
                time: new Date(metrics.collected_at).toLocaleString(locale === "tr" ? "tr-TR" : "en-US"),
              })}
            </p>
          </div>
        )}
      </section>

      {/* Kabiliyetler */}
      <section aria-label={t("nodedetail.capabilities.title")} className="rounded-xl border border-border bg-panel p-4 shadow-sm">
        <h3 className="text-sm font-semibold text-ink">{t("nodedetail.capabilities.title")}</h3>
        {capabilities.length === 0 ? (
          <p className="mt-2 text-xs text-ink-soft">{t("nodedetail.capabilities.none")}</p>
        ) : (
          <ul className="mt-2 flex flex-wrap gap-2">
            {capabilities.map((c) => (
              <li
                key={c.capability_id}
                title={c.available ? c.capability_id : c.reason_unavailable || c.capability_id}
                className={`rounded-full px-2.5 py-1 text-[11px] ${
                  c.available ? "bg-ok-soft text-ok" : "bg-warn-soft text-warn"
                }`}
              >
                {c.available ? "✓" : "✕"} {c.capability_id}
                {!c.available && c.reason_unavailable ? ` — ${c.reason_unavailable}` : ""}
              </li>
            ))}
          </ul>
        )}
      </section>

      {/* Servis işlemleri (tipli; shell yok; aile: linux systemd / windows SCM) */}
      <section aria-label={t("nodedetail.services.title")} className="rounded-xl border border-border bg-panel p-4 shadow-sm">
        <h3 className="text-sm font-semibold text-ink">
          {t(isWindows ? "nodedetail.services.titleWindows" : "nodedetail.services.title")}
        </h3>
        <p className="mt-1 text-[11px] text-ink-soft">{t("nodedetail.services.note")}</p>
        {familyRestartCap && !familyRestartCap.available && (
          <p role="status" className="mt-2 rounded-lg bg-warn-soft px-3 py-2 text-xs text-warn">
            {t("nodedetail.services.unavailable", { reason: familyRestartCap.reason_unavailable || "" })}
          </p>
        )}
        <form onSubmit={onUnitSubmit} className="mt-3 flex flex-col gap-2">
          <label className="flex flex-col gap-1">
            <span className="text-xs font-medium text-ink">
              {t(isWindows ? "nodedetail.services.service" : "nodedetail.services.unit")}
            </span>
            <input
              value={unit}
              onChange={(e) => {
                setUnit(e.target.value);
                setUnitError(null);
              }}
              placeholder={isWindows ? "Dhcp" : "nginx.service"}
              aria-invalid={unitError !== null}
              className="rounded-lg border border-border bg-surface px-3 py-1.5 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
            />
          </label>
          {unitError && (
            <p role="alert" className="text-xs text-err">
              {unitError}
            </p>
          )}
          <div className="flex flex-wrap gap-2">
            <button
              type="button"
              onClick={() => void submitServiceOp(serviceOps[0] as ServiceOp)}
              disabled={pendingOp !== null}
              className="rounded-lg border border-border px-3 py-1.5 text-xs text-ink hover:bg-neutral-soft disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent"
            >
              {t("nodedetail.services.list")}
            </button>
            {(["status", "start", "stop", "restart"] as const).map((verb) => {
              const op = `${isWindows ? "windows.scm.service" : "linux.systemd.service"}.${verb}` as ServiceOp;
              const destructive = verb === "stop" || verb === "restart";
              return (
                <button
                  key={verb}
                  type="button"
                  onClick={() => void submitServiceOp(op)}
                  disabled={pendingOp !== null}
                  className={`rounded-lg px-3 py-1.5 text-xs font-medium disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent ${
                    destructive
                      ? "border border-warn/40 bg-warn-soft text-warn hover:bg-warn/20"
                      : "border border-border bg-surface text-ink hover:bg-neutral-soft"
                  }`}
                >
                  {t(`nodedetail.services.${verb}` as const)}
                </button>
              );
            })}
          </div>
        </form>

        {lastTaskId && (
          <p role="status" className="mt-2 text-[11px] text-ok">
            {t("nodedetail.services.queued", { task: lastTaskId })}
          </p>
        )}

        {tasks.length > 0 && (
          <table className="mt-3 w-full text-left text-xs">
            <caption className="sr-only">{t("nodedetail.services.tableCaption")}</caption>
            <thead>
              <tr className="border-b border-border text-[11px] uppercase tracking-wide text-ink-soft">
                <th scope="col" className="px-2 py-1.5 font-medium">{t("nodedetail.services.colOp")}</th>
                <th scope="col" className="px-2 py-1.5 font-medium">{t("nodedetail.services.colStatus")}</th>
                <th scope="col" className="px-2 py-1.5 font-medium">{t("nodedetail.services.colTime")}</th>
              </tr>
            </thead>
            <tbody>
              {tasks.map((task) => (
                <tr key={task.id} className="border-b border-border/60 last:border-0">
                  <td className="px-2 py-1.5 text-ink">{task.operation}</td>
                  <td className="px-2 py-1.5">
                    <StatusBadge variant={taskBadge[task.status]}>{t(`tasks.status.${task.status}` as const)}</StatusBadge>
                  </td>
                  <td className="px-2 py-1.5 text-ink-soft">{new Date(task.updated_at).toLocaleString(locale === "tr" ? "tr-TR" : "en-US")}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      {/* Faz 06: salt-okuma dağıtım işlemleri (yalnız linux node'ları) */}
      {!isWindows && (
        <section aria-label={t("nodedetail.survey.title")} className="rounded-xl border border-border bg-panel p-4 shadow-sm">
          <h3 className="text-sm font-semibold text-ink">{t("nodedetail.survey.title")}</h3>
          <p className="mt-1 text-[11px] text-ink-soft">{t("nodedetail.survey.note")}</p>

          {/* Paket envanteri */}
          <form
            onSubmit={(e) => {
              e.preventDefault();
              submitPkgList();
            }}
            className="mt-3 flex flex-wrap items-end gap-2"
          >
            <label className="flex flex-col gap-1">
              <span className="text-xs font-medium text-ink">{t("nodedetail.survey.pkgLimit")}</span>
              <input
                type="number"
                value={pkgLimit}
                onChange={(e) => {
                  setPkgLimit(Number(e.target.value));
                  setPkgError(null);
                }}
                className="w-24 rounded-lg border border-border bg-surface px-3 py-1.5 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
              />
            </label>
            <button
              type="submit"
              disabled={fwPending !== null}
              className="rounded-lg border border-border bg-surface px-3 py-1.5 text-xs font-medium text-ink hover:bg-neutral-soft disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent"
            >
              {t("nodedetail.survey.pkgList")}
            </button>
          </form>
          {pkgError && (
            <p role="alert" className="mt-2 text-xs text-err">
              {pkgError}
            </p>
          )}

          {/* Firewall sörveyi + kural önizlemesi */}
          <div className="mt-4 flex flex-wrap gap-2">
            <button
              type="button"
              onClick={() => submitFirewall("linux.firewall.status")}
              disabled={fwPending !== null}
              className="rounded-lg border border-border bg-surface px-3 py-1.5 text-xs font-medium text-ink hover:bg-neutral-soft disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent"
            >
              {t("nodedetail.survey.fwStatus")}
            </button>
            <button
              type="button"
              onClick={() => submitFirewall("linux.firewall.rules.list")}
              disabled={fwPending !== null}
              className="rounded-lg border border-border bg-surface px-3 py-1.5 text-xs font-medium text-ink hover:bg-neutral-soft disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent"
            >
              {t("nodedetail.survey.fwRules")}
            </button>
          </div>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              submitFirewall("linux.firewall.rule.preview");
            }}
            className="mt-3 flex flex-wrap items-end gap-2"
          >
            <label className="flex flex-col gap-1">
              <span className="text-xs font-medium text-ink">{t("nodedetail.survey.fwZone")}</span>
              <input
                value={fwZone}
                onChange={(e) => {
                  setFwZone(e.target.value);
                  setFwError(null);
                }}
                placeholder="public"
                aria-invalid={fwError !== null}
                className="w-32 rounded-lg border border-border bg-surface px-3 py-1.5 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
              />
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-xs font-medium text-ink">{t("nodedetail.survey.fwPort")}</span>
              <input
                type="number"
                value={fwPort}
                onChange={(e) => {
                  setFwPort(Number(e.target.value));
                  setFwError(null);
                }}
                className="w-24 rounded-lg border border-border bg-surface px-3 py-1.5 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
              />
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-xs font-medium text-ink">{t("nodedetail.survey.fwProto")}</span>
              <select
                value={fwProto}
                onChange={(e) => setFwProto(e.target.value as "tcp" | "udp")}
                className="rounded-lg border border-border bg-surface px-3 py-1.5 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
              >
                <option value="tcp">tcp</option>
                <option value="udp">udp</option>
              </select>
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-xs font-medium text-ink">{t("nodedetail.survey.fwAction")}</span>
              <select
                value={fwAction}
                onChange={(e) => setFwAction(e.target.value as FwAction)}
                className="rounded-lg border border-border bg-surface px-3 py-1.5 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
              >
                {FW_ACTIONS.map((a) => (
                  <option key={a} value={a}>
                    {a}
                  </option>
                ))}
              </select>
            </label>
            <button
              type="submit"
              disabled={fwPending !== null}
              className="rounded-lg border border-warn/40 bg-warn-soft px-3 py-1.5 text-xs font-medium text-warn hover:bg-warn/20 disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent"
            >
              {t("nodedetail.survey.fwPreview")}
            </button>
          </form>
          <p className="mt-2 text-[11px] text-ink-soft">{t("nodedetail.survey.previewNote")}</p>
          {fwError && (
            <p role="alert" className="mt-2 text-xs text-err">
              {fwError}
            </p>
          )}
        </section>
      )}

      {/* Faz 07: Docker işlemleri (yalnız docker capability'li node'lar).
          Daemon erişimi yalnız broker'dadır; remove/compose.deploy
          PREVIEW-ONLY'dir (ADR-0011) — UI da açıkça etiketler. */}
      {isDocker && (
        <section aria-label={t("nodedetail.docker.title")} className="rounded-xl border border-border bg-panel p-4 shadow-sm">
          <h3 className="text-sm font-semibold text-ink">{t("nodedetail.docker.title")}</h3>
          <p className="mt-1 text-[11px] text-ink-soft">{t("nodedetail.docker.note")}</p>

          {/* list ailesi */}
          <div className="mt-3 flex flex-wrap items-end gap-2">
            <label className="flex flex-col gap-1">
              <span className="text-xs font-medium text-ink">{t("nodedetail.docker.limit")}</span>
              <input
                type="number"
                min={1}
                max={500}
                value={dockerLimit}
                onChange={(e) => {
                  setDockerLimit(Number(e.target.value));
                  setDockerError(null);
                }}
                className="w-24 rounded-lg border border-border bg-surface px-3 py-1.5 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
              />
            </label>
            {DOCKER_LIST_OPS.map((op) => (
              <button
                key={op}
                type="button"
                onClick={() => void submitDockerList(op)}
                disabled={dockerPending !== null}
                className="rounded-lg border border-border bg-surface px-3 py-1.5 text-xs font-medium text-ink hover:bg-neutral-soft disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent"
              >
                {t(`nodedetail.docker.${op.split(".")[1]}.list` as never)}
              </button>
            ))}
          </div>

          {/* container yaşam döngüsü + loglar */}
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void submitDockerContainer("docker.container.inspect");
            }}
            className="mt-3 flex flex-wrap items-end gap-2"
          >
            <label className="flex flex-col gap-1">
              <span className="text-xs font-medium text-ink">{t("nodedetail.docker.containerRef")}</span>
              <input
                value={dockerContainerRef}
                onChange={(e) => {
                  setDockerContainerRef(e.target.value);
                  setDockerError(null);
                }}
                placeholder="web-01"
                className="w-44 rounded-lg border border-border bg-surface px-3 py-1.5 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
              />
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-xs font-medium text-ink">{t("nodedetail.docker.tail")}</span>
              <input
                type="number"
                min={1}
                max={500}
                value={dockerLogTail}
                onChange={(e) => setDockerLogTail(Number(e.target.value))}
                className="w-24 rounded-lg border border-border bg-surface px-3 py-1.5 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
              />
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-xs font-medium text-ink">{t("nodedetail.docker.timeout")}</span>
              <input
                type="number"
                min={1}
                max={120}
                value={dockerStopTimeout}
                onChange={(e) => setDockerStopTimeout(Number(e.target.value))}
                className="w-24 rounded-lg border border-border bg-surface px-3 py-1.5 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
              />
            </label>
            <button
              type="submit"
              disabled={dockerPending !== null}
              className="rounded-lg border border-border bg-surface px-3 py-1.5 text-xs font-medium text-ink hover:bg-neutral-soft disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent"
            >
              {t("nodedetail.docker.inspect")}
            </button>
            <button
              type="button"
              onClick={() => void submitDockerContainer("docker.container.logs")}
              disabled={dockerPending !== null}
              className="rounded-lg border border-border bg-surface px-3 py-1.5 text-xs font-medium text-ink hover:bg-neutral-soft disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent"
            >
              {t("nodedetail.docker.logs")}
            </button>
            <button
              type="button"
              onClick={() => void submitDockerContainer("docker.container.start")}
              disabled={dockerPending !== null}
              className="rounded-lg border border-border bg-surface px-3 py-1.5 text-xs font-medium text-ink hover:bg-neutral-soft disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent"
            >
              {t("nodedetail.docker.start")}
            </button>
            <button
              type="button"
              onClick={() => void submitDockerContainer("docker.container.stop")}
              disabled={dockerPending !== null}
              className="rounded-lg border border-border bg-surface px-3 py-1.5 text-xs font-medium text-ink hover:bg-neutral-soft disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent"
            >
              {t("nodedetail.docker.stop")}
            </button>
            <button
              type="button"
              onClick={() => void submitDockerContainer("docker.container.restart")}
              disabled={dockerPending !== null}
              className="rounded-lg border border-border bg-surface px-3 py-1.5 text-xs font-medium text-ink hover:bg-neutral-soft disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent"
            >
              {t("nodedetail.docker.restart")}
            </button>
          </form>

          {/* remove ailesi — YALNIZ ÖNİZLEME (ADR-0011) */}
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void submitDockerRemove(`${"docker."}${removeKind}.remove` as DockerRemoveOp);
            }}
            className="mt-4 flex flex-wrap items-end gap-2 border-t border-border pt-3"
          >
            <label className="flex flex-col gap-1">
              <span className="text-xs font-medium text-ink">{t("nodedetail.docker.removeKind")}</span>
              <select
                value={removeKind}
                onChange={(e) => setRemoveKind(e.target.value as RemoveKind)}
                className="rounded-lg border border-border bg-surface px-3 py-1.5 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
              >
                {REMOVE_KINDS.map((k) => (
                  <option key={k} value={k}>
                    {t(`nodedetail.docker.kind.${k}` as never)}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-xs font-medium text-ink">{t("nodedetail.docker.removeRef")}</span>
              <input
                value={removeRef}
                onChange={(e) => {
                  setRemoveRef(e.target.value);
                  setDockerError(null);
                }}
                placeholder={removeKind === "image" ? "nginx:1.25" : "app-data"}
                className="w-44 rounded-lg border border-border bg-surface px-3 py-1.5 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
              />
            </label>
            <label className="flex items-center gap-2 pb-1.5">
              <input
                type="checkbox"
                checked={removeForce}
                onChange={(e) => setRemoveForce(e.target.checked)}
                className="h-4 w-4 rounded border-border"
              />
              <span className="text-xs font-medium text-ink">{t("nodedetail.docker.force")}</span>
            </label>
            <button
              type="submit"
              disabled={dockerPending !== null}
              className="rounded-lg border border-warn/40 bg-warn-soft px-3 py-1.5 text-xs font-medium text-warn hover:bg-warn/20 disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent"
            >
              {t("nodedetail.docker.removePreview")}
            </button>
            <span className="pb-1.5 text-[11px] text-ink-soft">{t("nodedetail.docker.removeNote")}</span>
          </form>

          {/* Compose analizi — doğrulama + plan (yürütmez, ADR-0011) */}
          <div className="mt-4 border-t border-border pt-3">
            <label className="flex flex-col gap-1">
              <span className="text-xs font-medium text-ink">{t("nodedetail.compose.yaml")}</span>
              <textarea
                value={composeYaml}
                onChange={(e) => {
                  setComposeYaml(e.target.value);
                  setComposeError(null);
                }}
                rows={6}
                spellCheck={false}
                className="w-full rounded-lg border border-border bg-surface px-3 py-2 font-mono text-xs text-ink focus-visible:outline-2 focus-visible:outline-accent"
              />
            </label>
            <div className="mt-2 flex flex-wrap gap-2">
              <button
                type="button"
                onClick={() => void submitCompose("docker.compose.validate")}
                disabled={dockerPending !== null}
                className="rounded-lg border border-border bg-surface px-3 py-1.5 text-xs font-medium text-ink hover:bg-neutral-soft disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent"
              >
                {t("nodedetail.compose.validate")}
              </button>
              <button
                type="button"
                onClick={() => void submitCompose("docker.compose.deploy")}
                disabled={dockerPending !== null}
                className="rounded-lg border border-warn/40 bg-warn-soft px-3 py-1.5 text-xs font-medium text-warn hover:bg-warn/20 disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent"
              >
                {t("nodedetail.compose.plan")}
              </button>
              <span className="self-center text-[11px] text-ink-soft">{t("nodedetail.compose.planNote")}</span>
            </div>
            {composeError && (
              <p role="alert" className="mt-2 text-xs text-err">
                {composeError}
              </p>
            )}
            {composeReport && (
              <div className="mt-3 rounded-lg border border-border bg-surface p-3" data-testid="compose-report">
                <p className="text-xs font-medium text-ink">
                  {composeReport.valid
                    ? t("nodedetail.compose.valid", { services: composeReport.services ?? 0 })
                    : t("nodedetail.compose.invalid")}
                </p>
                {(composeReport.errors ?? []).map((e, i) => (
                  <p key={`e${i}`} className="mt-1 text-xs text-err">
                    • {e}
                  </p>
                ))}
                {(composeReport.findings ?? []).map((f, i) => (
                  <p key={`f${i}`} className={`mt-1 text-xs ${f.severity === "high" ? "text-err" : f.severity === "medium" ? "text-warn" : "text-ink-soft"}`}>
                    • [{f.severity}] {f.rule}
                    {f.service ? ` (${f.service})` : ""} — {f.message}
                  </p>
                ))}
                {(composeReport.actions ?? []).map((a, i) => (
                  <p key={`a${i}`} className="mt-1 font-mono text-[11px] text-ink-soft">
                    → {a}
                  </p>
                ))}
                {composeReport.note && <p className="mt-2 text-[11px] text-ink-soft">{composeReport.note}</p>}
              </div>
            )}
          </div>

          {dockerError && (
            <p role="alert" className="mt-2 text-xs text-err">
              {dockerError}
            </p>
          )}
        </section>
      )}

      {/* Faz 08: Web hosting işlemleri (yalnız web.site.* capability'li
          node'lar). Gerçek yürütme yalnız broker'da --web-enable arkasındadır
          (ADR-0012); delete docroot'u KORUR — UI açıkça etiketler. */}
      {isWeb && (
        <section aria-label={t("nodedetail.web.title")} className="rounded-xl border border-border bg-panel p-4 shadow-sm">
          <h3 className="text-sm font-semibold text-ink">{t("nodedetail.web.title")}</h3>
          <p className="mt-1 text-[11px] text-ink-soft">{t("nodedetail.web.note")}</p>

          {/* salt-okuma: site listesi + php matrisi */}
          <div className="mt-3 flex flex-wrap items-end gap-2">
            <label className="flex flex-col gap-1">
              <span className="text-xs font-medium text-ink">{t("nodedetail.web.limit")}</span>
              <input
                type="number"
                min={1}
                max={500}
                value={webLimit}
                onChange={(e) => {
                  setWebLimit(Number(e.target.value));
                  setWebError(null);
                }}
                className="w-24 rounded-lg border border-border bg-surface px-3 py-1.5 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
              />
            </label>
            <button
              type="button"
              onClick={submitWebList}
              disabled={webPending !== null}
              className="rounded-lg border border-border bg-surface px-3 py-1.5 text-xs font-medium text-ink hover:bg-neutral-soft disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent"
            >
              {t("nodedetail.web.siteList")}
            </button>
            <button
              type="button"
              onClick={submitWebPHP}
              disabled={webPending !== null}
              className="rounded-lg border border-border bg-surface px-3 py-1.5 text-xs font-medium text-ink hover:bg-neutral-soft disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent"
            >
              {t("nodedetail.web.phpList")}
            </button>
          </div>

          {/* create/update formu */}
          <form
            onSubmit={(e) => {
              e.preventDefault();
              submitWebForm("web.site.create");
            }}
            className="mt-3 flex flex-wrap items-end gap-2"
          >
            <label className="flex flex-col gap-1">
              <span className="text-xs font-medium text-ink">{t("nodedetail.web.name")}</span>
              <input
                value={webName}
                onChange={(e) => {
                  setWebName(e.target.value);
                  setWebError(null);
                }}
                placeholder="blog"
                className="w-36 rounded-lg border border-border bg-surface px-3 py-1.5 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
              />
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-xs font-medium text-ink">{t("nodedetail.web.serverNames")}</span>
              <input
                value={webServerNames}
                onChange={(e) => {
                  setWebServerNames(e.target.value);
                  setWebError(null);
                }}
                placeholder="blog.example.com, www.blog.example.com"
                className="w-72 rounded-lg border border-border bg-surface px-3 py-1.5 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
              />
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-xs font-medium text-ink">{t("nodedetail.web.backend")}</span>
              <select
                value={webBackend}
                onChange={(e) => {
                  setWebBackend(e.target.value as WebBackend);
                  setWebError(null);
                }}
                className="rounded-lg border border-border bg-surface px-3 py-1.5 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
              >
                {WEB_BACKENDS.map((b) => (
                  <option key={b} value={b}>
                    {t(`nodedetail.web.backend.${b}` as never)}
                  </option>
                ))}
              </select>
            </label>
            {webBackend === "php" && (
              <>
                <label className="flex flex-col gap-1">
                  <span className="text-xs font-medium text-ink">{t("nodedetail.web.phpVersion")}</span>
                  <select
                    value={webPHPVersion}
                    onChange={(e) => setWebPHPVersion(e.target.value)}
                    className="rounded-lg border border-border bg-surface px-3 py-1.5 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
                  >
                    {WEB_PHP_VERSIONS.map((v) => (
                      <option key={v} value={v}>
                        PHP {v}
                      </option>
                    ))}
                  </select>
                </label>
                <label className="flex flex-col gap-1">
                  <span className="text-xs font-medium text-ink">{t("nodedetail.web.maxChildren")}</span>
                  <input
                    type="number"
                    min={1}
                    max={200}
                    value={webMaxChildren}
                    onChange={(e) => setWebMaxChildren(Number(e.target.value))}
                    className="w-24 rounded-lg border border-border bg-surface px-3 py-1.5 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
                  />
                </label>
              </>
            )}
            {webBackend === "proxy" && (
              <label className="flex flex-col gap-1">
                <span className="text-xs font-medium text-ink">{t("nodedetail.web.proxyTarget")}</span>
                <input
                  value={webProxyTarget}
                  onChange={(e) => {
                    setWebProxyTarget(e.target.value);
                    setWebError(null);
                  }}
                  placeholder="127.0.0.1:3000"
                  className="w-48 rounded-lg border border-border bg-surface px-3 py-1.5 font-mono text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
                />
              </label>
            )}
            <label className="flex flex-col gap-1">
              <span className="text-xs font-medium text-ink">{t("nodedetail.web.listenPort")}</span>
              <input
                type="number"
                min={1}
                max={65535}
                value={webPort}
                onChange={(e) => {
                  setWebPort(Number(e.target.value));
                  setWebError(null);
                }}
                className="w-24 rounded-lg border border-border bg-surface px-3 py-1.5 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
              />
            </label>
            <button
              type="submit"
              disabled={webPending !== null}
              className="rounded-lg border border-border bg-surface px-3 py-1.5 text-xs font-medium text-ink hover:bg-neutral-soft disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent"
            >
              {t("nodedetail.web.create")}
            </button>
            <button
              type="button"
              onClick={() => submitWebForm("web.site.update")}
              disabled={webPending !== null}
              className="rounded-lg border border-border bg-surface px-3 py-1.5 text-xs font-medium text-ink hover:bg-neutral-soft disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent"
            >
              {t("nodedetail.web.update")}
            </button>
          </form>

          {/* status / disable / delete hedefi */}
          <div className="mt-3 flex flex-wrap items-end gap-2">
            <label className="flex flex-col gap-1">
              <span className="text-xs font-medium text-ink">{t("nodedetail.web.targetSite")}</span>
              <input
                value={webTargetSite}
                onChange={(e) => {
                  setWebTargetSite(e.target.value);
                  setWebError(null);
                }}
                placeholder="blog"
                className="w-36 rounded-lg border border-border bg-surface px-3 py-1.5 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
              />
            </label>
            <button
              type="button"
              onClick={submitWebStatus}
              disabled={webPending !== null}
              className="rounded-lg border border-border bg-surface px-3 py-1.5 text-xs font-medium text-ink hover:bg-neutral-soft disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent"
            >
              {t("nodedetail.web.status")}
            </button>
            <button
              type="button"
              onClick={() => submitWebSiteAction("web.site.disable")}
              disabled={webPending !== null}
              className="rounded-lg border border-border bg-surface px-3 py-1.5 text-xs font-medium text-ink hover:bg-neutral-soft disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent"
            >
              {t("nodedetail.web.disable")}
            </button>
            <button
              type="button"
              onClick={() => submitWebSiteAction("web.site.delete")}
              disabled={webPending !== null}
              className="rounded-lg border border-warn/40 bg-warn-soft px-3 py-1.5 text-xs font-medium text-warn hover:bg-warn/20 disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent"
            >
              {t("nodedetail.web.delete")}
            </button>
            <span className="pb-1.5 text-[11px] text-ink-soft">{t("nodedetail.web.deleteNote")}</span>
          </div>

          {webError && (
            <p role="alert" className="mt-2 text-xs text-err">
              {webError}
            </p>
          )}

          {/* görev sonucu poll edilip gösterilir */}
          {webReport && (
            <div className="mt-3 rounded-lg border border-border bg-surface p-3" data-testid="web-report">
              {webReport.sites && (
                <div>
                  <p className="text-xs font-medium text-ink">{t("nodedetail.web.sitesTitle")}</p>
                  {webReport.sites.length === 0 ? (
                    <p className="mt-1 text-xs text-ink-soft">{t("nodedetail.web.sitesEmpty")}</p>
                  ) : (
                    <table className="mt-1 w-full text-left text-xs text-ink">
                      <thead>
                        <tr className="text-ink-soft">
                          <th className="py-1 pr-2 font-medium">{t("nodedetail.web.colSite")}</th>
                          <th className="py-1 pr-2 font-medium">{t("nodedetail.web.colBackend")}</th>
                          <th className="py-1 pr-2 font-medium">{t("nodedetail.web.colPort")}</th>
                          <th className="py-1 pr-2 font-medium">{t("nodedetail.web.colEnabled")}</th>
                          <th className="py-1 font-medium">{t("nodedetail.web.colUser")}</th>
                        </tr>
                      </thead>
                      <tbody>
                        {webReport.sites.map((s) => (
                          <tr key={s.name} className="border-t border-border">
                            <td className="py-1 pr-2 font-mono">{s.name}</td>
                            <td className="py-1 pr-2">
                              {s.backend}
                              {s.php_version ? ` (${s.php_version})` : ""}
                            </td>
                            <td className="py-1 pr-2 font-mono">{s.port ?? "—"}</td>
                            <td className="py-1 pr-2">{s.enabled ? t("nodedetail.web.enabledYes") : t("nodedetail.web.enabledNo")}</td>
                            <td className="py-1 font-mono">{s.user ?? "—"}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  )}
                </div>
              )}
              {webReport.php && (
                <div>
                  <p className="text-xs font-medium text-ink">{t("nodedetail.web.phpTitle")}</p>
                  <p className="mt-1 text-xs text-ink-soft">
                    {t("nodedetail.web.phpSupported")}: {webReport.php.supported.join(", ")}
                  </p>
                  <p className="text-xs text-ink-soft">
                    {t("nodedetail.web.phpInstalled")}:{" "}
                    {webReport.php.installed.length === 0
                      ? t("nodedetail.web.phpNone")
                      : webReport.php.installed.map((i) => i.version).join(", ")}
                  </p>
                  {(webReport.php.pools ?? []).map((p, i) => (
                    <p key={`p${i}`} className="font-mono text-[11px] text-ink-soft">
                      → {p.site} (php {p.version}): {p.path}
                    </p>
                  ))}
                </div>
              )}
              {webReport.status && (
                <div>
                  <p className="text-xs font-medium text-ink">{t("nodedetail.web.statusTitle")}</p>
                  <p className="mt-1 text-xs text-ink">
                    {webReport.status.site.name} — {webReport.status.site.backend}
                    {webReport.status.site.php_version ? ` (${webReport.status.site.php_version})` : ""} ·{" "}
                    {t("nodedetail.web.colPort")} {webReport.status.site.port ?? "—"} ·{" "}
                    {webReport.status.site.enabled ? t("nodedetail.web.enabledYes") : t("nodedetail.web.enabledNo")}
                  </p>
                  {webReport.status.config_path && (
                    <p className="font-mono text-[11px] text-ink-soft">→ {webReport.status.config_path}</p>
                  )}
                  {webReport.status.pool_path && (
                    <p className="font-mono text-[11px] text-ink-soft">→ {webReport.status.pool_path}</p>
                  )}
                  {webReport.status.rollback_path && (
                    <p className="font-mono text-[11px] text-ink-soft">
                      → {t("nodedetail.web.rollbackLabel")}: {webReport.status.rollback_path}
                    </p>
                  )}
                  {webReport.status.listener && (
                    <p className="text-[11px] text-ink-soft">{webReport.status.listener}</p>
                  )}
                </div>
              )}
              {webReport.result && (
                <div>
                  <p className="text-xs font-medium text-ink">
                    {t("nodedetail.web.resultTitle", { action: webReport.result.action, site: webReport.result.site })}{" "}
                    · {webReport.result.executed ? t("nodedetail.web.executedYes") : t("nodedetail.web.executedNo")}
                  </p>
                  {webReport.result.config_path && (
                    <p className="font-mono text-[11px] text-ink-soft">→ {webReport.result.config_path}</p>
                  )}
                  {webReport.result.rollback_path && (
                    <p className="font-mono text-[11px] text-ink-soft">
                      → {t("nodedetail.web.rollbackLabel")}: {webReport.result.rollback_path}
                    </p>
                  )}
                  {webReport.result.docroot_preserved && (
                    <p className="text-[11px] text-ink-soft">{t("nodedetail.web.docPreserved")}</p>
                  )}
                  {(webReport.result.notes ?? []).map((n, i) => (
                    <p key={`n${i}`} className="text-[11px] text-ink-soft">
                      • {n}
                    </p>
                  ))}
                </div>
              )}
            </div>
          )}
        </section>
      )}

      {/* Faz 05: Windows Event Log okuma (yalnız windows node'ları) */}
      {isWindows && (
        <section aria-label={t("nodedetail.eventlog.title")} className="rounded-xl border border-border bg-panel p-4 shadow-sm">
          <h3 className="text-sm font-semibold text-ink">{t("nodedetail.eventlog.title")}</h3>
          <p className="mt-1 text-[11px] text-ink-soft">{t("nodedetail.eventlog.note")}</p>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void submitEventlogRead();
            }}
            className="mt-3 flex flex-wrap items-end gap-2"
          >
            <label className="flex flex-col gap-1">
              <span className="text-xs font-medium text-ink">{t("nodedetail.eventlog.channel")}</span>
              <select
                value={evChannel}
                onChange={(e) => setEvChannel(e.target.value as EventlogChannel)}
                className="rounded-lg border border-border bg-surface px-3 py-1.5 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
              >
                {EVENTLOG_CHANNELS.map((ch) => (
                  <option key={ch} value={ch}>
                    {ch}
                  </option>
                ))}
              </select>
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-xs font-medium text-ink">{t("nodedetail.eventlog.level")}</span>
              <select
                value={evLevel}
                onChange={(e) => setEvLevel(Number(e.target.value))}
                className="rounded-lg border border-border bg-surface px-3 py-1.5 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
              >
                <option value={0}>{t("nodedetail.eventlog.levelAny")}</option>
                <option value={1}>{t("nodedetail.eventlog.levelCritical")}</option>
                <option value={2}>{t("nodedetail.eventlog.levelError")}</option>
                <option value={3}>{t("nodedetail.eventlog.levelWarning")}</option>
                <option value={4}>{t("nodedetail.eventlog.levelInformation")}</option>
                <option value={5}>{t("nodedetail.eventlog.levelVerbose")}</option>
              </select>
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-xs font-medium text-ink">{t("nodedetail.eventlog.count")}</span>
              <input
                type="number"
                min={1}
                max={200}
                value={evCount}
                onChange={(e) => {
                  setEvCount(Number(e.target.value));
                  setEvError(null);
                }}
                className="w-24 rounded-lg border border-border bg-surface px-3 py-1.5 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
              />
            </label>
            <button
              type="submit"
              disabled={evPending}
              className="rounded-lg border border-border bg-surface px-3 py-1.5 text-xs font-medium text-ink hover:bg-neutral-soft disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent"
            >
              {t("nodedetail.eventlog.read")}
            </button>
          </form>
          {evError && (
            <p role="alert" className="mt-2 text-xs text-err">
              {evError}
            </p>
          )}
        </section>
      )}

      {/* Faz 10: Veritabanları + scoped dosya yöneticisi (yalnız db./fs.*
          capability'li node'lar). Şifre node'da üretilir, CP'de seal edilir
          ve TEK SEFERLİK reveal edilir (ADR-0014); dosya kökleri yalnız
          site docroot'larıdır. */}
      {(isDb || isFs) && (
        <DbFsPanel
          nodeId={node.id}
          isDb={isDb}
          isFs={isFs}
          submitTask={async (op, payload) => {
            const created = await submitTask(op, payload);
            return created as unknown as { id: string; status: string; result?: unknown; error_category?: string } | null;
          }}
          t={(k) => t(k as Parameters<typeof t>[0])}
        />
      )}

      {/* Faz 11: E-posta servisi yönetimi (yalnız mail.* capability'li
          node'lar). Preflight kurulum yapmaz (kriter 1); deliverability
          gereksinimleri burada açıklanır (kriter 3); kuyruk içeriksizdir
          (kriter 5). */}
      {isMail && (
        <MailPanel
          nodeId={node.id}
          submitTask={async (op, payload) => {
            const created = await submitTask(op, payload);
            return created as unknown as { id: string; status: string; result?: unknown; error_category?: string } | null;
          }}
          t={(k) => t(k as Parameters<typeof t>[0])}
        />
      )}
    </div>
  );
}

// supportStateLabel maps the agent-reported state to a closed i18n key
// (unknown values fall back to the unknown label — never a false claim).
function supportStateLabel(state: string, t: ReturnType<typeof useI18n>["t"]): string {
  if (state === "supported") return t("nodedetail.facts.support.supported");
  if (state === "untested") return t("nodedetail.facts.support.untested");
  if (state === "") return "—";
  return t("nodedetail.facts.support.unknown");
}

function Fact({ label, value, hint }: { label: string; value: string; hint?: string | undefined }) {
  return (
    <div>
      <dt className="text-[10px] uppercase tracking-wide text-ink-soft">{label}</dt>
      <dd className="truncate text-ink" title={hint ? `${value} — ${hint}` : value}>{value}</dd>
      {hint && <dd className="truncate text-[10px] text-ink-soft" title={hint}>{hint}</dd>}
    </div>
  );
}

function Metric({ label, value, hint }: { label: string; value: string; hint?: string }) {
  return (
    <div>
      <dt className="text-[10px] uppercase tracking-wide text-ink-soft">{label}</dt>
      <dd className="text-ink">{value}</dd>
      {hint && <dd className="text-[10px] text-ink-soft">{hint}</dd>}
    </div>
  );
}
