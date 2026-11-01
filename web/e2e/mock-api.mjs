// E2E mock API — yalnızca geliştirme/E2E amaçlı; gerçek backend CI'da
// E2E_REAL=1 ile çalışır. Sözleşme (JSON şekilleri) gerçek API ile aynıdır:
// docs/architecture/api-and-protocol.md ve backend handler'ları eşleşir.
// CSRF: gerçek sunucuda olduğu gibi oturum token'ından türetilir.
import { createServer } from "node:http";
import { createHash, randomBytes } from "node:crypto";

const PORT = Number(process.env.MOCK_PORT ?? 8787);

const CSRF_PREFIX = "srvctl-csrf:v1:";
const deriveCsrf = (token) => createHash("sha256").update(CSRF_PREFIX + token).digest("hex");

// ---- In-memory durum ----
const users = new Map(); // username -> {id, password, display_name, status, created_at, last_login_at}
const sessions = new Map(); // token -> {user, created}
const nodes = [];
const groups = [];
const tasks = [];
const audit = [];
const enrollmentTokens = [];
let idSeq = 1;
const uuid = () =>
  `${String(idSeq++).padStart(8, "0")}-0000-4000-8000-${String(idSeq).padStart(12, "0")}`;

users.set("admin", {
  id: uuid(),
  password: "guclu-parola-12345",
  display_name: "E2E Yönetici",
  status: "active",
  created_at: new Date().toISOString(),
  last_login_at: null,
});

function logAudit(actor, action, targetType, targetId, result) {
  audit.unshift({
    id: audit.length + 1,
    actor,
    action,
    target_type: targetType,
    target_id: targetId,
    result,
    correlation_id: randomBytes(8).toString("hex"),
    metadata: {},
    created_at: new Date().toISOString(),
  });
}

const OPERATIONS = [
  { name: "noop.echo", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 30 },
  { name: "inventory.node.tag.set", version: "1.0.0", class: "idempotent_desired_state", retry_safe: true, timeout_seconds: 30 },
  // Faz 04: linux systemd servis operasyonları (node hedefli).
  { name: "linux.systemd.service.list", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "linux.systemd.service.status", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "linux.systemd.service.start", version: "1.0.0", class: "idempotent_desired_state", retry_safe: true, timeout_seconds: 120 },
  { name: "linux.systemd.service.stop", version: "1.0.0", class: "idempotent_desired_state", retry_safe: true, timeout_seconds: 120 },
  { name: "linux.systemd.service.restart", version: "1.0.0", class: "non_idempotent", retry_safe: false, timeout_seconds: 120 },
  // Faz 05: windows SCM servis operasyonları + event log okuma.
  { name: "windows.scm.service.list", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "windows.scm.service.status", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "windows.scm.service.start", version: "1.0.0", class: "idempotent_desired_state", retry_safe: true, timeout_seconds: 120 },
  { name: "windows.scm.service.stop", version: "1.0.0", class: "idempotent_desired_state", retry_safe: true, timeout_seconds: 120 },
  { name: "windows.scm.service.restart", version: "1.0.0", class: "non_idempotent", retry_safe: false, timeout_seconds: 180 },
  { name: "windows.eventlog.read", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  // Faz 06: Linux dağıtım adaptörleri (salt-okuma; kural değişikliği yok — ADR-0010).
  { name: "linux.pkg.list", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "linux.firewall.status", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "linux.firewall.rules.list", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "linux.firewall.rule.preview", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  // Faz 07: Docker Engine ailesi (remove ailesi preview-only — ADR-0011).
  { name: "docker.container.list", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "docker.container.inspect", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "docker.container.logs", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 90 },
  { name: "docker.container.start", version: "1.0.0", class: "idempotent_desired_state", retry_safe: true, timeout_seconds: 120 },
  { name: "docker.container.stop", version: "1.0.0", class: "idempotent_desired_state", retry_safe: true, timeout_seconds: 120 },
  { name: "docker.container.restart", version: "1.0.0", class: "non_idempotent", retry_safe: false, timeout_seconds: 180 },
  { name: "docker.container.remove", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "docker.image.list", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "docker.image.remove", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "docker.network.list", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "docker.network.remove", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "docker.volume.list", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "docker.volume.remove", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "docker.compose.validate", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "docker.compose.deploy", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  // Faz 08: web hosting ailesi (gerçek yürütme broker'da --web-enable; ADR-0012).
  { name: "web.site.list", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "web.site.status", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "web.php.list", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "web.site.create", version: "1.0.0", class: "idempotent_desired_state", retry_safe: true, timeout_seconds: 300 },
  { name: "web.site.update", version: "1.0.0", class: "idempotent_desired_state", retry_safe: true, timeout_seconds: 300 },
  { name: "web.site.disable", version: "1.0.0", class: "idempotent_desired_state", retry_safe: true, timeout_seconds: 180 },
  { name: "web.site.delete", version: "1.0.0", class: "non_idempotent", retry_safe: false, timeout_seconds: 300 },
];

// Faz 04 telemetri mock verisi: her node'a agent telemetrisi eklenir.
const nodeFacts = new Map(); // node_id -> facts
const nodeMetrics = new Map(); // node_id -> snapshot
const nodeCapabilities = new Map(); // node_id -> capability[]

function attachMockTelemetry(node) {
  const now = new Date().toISOString();
  nodeFacts.set(node.id, {
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
    collected_at: now,
  });
  nodeMetrics.set(node.id, {
    collected_at: now,
    source: "procfs+statfs",
    cpu: { percent: 12.5, logical_cores: 4, load_1m: 0.4, load_5m: 0.3, load_15m: 0.2 },
    memory: {
      total_bytes: 16 * 1024 ** 3,
      available_bytes: 8 * 1024 ** 3,
      used_bytes: 8 * 1024 ** 3,
      used_percent: 50,
    },
    disks: [
      {
        device: "/dev/vda1",
        mount_point: "/",
        fs_type: "ext4",
        total_bytes: 40 * 1024 ** 3,
        used_bytes: 10 * 1024 ** 3,
        available_bytes: 30 * 1024 ** 3,
      },
    ],
    networks: [{ interface: "ens160", rx_bytes: 987654321, tx_bytes: 123456789 }],
    uptime_seconds: 90061,
  });
  nodeCapabilities.set(node.id, [
    { node_id: node.id, capability_id: "agent.echo", version: "1.0.0", available: true, adapter_id: "builtin" },
    { node_id: node.id, capability_id: "linux.systemd.service.list", version: "1.0.0", available: true, adapter_id: "systemd-broker" },
    { node_id: node.id, capability_id: "linux.systemd.service.status", version: "1.0.0", available: true, adapter_id: "systemd-broker" },
    { node_id: node.id, capability_id: "linux.systemd.service.start", version: "1.0.0", available: true, adapter_id: "systemd-broker" },
    { node_id: node.id, capability_id: "linux.systemd.service.stop", version: "1.0.0", available: true, adapter_id: "systemd-broker" },
    { node_id: node.id, capability_id: "linux.systemd.service.restart", version: "1.0.0", available: true, adapter_id: "systemd-broker" },
  ]);
}


// Faz 05: windows node şablonu — SCM + event log kabiliyetleriyle.
function attachWindowsTelemetry(node) {
  const now = new Date().toISOString();
  nodeFacts.set(node.id, {
    node_id: node.id,
    os_id: "windows",
    os_version_id: "21H2",
    os_pretty_name: "Windows Server 2022 Standard",
    os_home_url: "",
    kernel_release: "build 20348.2849",
    arch: "amd64",
    systemd_version: "",
    package_manager: "msi",
    systemd_available: false,
    source: "registry",
    collected_at: now,
  });
  nodeMetrics.set(node.id, {
    collected_at: now,
    source: "winapi+iphlpapi",
    cpu: { percent: 23.4, logical_cores: 8, load_1m: 0, load_5m: 0, load_15m: 0 },
    memory: {
      total_bytes: 32 * 1024 ** 3,
      available_bytes: 18 * 1024 ** 3,
      used_bytes: 14 * 1024 ** 3,
      used_percent: 43.75,
    },
    disks: [
      {
        device: "C:\\",
        mount_point: "C:\\",
        fs_type: "NTFS",
        total_bytes: 120 * 1024 ** 3,
        used_bytes: 72 * 1024 ** 3,
        available_bytes: 48 * 1024 ** 3,
      },
    ],
    networks: [{ interface: "Ethernet", rx_bytes: 555555555, tx_bytes: 444444444 }],
    uptime_seconds: 432000,
  });
  nodeCapabilities.set(node.id, [
    { node_id: node.id, capability_id: "agent.echo", version: "1.0.0", available: true, adapter_id: "builtin" },
    { node_id: node.id, capability_id: "windows.scm.service.list", version: "1.0.0", available: true, adapter_id: "scm-broker" },
    { node_id: node.id, capability_id: "windows.scm.service.status", version: "1.0.0", available: true, adapter_id: "scm-broker" },
    { node_id: node.id, capability_id: "windows.scm.service.start", version: "1.0.0", available: true, adapter_id: "scm-broker" },
    { node_id: node.id, capability_id: "windows.scm.service.stop", version: "1.0.0", available: true, adapter_id: "scm-broker" },
    { node_id: node.id, capability_id: "windows.scm.service.restart", version: "1.0.0", available: true, adapter_id: "scm-broker" },
    { node_id: node.id, capability_id: "windows.eventlog.read", version: "1.0.0", available: true, adapter_id: "wevtutil" },
  ]);
}

// Faz 06: Rocky/Alma node şablonu — distro sınıflandırması + salt-okuma
// dağıtım kabiliyetleriyle (paket envanteri + firewall sörvey/önizleme).
function attachRockyTelemetry(node) {
  const now = new Date().toISOString();
  nodeFacts.set(node.id, {
    node_id: node.id,
    os_id: "rocky",
    os_version_id: "9.5",
    os_pretty_name: "Rocky Linux 9.5 (Blue Onyx)",
    os_home_url: "https://rockylinux.org/",
    kernel_release: "5.14.0-503.22.1.el9_5.x86_64",
    arch: "amd64",
    systemd_version: "252",
    package_manager: "dnf",
    systemd_available: true,
    distro_family: "rhel",
    distro_like: "rhel centos fedora",
    support_state: "supported",
    support_reason: "desteklenen majör sürüm 9",
    source: "os-release+procfs+systemctl",
    collected_at: now,
  });
  nodeMetrics.set(node.id, {
    collected_at: now,
    source: "procfs+statfs",
    cpu: { percent: 18.2, logical_cores: 4, load_1m: 0.2, load_5m: 0.15, load_15m: 0.1 },
    memory: {
      total_bytes: 8 * 1024 ** 3,
      available_bytes: 5 * 1024 ** 3,
      used_bytes: 3 * 1024 ** 3,
      used_percent: 37.5,
    },
    disks: [
      {
        device: "/dev/vda1",
        mount_point: "/",
        fs_type: "xfs",
        total_bytes: 60 * 1024 ** 3,
        used_bytes: 18 * 1024 ** 3,
        available_bytes: 42 * 1024 ** 3,
      },
    ],
    networks: [{ interface: "eth0", rx_bytes: 333333333, tx_bytes: 222222222 }],
    uptime_seconds: 259200,
  });
  nodeCapabilities.set(node.id, [
    { node_id: node.id, capability_id: "agent.echo", version: "1.0.0", available: true, adapter_id: "builtin" },
    { node_id: node.id, capability_id: "linux.systemd.service.list", version: "1.0.0", available: true, adapter_id: "systemd-broker" },
    { node_id: node.id, capability_id: "linux.systemd.service.status", version: "1.0.0", available: true, adapter_id: "systemd-broker" },
    { node_id: node.id, capability_id: "linux.systemd.service.start", version: "1.0.0", available: true, adapter_id: "systemd-broker" },
    { node_id: node.id, capability_id: "linux.systemd.service.stop", version: "1.0.0", available: true, adapter_id: "systemd-broker" },
    { node_id: node.id, capability_id: "linux.systemd.service.restart", version: "1.0.0", available: true, adapter_id: "systemd-broker" },
    { node_id: node.id, capability_id: "linux.pkg.list", version: "1.0.0", available: true, adapter_id: "pkg-broker" },
    { node_id: node.id, capability_id: "linux.firewall.status", version: "1.0.0", available: true, adapter_id: "fw-broker" },
    { node_id: node.id, capability_id: "linux.firewall.rules.list", version: "1.0.0", available: true, adapter_id: "fw-broker" },
    { node_id: node.id, capability_id: "linux.firewall.rule.preview", version: "1.0.0", available: true, adapter_id: "fw-broker" },
  ]);
}

// Faz 07: docker host şablonu — Engine ailesi kabiliyetleriyle
// (remove ailesi + compose deploy preview-only; ADR-0011).
function attachDockerTelemetry(node) {
  const now = new Date().toISOString();
  nodeFacts.set(node.id, {
    node_id: node.id,
    os_id: "ubuntu",
    os_version_id: "24.04",
    os_pretty_name: "Ubuntu 24.04.3 LTS (docker host)",
    os_home_url: "https://www.ubuntu.com/",
    kernel_release: "6.8.0-49-generic",
    arch: "amd64",
    systemd_version: "255",
    package_manager: "apt",
    systemd_available: true,
    source: "os-release+procfs+systemctl",
    collected_at: now,
  });
  nodeMetrics.set(node.id, {
    collected_at: now,
    source: "procfs+statfs",
    cpu: { percent: 31.7, logical_cores: 8, load_1m: 1.2, load_5m: 0.9, load_15m: 0.7 },
    memory: { total_bytes: 16 * 1024 ** 3, available_bytes: 6 * 1024 ** 3, used_bytes: 10 * 1024 ** 3, used_percent: 62.5 },
    disks: [{ device: "/dev/vda1", mount_point: "/", fs_type: "ext4", total_bytes: 80 * 1024 ** 3, used_bytes: 34 * 1024 ** 3, available_bytes: 46 * 1024 ** 3 }],
    networks: [{ interface: "eth0", rx_bytes: 777777777, tx_bytes: 555555555 }],
    uptime_seconds: 172800,
  });
  const caps = [];
  for (const c of [
    "docker.container.list", "docker.container.inspect", "docker.container.logs",
    "docker.container.start", "docker.container.stop", "docker.container.restart", "docker.container.remove",
    "docker.image.list", "docker.image.remove",
    "docker.network.list", "docker.network.remove",
    "docker.volume.list", "docker.volume.remove",
    "docker.compose.validate", "docker.compose.deploy",
  ]) {
    caps.push({ node_id: node.id, capability_id: c, version: "1.0.0", available: true, adapter_id: c.startsWith("docker.compose.") ? "builtin-compose" : "docker-broker" });
  }
  nodeCapabilities.set(node.id, caps);
}

// Faz 08: web host şablonu — web hosting ailesi kabiliyetleriyle
// (gerçek yürütme broker'da --web-enable; ADR-0012).
function attachWebHost(node) {
  const now = new Date().toISOString();
  nodeFacts.set(node.id, {
    node_id: node.id,
    os_id: "ubuntu",
    os_version_id: "24.04",
    os_pretty_name: "Ubuntu 24.04.3 LTS (web host)",
    os_home_url: "https://www.ubuntu.com/",
    kernel_release: "6.8.0-49-generic",
    arch: "amd64",
    systemd_version: "255",
    package_manager: "apt",
    systemd_available: true,
    source: "os-release+procfs+systemctl",
    collected_at: now,
  });
  nodeMetrics.set(node.id, {
    collected_at: now,
    source: "procfs+statfs",
    cpu: { percent: 12.5, logical_cores: 4, load_1m: 0.4, load_5m: 0.3, load_15m: 0.2 },
    memory: { total_bytes: 8 * 1024 ** 3, available_bytes: 4 * 1024 ** 3, used_bytes: 4 * 1024 ** 3, used_percent: 50 },
    disks: [{ device: "/dev/vda1", mount_point: "/", fs_type: "ext4", total_bytes: 60 * 1024 ** 3, used_bytes: 22 * 1024 ** 3, available_bytes: 38 * 1024 ** 3 }],
    networks: [{ interface: "eth0", rx_bytes: 123456789, tx_bytes: 987654321 }],
    uptime_seconds: 86400,
  });
  const caps = [];
  for (const c of [
    "web.site.list", "web.site.status", "web.php.list",
    "web.site.create", "web.site.update", "web.site.disable", "web.site.delete",
  ]) {
    caps.push({ node_id: node.id, capability_id: c, version: "1.0.0", available: true, adapter_id: "web-broker" });
  }
  nodeCapabilities.set(node.id, caps);
}

function json(res, status, body) {
  const data = JSON.stringify(body);
  res.writeHead(status, { "Content-Type": "application/json; charset=utf-8" });
  res.end(data);
}

function errorBody(code, message, status) {
  return { code, message, correlation_id: "mock", retryable: false, status };
}

// "Worker": queued görevleri 400ms sonra succeeded yapar (gerçek worker
// davranışının E2E gözlenebilir simülasyonu).
setInterval(() => {
  console.log("[mock-tick]", tasks.map((t) => `${t.operation}:${t.status}`).join(",") || "bos");
  const now = new Date().toISOString();
  for (const t of tasks) {
    if (t.status === "queued") {
      t.status = "dispatched";
    } else if (t.status === "dispatched") {
      t.status = "succeeded";
      t.result = { ok: true };
      if (t.operation === "docker.compose.validate" || t.operation === "docker.compose.deploy") {
        // Sahte compose analizi: policy bulguları + (deploy için) plan.
        const report = {
          valid: true,
          services: 2,
          findings: [
            { severity: "high", rule: "privileged", service: "worker", message: "privileged mod — host çekirdeğine tam erişim riski" },
            { severity: "medium", rule: "public-port", service: "web", message: "port tüm arayüzlerde açılır: 8080:80" },
          ],
        };
        if (t.operation === "docker.compose.deploy") {
          report.actions = [
            "container oluştur: web (image=nginx:1.25)",
            "container oluştur: worker (image=worker:2)",
            "container başlat: web",
            "container başlat: worker",
          ];
          report.executed = false;
          report.note = "PLAN — hiçbir kaynak oluşturulmadı (ADR-0011).";
        }
        t.result = report;
      }
      if (t.operation === "web.site.list") {
        t.result = {
          sites: [
            { name: "blog", backend: "static", port: 80, enabled: true, user: "sc-web-blog", user_exists: true, docroot: "/var/www/server-control/blog/html", docroot_exists: true, server_names: ["blog.example.com"] },
            { name: "shop", backend: "php", php_version: "8.3", port: 80, enabled: false, user: "sc-web-shop", user_exists: true, docroot: "/var/www/server-control/shop/html", docroot_exists: true, server_names: ["shop.example.com"] },
          ],
        };
      }
      if (t.operation === "web.site.status") {
        t.result = {
          status: {
            site: { name: (t.payload && t.payload.site) || "blog", backend: "php", php_version: "8.3", port: 80, enabled: true, user: "sc-web-blog", user_exists: true, docroot: "/var/www/server-control/blog/html", docroot_exists: true },
            config_path: "/etc/nginx/conf.d/srvctl-blog.conf",
            rollback_path: "/etc/nginx/conf.d/srvctl-blog.conf.rollback",
            has_rollback: true,
            pool_path: "/etc/php/8.3/fpm/pool.d/srvctl-blog.conf",
            pool_installed: true,
            listener: "port 80: nginx (yönetilen vhost)",
          },
        };
      }
      if (t.operation === "web.php.list") {
        t.result = {
          php: {
            supported: ["8.2", "8.3", "8.4", "8.5"],
            installed: [{ version: "8.3", unit: "php8.3-fpm.service", pool_dir: "/etc/php/8.3/fpm/pool.d" }],
            pools: [{ site: "blog", version: "8.3", path: "/etc/php/8.3/fpm/pool.d/srvctl-blog.conf" }],
          },
        };
      }
      if (t.operation === "web.site.create" || t.operation === "web.site.update") {
        const site = (t.payload && t.payload.name) || "site";
        const act = t.operation.split(".")[2];
        t.result = {
          result: {
            site, action: act, executed: true,
            config_path: "/etc/nginx/conf.d/srvctl-" + site + ".conf",
            rollback_path: t.operation === "web.site.update" ? "/etc/nginx/conf.d/srvctl-" + site + ".conf.rollback" : undefined,
            docroot: "/var/www/server-control/" + site + "/html",
            user: "sc-web-" + site,
            php_version: (t.payload && t.payload.php_version) || undefined,
            pool_path: t.payload && t.payload.backend === "php" ? "/etc/php/" + (t.payload.php_version || "8.3") + "/fpm/pool.d/srvctl-" + site + ".conf" : undefined,
            notes: ["syntax check (nginx -t) geçti; reload sinyali verildi"],
          },
        };
      }
      if (t.operation === "web.site.disable" || t.operation === "web.site.delete") {
        const site = (t.payload && t.payload.site) || "site";
        const act = t.operation.split(".")[2];
        t.result = {
          result: {
            site, action: act, executed: true,
            config_path: act === "disable" ? "/etc/nginx/conf.d/srvctl-" + site + ".conf.disabled" : "/etc/nginx/conf.d/srvctl-" + site + ".conf",
            rollback_path: act === "delete" ? "/etc/nginx/conf.d/srvctl-" + site + ".conf.rollback" : undefined,
            docroot: "/var/www/server-control/" + site + "/html",
            docroot_preserved: act === "delete" ? true : undefined,
            user: "sc-web-" + site,
            notes: act === "disable" ? ["config .disabled uzantısıyla saklandı"] : ["document root KORUNDU (kullanıcı verisi silinmez — ADR-0012)"],
          },
        };
      }
      if (t.operation === "docker.container.remove" || t.operation === "docker.image.remove" ||
          t.operation === "docker.volume.remove" || t.operation === "docker.network.remove") {
        t.result = {
          kind: t.operation.split(".")[1],
          ref: (t.payload && t.payload.ref) || "hedef",
          executed: false,
          note: "ÖNİZLEME — hiçbir şey silinmedi (ADR-0011).",
          api_path: "/v1.47/" + t.operation.split(".")[1] + "s/" + ((t.payload && t.payload.ref) || "hedef"),
        };
      }
      logAudit("system", "task.complete", "task", t.id, "success");
    }
  }
  void now;
}, 400).unref();

const server = createServer(async (req, res) => {
  const url = new URL(req.url, `http://127.0.0.1:${PORT}`);
  const path = url.pathname;
  const method = req.method ?? "GET";

  const readBody = () =>
    new Promise((resolve) => {
      let raw = "";
      req.on("data", (c) => (raw += c));
      req.on("end", () => {
        try {
          resolve(raw ? JSON.parse(raw) : {});
        } catch {
          resolve({});
        }
      });
    });

  const cookieToken = (() => {
    const m = /srvctl_session=([^;]+)/.exec(req.headers.cookie ?? "");
    return m ? decodeURIComponent(m[1]) : "";
  })();
  const session = sessions.get(cookieToken);
  const actor = session?.user?.username ?? "anonymous";

  const mutations = new Set(["POST", "PUT", "PATCH", "DELETE"]);
  if (mutations.has(method)) {
    const exempt =
      path === "/api/v1/auth/login" || path === "/api/v1/bootstrap/admin" || path === "/e2e/reset";
    const presented = req.headers["x-csrf-token"] ?? "";
    if (!exempt && (!session || presented !== deriveCsrf(cookieToken))) {
      json(res, 403, errorBody("csrf_invalid", "CSRF doğrulaması başarısız.", 403));
      return;
    }
  }

  // E2E durum sıfırlama (yalnız MOCK; spec'ler açık önkoşul bildirir):
  // node/görev/audit/telemetri durumunu temizler, oturumları KORUR.
  // CSRF muaf (yalnız test altyapısı; üretimde karşılığı yoktur).
  if (method === "POST" && path === "/e2e/reset") {
    nodes.length = 0;
    groups.length = 0;
    tasks.length = 0;
    audit.length = 0;
    enrollmentTokens.length = 0;
    nodeFacts.clear();
    nodeMetrics.clear();
    nodeCapabilities.clear();
    json(res, 200, { status: "reset", mock: true });
    return;
  }

  // ---- auth ----
  if (method === "POST" && path === "/api/v1/auth/login") {
    const body = await readBody();
    const u = users.get(String(body.username ?? ""));
    if (!u || u.password !== body.password || u.status !== "active") {
      json(res, 401, errorBody("invalid_credentials", "Kullanıcı adı veya parola hatalı.", 401));
      return;
    }
    const token = randomBytes(32).toString("base64url");
    u.last_login_at = new Date().toISOString();
    sessions.set(token, { user: { id: u.id, username: "admin" }, created: Date.now() });
    logAudit("admin", "auth.login", "session", u.id, "success");
    res.setHeader(
      "Set-Cookie",
      `srvctl_session=${encodeURIComponent(token)}; Path=/; HttpOnly; SameSite=Strict`,
    );
    json(res, 200, {
      user: { id: u.id, username: "admin" },
      csrf_token: deriveCsrf(token),
      expires_at: new Date(Date.now() + 12 * 3600_000).toISOString(),
    });
    return;
  }
  if (method === "GET" && path === "/api/v1/auth/session") {
    if (!session) {
      json(res, 401, errorBody("unauthorized", "Oturum gerekli.", 401));
      return;
    }
    json(res, 200, {
      user_id: session.user.id,
      username: session.user.username,
      session_id: uuid(),
      server_time: new Date().toISOString(),
    });
    return;
  }
  if (method === "GET" && path === "/api/v1/auth/csrf") {
    if (!session) {
      json(res, 401, errorBody("unauthorized", "Oturum gerekli.", 401));
      return;
    }
    json(res, 200, { csrf_token: deriveCsrf(cookieToken) });
    return;
  }
  if (method === "POST" && path === "/api/v1/auth/logout") {
    sessions.delete(cookieToken);
    res.setHeader("Set-Cookie", "srvctl_session=; Path=/; Max-Age=0");
    json(res, 200, { status: "ok" });
    return;
  }

  // E2E health (webServer readiness için; oturumsuz).
  if (method === "GET" && path === "/e2e/health") {
    json(res, 200, { status: "ok", mock: true });
    return;
  }

  // Korunan uçlar: oturum şart.
  if (!session && path.startsWith("/api/v1/")) {
    json(res, 401, errorBody("unauthorized", "Oturum gerekli.", 401));
    return;
  }

  // ---- overview / users ----
  if (method === "GET" && path === "/api/v1/overview") {
    json(res, 200, { nodes: nodes.length, tasks: tasks.length });
    return;
  }
  if (method === "GET" && path === "/api/v1/users") {
    json(res, 200, {
      users: [...users.values()].map((u) => ({
        id: u.id,
        username: "admin",
        display_name: u.display_name,
        status: u.status,
        created_at: u.created_at,
        last_login_at: u.last_login_at,
      })),
    });
    return;
  }

  // ---- nodes ----
  if (method === "GET" && path === "/api/v1/nodes") {
    json(res, 200, { nodes, has_more: false });
    return;
  }
  if (method === "POST" && path === "/api/v1/nodes") {
    const body = await readBody();
    if (!body.display_name) {
      json(res, 400, errorBody("invalid_request", "Görünen ad gerekli.", 400));
      return;
    }
    const node = {
      id: uuid(),
      display_name: body.display_name,
      hostname: body.hostname || null,
      os_family: body.os_family || "unknown",
      os_name: body.os_name ?? null,
      os_version: body.os_version ?? null,
      arch: body.arch ?? null,
      agent_version: null,
      status: "pending",
      tags: Array.isArray(body.tags) ? body.tags : [],
      group_ids: Array.isArray(body.group_ids) ? body.group_ids : [],
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
      last_seen_at: null,
    };
    nodes.push(node);
    // Faz 05 (yalnız MOCK): windows ailesi işaretli ya da "win-" önekli
    // node'lar Windows telemetri şablonuyla doğar — E2E'nin SCM/eventlog
    // akışlarını form bağımsız sınaması için.
    if (body.os_family === "windows" || String(body.display_name || "").startsWith("win-")) {
      attachWindowsTelemetry(node);
    } else if (body.os_family === "rocky" || String(body.display_name || "").startsWith("rocky-") || String(body.display_name || "").startsWith("alma-")) {
      attachRockyTelemetry(node);
    } else if (String(body.display_name || "").startsWith("docker-") || body.os_family === "docker") {
      attachDockerTelemetry(node);
    } else if (String(body.display_name || "").startsWith("web-") || body.os_family === "web") {
      attachWebHost(node);
    } else {
      attachMockTelemetry(node);
    }
    logAudit(actor, "node.create", "node", node.id, "success");
    json(res, 201, node);
    return;
  }

  // ---- node telemetri (Faz 04) ----
  const factsMatch = /^\/api\/v1\/nodes\/([^/]+)\/facts\/system$/.exec(path);
  if (method === "GET" && factsMatch) {
    const f = nodeFacts.get(factsMatch[1]);
    if (!f) {
      json(res, 404, errorBody("not_found", "Telemetri kaydı bulunamadı.", 404));
      return;
    }
    json(res, 200, f);
    return;
  }
  const latestMatch = /^\/api\/v1\/nodes\/([^/]+)\/metrics\/latest$/.exec(path);
  if (method === "GET" && latestMatch) {
    const m = nodeMetrics.get(latestMatch[1]);
    if (!m) {
      json(res, 404, errorBody("not_found", "Telemetri kaydı bulunamadı.", 404));
      return;
    }
    json(res, 200, m);
    return;
  }
  const capsMatch = /^\/api\/v1\/nodes\/([^/]+)\/capabilities$/.exec(path);
  if (method === "GET" && capsMatch) {
    json(res, 200, { capabilities: nodeCapabilities.get(capsMatch[1]) ?? [] });
    return;
  }

  // ---- groups ----
  if (method === "GET" && path === "/api/v1/node-groups") {
    json(res, 200, { groups });
    return;
  }
  if (method === "POST" && path === "/api/v1/node-groups") {
    const body = await readBody();
    const g = { id: uuid(), name: body.name, description: body.description ?? "", created_at: new Date().toISOString() };
    groups.push(g);
    logAudit(actor, "node.group.create", "node_group", g.id, "success");
    json(res, 201, g);
    return;
  }

  // ---- tasks ----
  if (method === "GET" && path === "/api/v1/task-operations") {
    json(res, 200, { operations: OPERATIONS });
    return;
  }
  if (method === "GET" && path === "/api/v1/tasks") {
    json(res, 200, { tasks, has_more: false });
    return;
  }
  const taskMatch = /^\/api\/v1\/tasks\/([^/]+)$/.exec(path);
  if (method === "GET" && taskMatch) {
    const t = tasks.find((x) => x.id === taskMatch[1]);
    if (!t) {
      json(res, 404, errorBody("not_found", "Görev bulunamadı.", 404));
      return;
    }
    json(res, 200, t);
    return;
  }
  if (method === "POST" && path === "/api/v1/tasks") {
    const body = await readBody();
    if (!OPERATIONS.some((o) => o.name === body.operation)) {
      json(res, 400, errorBody("unsupported_operation", "Desteklenmeyen operasyon.", 400));
      return;
    }
    const task = {
      id: uuid(),
      operation: body.operation,
      operation_version: "1.0.0",
      node_id: body.node_id || null,
      payload: body.payload ?? {},
      status: "queued",
      actor,
      correlation_id: randomBytes(8).toString("hex"),
      idempotency_key: body.idempotency_key || null,
      error_category: null,
      attempts: 0,
      max_attempts: 3,
      retry_safe: true,
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
    };
    tasks.unshift(task);
    logAudit(actor, "task.create", "task", task.id, "success");
    json(res, 201, task);
    return;
  }
  const cancelMatch = /^\/api\/v1\/tasks\/([^/]+)\/cancel$/.exec(path);
  if (method === "POST" && cancelMatch) {
    const t = tasks.find((x) => x.id === cancelMatch[1]);
    if (!t || t.status !== "queued") {
      json(res, 409, errorBody("not_cancellable", "Görev iptal edilemez durumda.", 409));
      return;
    }
    t.status = "cancelled";
    logAudit(actor, "task.cancel", "task", t.id, "success");
    json(res, 200, t);
    return;
  }

    // ---- agent enrollment tokens (mock) ----
  if (method === "GET" && path === "/api/v1/agent-enrollments") {
    json(res, 200, { tokens: enrollmentTokens });
    return;
  }
  if (method === "POST" && path === "/api/v1/agent-enrollments") {
    const body = await readBody();
    const now = new Date().toISOString();
    const ttl = (body.ttl_minutes ?? 15) * 60_000;
    const tok = {
      id: uuid(),
      scope_type: body.scope_type === "node" ? "node" : "group",
      node_id: body.node_id || null,
      group_id: body.group_id || null,
      created_by: actor,
      expires_at: new Date(Date.now() + ttl).toISOString(),
      consumed_at: null,
      revoked_at: null,
      created_at: now,
    };
    enrollmentTokens.unshift(tok);
    logAudit(actor, "agent.token.issue", "enrollment_token", tok.id, "success");
    // Plaintext yalnızca oluşturma yanıtında bir kez döner (gerçek API gibi).
    const plaintext = "e2e-" + tok.id.slice(0, 8) + "-mock-token";
    json(res, 201, { token: tok, plaintext, expires_at: tok.expires_at, warning: "tek gösterim" });
    return;
  }
  const revokeMatch = /^\/api\/v1\/agent-enrollments\/([^/]+)\/revoke$/.exec(path);
  if (method === "POST" && revokeMatch) {
    const tok = enrollmentTokens.find((x) => x.id === revokeMatch[1] && !x.consumed_at && !x.revoked_at);
    if (!tok) {
      json(res, 404, errorBody("not_found", "Token bulunamadı.", 404));
      return;
    }
    tok.revoked_at = new Date().toISOString();
    logAudit(actor, "agent.token.revoke", "enrollment_token", tok.id, "success");
    json(res, 200, { status: "ok" });
    return;
  }

  // ---- audit ----
  if (method === "GET" && path === "/api/v1/audit") {
    const action = url.searchParams.get("action");
    const filtered = action ? audit.filter((e) => e.action === action) : audit;
    json(res, 200, { entries: filtered.slice(0, 50), has_more: false });
    return;
  }

  json(res, 404, errorBody("not_found", "İstenen kaynak bulunamadı.", 404));
});

server.listen(PORT, "127.0.0.1", () => {
  console.log(`e2e mock api listening on http://127.0.0.1:${PORT}`);
});
