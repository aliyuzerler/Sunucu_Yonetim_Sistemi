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
// Faz 09 mock state: sağlayıcılar reset ile temizlenir; sertifikalar demo
// içerikle yeniden tohumlanır (yenileme başarısızlığı + rate_limit görünür
// örnek — kriter 3'ün mock modda doğrulanması).
const dnsProviders = [];
let certificates = [];
function seedDemoCertificates() {

  certificates = [
    {
      id: uuid(),
      name: "demo.example.internal",
      sans: ["demo.example.internal", "www.demo.example.internal"],
      provider_id: null,
      node_id: null,
      state: "renewal_failed",
      not_after: new Date(Date.now() + 12 * 86400e3).toISOString(),
      fingerprint: "aa11bb22cc33dd44".repeat(4),
      last_error: "A: 5 validation errors per week (rate limit)",
      last_error_class: "rate_limit",
      retry_after_secs: 3600,
      expires_in_days: 12,
      expiring_soon: true,
      effective_state: "renewal_failed",
      issued_at: new Date(Date.now() - 78 * 86400e3).toISOString(),
      created_at: new Date(Date.now() - 80 * 86400e3).toISOString(),
      updated_at: new Date(Date.now() - 86400e3).toISOString(),
    },
  ];
}
let idSeq = 1;
const uuid = () =>
  `${String(idSeq++).padStart(8, "0")}-0000-4000-8000-${String(idSeq).padStart(12, "0")}`;

seedDemoCertificates();

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
  // Faz 10: veritabanı + scoped dosya yöneticisi (şifre node'da üretilir,
  // seal edilir, tek-seferlik reveal — ADR-0014).
  { name: "db.instance.list", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "db.database.list", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "db.database.create", version: "1.0.0", class: "idempotent_desired_state", retry_safe: true, timeout_seconds: 60 },
  { name: "db.database.delete", version: "1.0.0", class: "destructive", retry_safe: false, timeout_seconds: 120 },
  { name: "db.user.list", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "db.user.create", version: "1.0.0", class: "idempotent_desired_state", retry_safe: true, timeout_seconds: 60 },
  { name: "db.user.delete", version: "1.0.0", class: "destructive", retry_safe: false, timeout_seconds: 60 },
  { name: "db.grant.set", version: "1.0.0", class: "idempotent_desired_state", retry_safe: true, timeout_seconds: 60 },
  { name: "db.backup", version: "1.0.0", class: "non_idempotent", retry_safe: true, timeout_seconds: 300 },
  { name: "db.backup.list", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "db.restore", version: "1.0.0", class: "destructive", retry_safe: false, timeout_seconds: 300 },
  { name: "fs.list", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "fs.download", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "fs.upload", version: "1.0.0", class: "idempotent_desired_state", retry_safe: true, timeout_seconds: 120 },
  { name: "fs.chmod", version: "1.0.0", class: "idempotent_desired_state", retry_safe: true, timeout_seconds: 60 },
  { name: "fs.chown", version: "1.0.0", class: "idempotent_desired_state", retry_safe: true, timeout_seconds: 60 },
  // Faz 11: e-posta servisi (Postfix/Dovecot/Rspamd — ADR-0015).
  { name: "mail.preflight", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "mail.status", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "mail.domain.list", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "mail.domain.create", version: "1.0.0", class: "idempotent_desired_state", retry_safe: true, timeout_seconds: 60 },
  { name: "mail.domain.delete", version: "1.0.0", class: "destructive", retry_safe: false, timeout_seconds: 60 },
  { name: "mail.mailbox.list", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "mail.mailbox.create", version: "1.0.0", class: "idempotent_desired_state", retry_safe: true, timeout_seconds: 120 },
  { name: "mail.mailbox.delete", version: "1.0.0", class: "destructive", retry_safe: false, timeout_seconds: 120 },
  { name: "mail.alias.list", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "mail.alias.create", version: "1.0.0", class: "idempotent_desired_state", retry_safe: true, timeout_seconds: 60 },
  { name: "mail.alias.delete", version: "1.0.0", class: "destructive", retry_safe: false, timeout_seconds: 60 },
  { name: "mail.quota.set", version: "1.0.0", class: "idempotent_desired_state", retry_safe: true, timeout_seconds: 60 },
  { name: "mail.config.check", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "mail.queue.list", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "mail.limits.get", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "mail.limits.set", version: "1.0.0", class: "idempotent_desired_state", retry_safe: true, timeout_seconds: 120 },
  // Faz 12: KVM/QEMU/libvirt (disk yolu payload'da yoktur — ADR-0016).
  { name: "vm.preflight", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "vm.status", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "vm.list", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "vm.info", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "vm.create", version: "1.0.0", class: "idempotent_desired_state", retry_safe: true, timeout_seconds: 300 },
  { name: "vm.clone", version: "1.0.0", class: "non_idempotent", retry_safe: false, timeout_seconds: 300 },
  { name: "vm.start", version: "1.0.0", class: "idempotent_desired_state", retry_safe: true, timeout_seconds: 300 },
  { name: "vm.stop", version: "1.0.0", class: "idempotent_desired_state", retry_safe: true, timeout_seconds: 300 },
  { name: "vm.reboot", version: "1.0.0", class: "non_idempotent", retry_safe: false, timeout_seconds: 300 },
  { name: "vm.delete", version: "1.0.0", class: "destructive", retry_safe: false, timeout_seconds: 300 },
  { name: "vm.disk.list", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "vm.disk.add", version: "1.0.0", class: "idempotent_desired_state", retry_safe: true, timeout_seconds: 300 },
  { name: "vm.snapshot.list", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "vm.snapshot.create", version: "1.0.0", class: "idempotent_desired_state", retry_safe: true, timeout_seconds: 300 },
  { name: "vm.snapshot.revert", version: "1.0.0", class: "non_idempotent", retry_safe: false, timeout_seconds: 300 },
  { name: "vm.snapshot.delete", version: "1.0.0", class: "destructive", retry_safe: false, timeout_seconds: 300 },
  // Faz 09: DNS + SSL (sağlayıcı kimlik bilgileri şifreli; anahtar node'da — ADR-0013).
  { name: "dns.zone.list", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "dns.record.list", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "dns.record.diff", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
  { name: "dns.record.apply", version: "1.0.0", class: "idempotent_desired_state", retry_safe: true, timeout_seconds: 120 },
  { name: "dns.record.delete", version: "1.0.0", class: "non_idempotent", retry_safe: false, timeout_seconds: 120 },
  { name: "cert.issue", version: "1.0.0", class: "non_idempotent", retry_safe: false, timeout_seconds: 300 },
  { name: "cert.renew", version: "1.0.0", class: "non_idempotent", retry_safe: false, timeout_seconds: 300 },
  { name: "cert.revoke", version: "1.0.0", class: "non_idempotent", retry_safe: false, timeout_seconds: 120 },
  { name: "web.cert.request", version: "1.0.0", class: "idempotent_desired_state", retry_safe: true, timeout_seconds: 60 },
  { name: "web.cert.install", version: "1.0.0", class: "idempotent_desired_state", retry_safe: true, timeout_seconds: 120 },
  { name: "web.cert.status", version: "1.0.0", class: "read_only", retry_safe: true, timeout_seconds: 60 },
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
// Faz 10: db + fs kabiliyet şablonu — "dbfs-" önekli node'lar (E2E).
const mockDbCredentials = []; // {id, node_id, kind, username, host, fingerprint, revealed_at}

function attachDbFsTelemetry(node) {
  const now = new Date().toISOString();
  nodeFacts.set(node.id, {
    node_id: node.id,
    os_id: "ubuntu",
    os_version_id: "24.04",
    os_pretty_name: "Ubuntu 24.04.3 LTS (db+fs host)",
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
    cpu: { percent: 9.1, logical_cores: 2, load_1m: 0.2, load_5m: 0.2, load_15m: 0.1 },
    memory: { total_bytes: 4 * 1024 ** 3, available_bytes: 2 * 1024 ** 3, used_bytes: 2 * 1024 ** 3, used_percent: 50 },
    disks: [{ device: "/dev/vda1", mount_point: "/", fs_type: "ext4", total_bytes: 40 * 1024 ** 3, used_bytes: 12 * 1024 ** 3, available_bytes: 28 * 1024 ** 3 }],
    networks: [{ interface: "eth0", rx_bytes: 1111, tx_bytes: 2222 }],
    uptime_seconds: 7200,
  });
  const caps = [];
  for (const c of [
    "db.instance.list", "db.database.list", "db.database.create", "db.database.delete",
    "db.user.list", "db.user.create", "db.user.delete", "db.grant.set",
    "db.backup", "db.backup.list", "db.restore",
  ]) {
    caps.push({ node_id: node.id, capability_id: c, version: "1.0.0", available: true, adapter_id: "db-broker" });
  }
  for (const c of ["fs.list", "fs.download", "fs.upload", "fs.chmod", "fs.chown"]) {
    caps.push({ node_id: node.id, capability_id: c, version: "1.0.0", available: true, adapter_id: "fs-broker" });
  }
  nodeCapabilities.set(node.id, caps);
  // Demo kimlik: app_ro — henüz gösterilmemiş (reveal tek seferlik mock'u).
  mockDbCredentials.push({
    id: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee",
    node_id: node.id,
    kind: "postgres",
    username: "app_ro",
    host: "",
    fingerprint: "demo1234demo1234",
    revealed_at: null,
  });
}

// Faz 11: e-posta servisi şablonu — "mail-" önekli node'lar (E2E).
// 16 mail.* capability'si raporlanır (gerçek yürütme broker'da --mail-enable;
// ADR-0015). Demo mailbox credential'ı (kind="mailbox") tek-seferlik reveal
// mock'uyla birlikte seed edilir.
const mockMailDomains = []; // mail- node'una göre değil, tek demo alanı
const mockMailMailboxes = []; // {domain, local, quota_bytes}
const mockMailAliases = []; // {alias, target}
// Kuyruk demosu: alıcı adresi ve gövde YOK (kriter 5).
const mockMailQueue = [
  {
    queue_id: "3F1A2B4C5D",
    queue_name: "deferred",
    size_bytes: 2048,
    age_seconds: 900,
    sender: "bounce@mail.example.com",
    reason: "connect to remote.example: Connection refused",
  },
];

function attachMailTelemetry(node) {
  const now = new Date().toISOString();
  nodeFacts.set(node.id, {
    node_id: node.id,
    os_id: "debian",
    os_version_id: "13",
    os_pretty_name: "Debian 13 (mail host)",
    os_home_url: "https://www.debian.org/",
    kernel_release: "6.12.0-1-amd64",
    arch: "amd64",
    systemd_version: "257",
    package_manager: "apt",
    systemd_available: true,
    source: "os-release+procfs+systemctl",
    collected_at: now,
  });
  nodeMetrics.set(node.id, {
    collected_at: now,
    source: "procfs+statfs",
    cpu: { percent: 12.5, logical_cores: 4, load_1m: 0.4, load_5m: 0.3, load_15m: 0.2 },
    memory: { total_bytes: 8 * 1024 ** 3, available_bytes: 5 * 1024 ** 3, used_bytes: 3 * 1024 ** 3, used_percent: 37.5 },
    disks: [{ device: "/dev/vda1", mount_point: "/", fs_type: "ext4", total_bytes: 80 * 1024 ** 3, used_bytes: 22 * 1024 ** 3, available_bytes: 58 * 1024 ** 3 }],
    networks: [{ interface: "eth0", rx_bytes: 999999, tx_bytes: 888888 }],
    uptime_seconds: 123456,
  });
  const caps = [];
  for (const c of [
    "mail.preflight", "mail.status",
    "mail.domain.list", "mail.domain.create", "mail.domain.delete",
    "mail.mailbox.list", "mail.mailbox.create", "mail.mailbox.delete",
    "mail.alias.list", "mail.alias.create", "mail.alias.delete",
    "mail.quota.set", "mail.config.check", "mail.queue.list",
    "mail.limits.get", "mail.limits.set",
  ]) {
    caps.push({ node_id: node.id, capability_id: c, version: "1.0.0", available: true, adapter_id: "mail-broker" });
  }
  nodeCapabilities.set(node.id, caps);
  // Demo domain + mailbox + kimlik (yalnız İLK mail node'unda bir kez).
  if (mockMailDomains.length === 0) {
    mockMailDomains.push("mail.example.com");
    mockMailMailboxes.push({ domain: "mail.example.com", local: "ali", quota_bytes: 1024 ** 3 });
    mockMailAliases.push({ alias: "info@mail.example.com", target: "ali@mail.example.com" });
    mockDbCredentials.push({
      id: "bbbbbbbb-cccc-4ddd-8eee-ffffffffff01",
      node_id: node.id,
      kind: "mailbox",
      username: "ali@mail.example.com",
      host: "",
      fingerprint: "maildemo0000mail",
      revealed_at: null,
    });
  }
}

// Faz 12: sanallaştırma şablonu — "vm-" önekli node'lar (E2E).
// 16 vm.* capability'si raporlanır (gerçek yürütme broker'da --vm-enable;
// ADR-0016). Demo VM durumu tek listede tutulur (mock, node-bağımsız).
const mockVMs = []; // { name, state, running, vcpu, mem, disk, pool, bridge, volume }
const mockVMSnapshots = {}; // name -> [{name, current}]

function attachVmTelemetry(node) {
  const now = new Date().toISOString();
  nodeFacts.set(node.id, {
    node_id: node.id,
    os_id: "ubuntu",
    os_version_id: "24.04",
    os_pretty_name: "Ubuntu 24.04.3 LTS (KVM host)",
    os_home_url: "https://www.ubuntu.com/",
    kernel_release: "6.8.0-49-generic",
    arch: "x86_64",
    systemd_version: "255",
    package_manager: "apt",
    systemd_available: true,
    source: "os-release+procfs+systemctl",
    collected_at: now,
  });
  nodeMetrics.set(node.id, {
    collected_at: now,
    source: "procfs+statfs",
    cpu: { percent: 22.5, logical_cores: 16, load_1m: 1.4, load_5m: 1.1, load_15m: 0.9 },
    memory: { total_bytes: 32 * 1024 ** 3, available_bytes: 24 * 1024 ** 3, used_bytes: 8 * 1024 ** 3, used_percent: 25 },
    disks: [{ device: "/dev/sda1", mount_point: "/", fs_type: "ext4", total_bytes: 500 * 1024 ** 3, used_bytes: 120 * 1024 ** 3, available_bytes: 380 * 1024 ** 3 }],
    networks: [{ interface: "br0", rx_bytes: 123456789, tx_bytes: 987654321 }],
    uptime_seconds: 864000,
  });
  const caps = [];
  for (const c of [
    "vm.preflight", "vm.status", "vm.list", "vm.info",
    "vm.create", "vm.clone", "vm.start", "vm.stop", "vm.reboot", "vm.delete",
    "vm.disk.list", "vm.disk.add",
    "vm.snapshot.list", "vm.snapshot.create", "vm.snapshot.revert", "vm.snapshot.delete",
  ]) {
    caps.push({ node_id: node.id, capability_id: c, version: "1.0.0", available: true, adapter_id: "vm-broker" });
  }
  nodeCapabilities.set(node.id, caps);
  if (mockVMs.length === 0) {
    mockVMs.push({ name: "demo-vm", state: "shut off", running: false, vcpu: 2, mem: 512, disk: 20, pool: "default", bridge: "br0", volume: "sc-demo-vm-root" });
    mockVMSnapshots["demo-vm"] = [{ name: "temiz-kurulum", current: true }];
  }
}

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
    "web.cert.request", "web.cert.install", "web.cert.status",
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
      // ---- Faz 09: DNS + SSL görev sonuçları ----
      if (t.operation === "dns.zone.list") {
        t.result = {
          zones: ["example.com", "test.example.internal"],
          capabilities: {
            record_types: ["A", "AAAA", "CNAME", "TXT", "MX"],
            ttl_min: 60, ttl_max: 86400, supports_dns01: true, wildcard: true,
            note: "yerleşik mock sağlayıcı (in-memory; yalnız test/offline değerlendirme)",
          },
        };
      }
      if (t.operation === "dns.record.list") {
        t.result = {
          records: [
            { name: "@", type: "A", value: "203.0.113.10", ttl: 3600 },
            { name: "www", type: "A", value: "203.0.113.10", ttl: 300 },
            { name: "www", type: "AAAA", value: "2001:db8::10", ttl: 300 },
            { name: "_acme-challenge", type: "TXT", value: "mock-digest-value", ttl: 60 },
          ],
          capabilities: { record_types: ["A", "AAAA", "CNAME", "TXT", "MX"], ttl_min: 60, ttl_max: 86400, supports_dns01: true, wildcard: true },
        };
      }
      if (t.operation === "dns.record.diff" || t.operation === "dns.record.apply") {
        const p = t.payload || {};
        const action = p.name === "www" && p.type === "A" ? "update" : "create";
        t.result = {
          change: {
            action,
            before: action === "update" ? { name: p.name, type: p.type, value: "203.0.113.10", ttl: 300 } : null,
            after: { name: p.name, type: p.type, value: p.value, ttl: p.ttl },
          },
        };
      }
      if (t.operation === "dns.record.delete") {
        t.result = { deleted: { name: (t.payload && t.payload.name) || "@", type: (t.payload && t.payload.type) || "A" } };
      }
      if (t.operation === "web.cert.request") {
        const d = (t.payload && t.payload.domain) || "example.internal";
        t.result = {
          request: {
            domain: d,
            sans: [d, ...((t.payload && t.payload.sans) || [])],
            csr_pem: "-----BEGIN CERTIFICATE REQUEST-----\nMIIBmockCSRPayload\n-----END CERTIFICATE REQUEST-----",
            key_fingerprint: "11".repeat(32),
            key_reused: false,
          },
        };
      }
      if (t.operation === "cert.issue" || t.operation === "cert.renew") {
        const name = (t.payload && t.payload.name) || "example.internal";
        const rec = {
          id: uuid(),
          name,
          sans: [name, ...((t.payload && t.payload.sans) || [])],
          provider_id: (t.payload && t.payload.provider_id) || null,
          node_id: (t.payload && t.payload.node_id) || null,
          state: "issued",
          not_after: new Date(Date.now() + 90 * 86400e3).toISOString(),
          fingerprint: "22".repeat(32),
          last_error: null,
          last_error_class: null,
          retry_after_secs: 0,
          expires_in_days: 90,
          expiring_soon: false,
          effective_state: "issued",
          issued_at: new Date().toISOString(),
          created_at: new Date().toISOString(),
          updated_at: new Date().toISOString(),
        };
        certificates.unshift(rec);
        t.result = {
          certificate: rec,
          cert_pem: "-----BEGIN CERTIFICATE-----\nMIIBmockLeafCertificate\n-----END CERTIFICATE-----",
          chain_pem: "-----BEGIN CERTIFICATE-----\nMIIBmockChainCertificate\n-----END CERTIFICATE-----",
        };
      }
      if (t.operation === "cert.revoke") {
        const rec = certificates.find((c) => c.id === (t.payload && t.payload.cert_id));
        if (rec) {
          rec.state = "revoked";
          rec.effective_state = "revoked";
          rec.revoked_at = new Date().toISOString();
        }
        t.result = { certificate: rec ?? null };
      }
      if (t.operation === "web.cert.install") {
        const d = (t.payload && t.payload.domain) || "example.internal";
        t.result = {
          result: {
            domain: d,
            not_after: new Date(Date.now() + 90 * 86400e3).toISOString(),
            fingerprint: "22".repeat(32),
            reloaded: true,
            rollback_used: false,
            notes: ["nginx -t kapısı geçildi; reload sinyali verildi"],
          },
        };
      }
      if (t.operation === "web.cert.status") {
        t.result = {
          certificates: [
            { domain: (t.payload && t.payload.domain) || "example.internal", not_after: new Date(Date.now() + 90 * 86400e3).toISOString(), fingerprint: "22".repeat(32), key_present: true, days_left: 90 },
          ],
        };
      }
      // Faz 10: db.* simülasyonları (şifre TEK SEFERLİK reveal yüzeyine
      // ayrıştırılır — task sonucunda düz şifre TAŞINMAZ, kriter 2).
      if (t.operation === "db.instance.list") {
        t.result = {
          instances: [
            { kind: "postgres", version: "18.2", socket: "/run/postgresql/.s.PGSQL.5432", active: "active" },
            { kind: "mysql", version: "11.4-MariaDB", socket: "/run/mysqld/mysqld.sock", active: "active" },
          ],
        };
      }
      if (t.operation === "db.database.list") {
        const kind = (t.payload && t.payload.kind) || "postgres";
        t.result = {
          databases: kind === "mysql"
            ? [{ name: "shop_db", size: "2.5 MiB" }, { name: "wordpress", size: "48 MiB" }]
            : [{ name: "app_db", size: "17 MB" }, { name: "analytics", size: "1.2 GB" }],
        };
      }
      if (t.operation === "db.user.list") {
        t.result = { users: [{ name: "app_ro", host: "" }, { name: "shop_u", host: "localhost" }] };
      }
      if (t.operation === "db.backup.list") {
        t.result = {
          backups: [
            { file: "app_db-20261102T090507Z.sql", bytes: 2458624, created: "2026-11-02T09:05:09Z" },
          ],
        };
      }
      if (t.operation === "db.database.create") {
        t.result = { kind: t.payload && t.payload.kind, database: t.payload && t.payload.name, created: true };
      }
      if (t.operation === "db.database.delete") {
        t.result = { kind: t.payload && t.payload.kind, database: t.payload && t.payload.database, deleted: true };
      }
      if (t.operation === "db.user.create") {
        const kind = (t.payload && t.payload.kind) || "postgres";
        const name = (t.payload && t.payload.name) || "app_ro";
        const host = (t.payload && t.payload.host) || "";
        const fp = "f1" + Math.random().toString(16).slice(2, 14);
        mockDbCredentials.push({
          id: uuid(),
          node_id: t.node_id,
          kind,
          username: name,
          host,
          fingerprint: fp,
          revealed_at: null,
        });
        t.result = {
          kind,
          user: { name, host },
          credentials: { credential_id: mockDbCredentials[mockDbCredentials.length - 1].id, fingerprint: fp, one_time_reveal: true },
        };
      }
      if (t.operation === "db.user.delete") {
        t.result = { kind: t.payload && t.payload.kind, user: { name: t.payload && t.payload.name, host: (t.payload && t.payload.host) || "" }, deleted: true, credentials_removed: true };
      }
      if (t.operation === "db.grant.set") {
        t.result = {
          kind: t.payload && t.payload.kind,
          user: { name: t.payload && t.payload.user, host: (t.payload && t.payload.host) || "" },
          database: t.payload && t.payload.database,
          level: t.payload && t.payload.level,
          set: true,
        };
      }
      if (t.operation === "db.backup") {
        const db = (t.payload && t.payload.database) || "app_db";
        t.result = {
          kind: t.payload && t.payload.kind,
          database: db,
          file: db + "-20261102T090507Z.sql",
          bytes: 2458624,
          sha256: "b7f2" + "a".repeat(60),
          duration: "1.24s",
        };
      }
      if (t.operation === "db.restore") {
        t.result = {
          kind: t.payload && t.payload.kind,
          database: t.payload && t.payload.database,
          file: t.payload && t.payload.backup,
          bytes: 2458624,
          restored: true,
          duration: "2.02s",
        };
      }
      // Faz 11: mail.* simülasyonları. queue.list alıcı adresi/gövde
      // TAŞIMAZ (kriter 5); preflight kurulum yapmaz, yalnız raporlar
      // (kriter 1); mailbox.create şifre DÖNDÜRMEZ — scrub edilmiş şekil
      // (credential_id + fingerprint, kriter 2).
      if (t.operation === "mail.preflight") {
        t.result = {
          preflight: {
            postfix_installed: true,
            dovecot_installed: true,
            rspamd_installed: false,
            binaries: [
              { name: "postfix", found: true, version: "" },
              { name: "postmap", found: true, version: "" },
              { name: "postqueue", found: true, version: "" },
              { name: "postconf", found: true, version: "" },
              { name: "doveconf", found: true, version: "" },
              { name: "doveadm", found: true, version: "" },
              { name: "rspamadm", found: false, version: "" },
            ],
            ports: [
              { port: 25, occupied: true, state: "occupied" },
              { port: 465, occupied: false, state: "free" },
              { port: 587, occupied: false, state: "free" },
              { port: 143, occupied: false, state: "free" },
              { port: 993, occupied: false, state: "free" },
            ],
            vmail_user: true,
            managed_dir_ready: true,
            postfix_wired: true,
            dovecot_wired: true,
            hostname: "mail-01",
            ptr: { ip: "203.0.113.10", state: "unknown" },
            notes: ["PTR/reverse DNS dış DNS bağımlılığıdır — panel buradan doğrulayamaz (UI'daki deliverability rehberine bakın)"],
          },
        };
      }
      if (t.operation === "mail.status") {
        t.result = {
          status: {
            services: { postfix: "active", dovecot: "active", rspamd: "inactive" },
            queue: { total: mockMailQueue.length, bytes: 2048, deferred: mockMailQueue.length, probe_mode: "postqueue" },
            domains: mockMailDomains.length,
            mailboxes: mockMailMailboxes.length,
            aliases: mockMailAliases.length,
            postfix_wired: true,
            dovecot_wired: true,
          },
        };
      }
      if (t.operation === "mail.domain.list") {
        t.result = { domains: mockMailDomains.map((d) => ({ name: d })) };
      }
      if (t.operation === "mail.domain.create") {
        const d = (t.payload && t.payload.domain) || "";
        if (!mockMailDomains.includes(d)) mockMailDomains.push(d);
        t.result = { result: { applied: true, reloaded: true }, domain: d };
      }
      if (t.operation === "mail.domain.delete") {
        const d = (t.payload && t.payload.domain) || "";
        const idx = mockMailDomains.indexOf(d);
        if (idx >= 0) mockMailDomains.splice(idx, 1);
        t.result = { result: { applied: true, reloaded: true }, domain: d };
      }
      if (t.operation === "mail.mailbox.list") {
        const d = (t.payload && t.payload.domain) || "";
        t.result = {
          mailboxes: mockMailMailboxes
            .filter((m) => !d || m.domain === d)
            .map((m) => ({ address: m.local + "@" + m.domain, maildir: "/var/vmail/" + m.domain + "/" + m.local + "/Maildir", quota_bytes: m.quota_bytes })),
        };
      }
      if (t.operation === "mail.mailbox.create") {
        const domain = (t.payload && t.payload.domain) || "";
        const local = (t.payload && t.payload.local) || "";
        const fp = "m1" + Math.random().toString(16).slice(2, 14);
        const address = local + "@" + domain;
        if (!mockMailMailboxes.some((m) => m.local + "@" + m.domain === address)) {
          mockMailMailboxes.push({ domain, local, quota_bytes: 1024 ** 3 });
          mockDbCredentials.push({
            id: uuid(),
            node_id: t.node_id,
            kind: "mailbox",
            username: address,
            host: "",
            fingerprint: fp,
            revealed_at: null,
          });
        }
        t.result = {
          applied: true,
          reloaded: true,
          domain,
          mailbox: { address, maildir: "/var/vmail/" + domain + "/" + local + "/Maildir", quota_bytes: 1024 ** 3 },
          credentials: { credential_id: mockDbCredentials[mockDbCredentials.length - 1].id, fingerprint: fp, one_time_reveal: true },
        };
      }
      if (t.operation === "mail.mailbox.delete") {
        const address = (t.payload && t.payload.mailbox) || "";
        const at = address.indexOf("@");
        if (at > 0) {
          const local = address.slice(0, at);
          const domain = address.slice(at + 1);
          const idx = mockMailMailboxes.findIndex((m) => m.local === local && m.domain === domain);
          if (idx >= 0) mockMailMailboxes.splice(idx, 1);
        }
        t.result = { applied: true, reloaded: true, mailbox: { address }, maildir_removed: true, credentials_removed: true };
      }
      if (t.operation === "mail.alias.list") {
        const d = (t.payload && t.payload.domain) || "";
        t.result = { aliases: mockMailAliases.filter((a) => !d || a.alias.endsWith("@" + d)) };
      }
      if (t.operation === "mail.alias.create") {
        const alias = (t.payload && t.payload.alias) || "";
        const target = (t.payload && t.payload.target) || "";
        if (!mockMailAliases.some((a) => a.alias === alias)) mockMailAliases.push({ alias, target });
        t.result = { result: { applied: true, reloaded: true }, alias };
      }
      if (t.operation === "mail.alias.delete") {
        const alias = (t.payload && t.payload.alias) || "";
        const idx = mockMailAliases.findIndex((a) => a.alias === alias);
        if (idx >= 0) mockMailAliases.splice(idx, 1);
        t.result = { result: { applied: true, reloaded: true }, alias };
      }
      if (t.operation === "mail.quota.set") {
        const mailbox = (t.payload && t.payload.mailbox) || "";
        const bytes = Number((t.payload && t.payload.bytes) || 0);
        const at = mailbox.indexOf("@");
        if (at > 0) {
          const m = mockMailMailboxes.find(
            (mm) => mm.local === mailbox.slice(0, at) && mm.domain === mailbox.slice(at + 1),
          );
          if (m && bytes > 0) m.quota_bytes = bytes;
        }
        t.result = { result: { applied: true, reloaded: true }, mailbox };
      }
      if (t.operation === "mail.config.check") {
        t.result = {
          config_check: {
            postfix: { state: "pass" },
            dovecot: { state: "pass" },
            rspamd: { state: "absent", detail: "rspamadm kurulu değil" },
          },
        };
      }
      if (t.operation === "mail.queue.list") {
        t.result = { queue: mockMailQueue };
      }
      if (t.operation === "mail.limits.get") {
        t.result = {
          limits: {
            message_size_limit: "10240000",
            mailbox_size_limit: "10240000",
            smtpd_client_message_rate_limit: "0",
            smtpd_client_recipient_rate_limit: "0",
            smtpd_client_connection_count_limit: "0",
            anvil_rate_time_unit: "60",
          },
        };
      }
      if (t.operation === "mail.limits.set") {
        t.result = { result: { applied: true, reloaded: true }, keys: Object.keys((t.payload && t.payload.limits) || {}) };
      }
      // Faz 12: vm.* simülasyonları. preflight kurulum yapmaz (kriter 1);
      // create/disk.add payload'da yol TAŞIMAZ (kriter 3); disk.list gerçek
      // format doğrulaması ve yabancı disk bilgisini taşır (kriter 4).
      if (t.operation === "vm.preflight") {
        t.result = {
          preflight: {
            kvm_available: true,
            cpu_model: "Demo Xeon 8375C",
            cpu_flags: ["fpu", "vmx", "sse4_2", "avx2"],
            virt_flag: "vmx",
            mem_total_mib: 32768,
            mem_available_mib: 24576,
            libvirt_version: "10.0.0",
            hypervisor: "QEMU 8.2.0",
            uri: "qemu:///system",
            qemu_img: true,
            pools: [
              { name: "default", type: "dir", active: true, capacity_bytes: 536870912000, free_bytes: 407686309888, target_path: "/var/lib/libvirt/images" },
            ],
            bridges: ["br0"],
            networks: ["default"],
            notes: [],
          },
        };
      }
      if (t.operation === "vm.status") {
        t.result = {
          status: {
            uri: "qemu:///system",
            libvirt_version: "10.0.0",
            hypervisor: "QEMU 8.2.0",
            domains_total: mockVMs.length,
            running: mockVMs.filter((v) => v.running).length,
            kvm_available: true,
          },
        };
      }
      if (t.operation === "vm.list") {
        t.result = { vms: mockVMs.map((v) => ({ name: v.name, state: v.state, running: v.running })) };
      }
      if (t.operation === "vm.info") {
        const name = (t.payload && t.payload.name) || "";
        const vm = mockVMs.find((v) => v.name === name);
        if (!vm) {
          t.status = "failed";
          t.error_category = "executor_error";
          t.result = null;
        } else {
          t.result = {
            vm: {
              name: vm.name,
              uuid: "aaaa1111-2222-4333-8444-555555555555",
              state: vm.state,
              running: vm.running,
              vcpu: vm.vcpu,
              memory_mib: vm.mem,
              arch: "x86_64",
              guest_agent: false,
              disks: [
                {
                  target_dev: "vda",
                  pool: vm.pool,
                  volume: vm.volume,
                  managed: true,
                  xml_format: "qcow2",
                  actual_format: "qcow2",
                  format_verified: true,
                  virtual_bytes: vm.disk * 1024 ** 3,
                  actual_bytes: 2097152,
                  check_ok: true,
                },
              ],
              interfaces: [{ mac: "52:54:00:ab:cd:01", bridge: vm.bridge, model: "virtio" }],
              console: { graphics_type: "vnc", listen: "127.0.0.1", port: 5900, local_only: true, serial: true },
            },
          };
        }
      }
      if (t.operation === "vm.create") {
        const p = t.payload || {};
        const name = p.name || "";
        if (mockVMs.some((v) => v.name === name)) {
          t.status = "failed";
          t.error_category = "executor_error";
          t.result = null;
        } else {
          const vm = {
            name,
            state: "shut off",
            running: false,
            vcpu: p.vcpu || 1,
            mem: p.mem_mib || 512,
            disk: p.disk_gib || 20,
            pool: p.pool || "default",
            bridge: p.bridge || "default",
            volume: "sc-" + name + "-root",
          };
          mockVMs.push(vm);
          t.result = {
            result: {
              name: vm.name,
              uuid: "bbbb2222-3333-4444-8555-666666666666",
              vcpu: vm.vcpu,
              memory_mib: vm.mem,
              disk_gib: vm.disk,
              pool: vm.pool,
              volume: vm.volume,
              bridge: vm.bridge,
              bridge_kind: "bridge",
              accelerator: "kvm",
              state: vm.state,
            },
          };
        }
      }
      if (t.operation === "vm.clone") {
        const p = t.payload || {};
        const src0 = mockVMs.find((v) => v.name === p.source);
        if (!src0) {
          t.status = "failed";
          t.error_category = "executor_error";
          t.result = null;
        } else {
          const vm = { ...src0, name: p.name, volume: "sc-" + p.name + "-vda", state: "shut off", running: false };
          mockVMs.push(vm);
          t.result = { result: { source: p.source, name: p.name, uuid: "cccc3333-4444-4555-8666-777777777777", volume: vm.volume } };
        }
      }
      if (t.operation === "vm.start" || t.operation === "vm.stop" || t.operation === "vm.reboot") {
        const name = (t.payload && t.payload.name) || "";
        const vm = mockVMs.find((v) => v.name === name);
        if (!vm) {
          t.status = "failed";
          t.error_category = "executor_error";
          t.result = null;
        } else {
          if (t.operation === "vm.start") {
            vm.running = true;
            vm.state = "running";
          } else if (t.operation === "vm.stop") {
            vm.running = false;
            vm.state = "shut off";
          }
          t.result = {
            result: {
              name: vm.name,
              action: t.operation === "vm.start" ? "start" : t.operation === "vm.stop" ? "stop" : "reboot",
              already: false,
              forced: t.operation === "vm.stop" && !!(t.payload && t.payload.force),
              state: vm.state,
            },
          };
        }
      }
      if (t.operation === "vm.delete") {
        const name = (t.payload && t.payload.name) || "";
        const idx = mockVMs.findIndex((v) => v.name === name);
        if (idx < 0) {
          t.status = "failed";
          t.error_category = "executor_error";
          t.result = null;
        } else {
          const vm = mockVMs[idx];
          mockVMs.splice(idx, 1);
          t.result = { result: { name: vm.name, undefined: true, volumes_removed: [vm.pool + "/" + vm.volume] } };
        }
      }
      if (t.operation === "vm.disk.list") {
        const name = (t.payload && t.payload.name) || "";
        const vm = mockVMs.find((v) => v.name === name);
        if (!vm) {
          t.status = "failed";
          t.error_category = "executor_error";
          t.result = null;
        } else {
          t.result = {
            disks: [
              {
                target_dev: "vda",
                pool: vm.pool,
                volume: vm.volume,
                managed: true,
                xml_format: "qcow2",
                actual_format: "qcow2",
                format_verified: true,
                virtual_bytes: vm.disk * 1024 ** 3,
                actual_bytes: 2097152,
                check_ok: true,
              },
            ],
          };
        }
      }
      if (t.operation === "vm.disk.add") {
        const p = t.payload || {};
        const vm = mockVMs.find((v) => v.name === p.name);
        if (!vm) {
          t.status = "failed";
          t.error_category = "executor_error";
          t.result = null;
        } else {
          t.result = { result: { name: vm.name, pool: p.pool || "default", volume: "sc-" + vm.name + "-vdb", target_dev: "vdb", size_gib: p.size_gib || 10 } };
        }
      }
      if (t.operation === "vm.snapshot.list") {
        const name = (t.payload && t.payload.name) || "";
        t.result = { snapshots: mockVMSnapshots[name] || [] };
      }
      if (t.operation === "vm.snapshot.create") {
        const p = t.payload || {};
        if (!mockVMSnapshots[p.name]) mockVMSnapshots[p.name] = [];
        mockVMSnapshots[p.name].push({ name: p.snapshot, current: true });
        t.result = { result: { name: p.snapshot, domain: p.name, verified_formats: ["vda=qcow2"] } };
      }
      if (t.operation === "vm.snapshot.revert" || t.operation === "vm.snapshot.delete") {
        const p = t.payload || {};
        const list = mockVMSnapshots[p.name] || [];
        const idx = list.findIndex((s) => s.name === p.snapshot);
        if (idx < 0) {
          t.status = "failed";
          t.error_category = "executor_error";
          t.result = null;
        } else {
          if (t.operation === "vm.snapshot.delete") list.splice(idx, 1);
          t.result = { result: { name: p.name, action: t.operation === "vm.snapshot.revert" ? "snapshot-revert" : "snapshot-delete", state: p.snapshot } };
        }
      }
      // Faz 10: fs.* simülasyonları (kök = site docroot; symlink'ler listede
      // görünür ama işlem kabul etmez — broker O_NOFOLLOW disiplini).
      if (t.operation === "fs.list") {
        t.result = {
          listing: {
            root: (t.payload && t.payload.root) || "blog",
            path: (t.payload && t.payload.path) || "",
            truncated: false,
            entries: [
              { name: "index.html", type: "file", size: 1024, mode: "0644", mod_time: "2026-11-02T09:00:00Z" },
              { name: "uploads", type: "dir", size: 4096, mode: "0750", mod_time: "2026-11-02T08:30:00Z" },
              { name: "logo.svg", type: "file", size: 15360, mode: "0644", mod_time: "2026-11-01T17:12:00Z" },
            ],
          },
        };
      }
      if (t.operation === "fs.download") {
        t.result = {
          file: {
            path: (t.payload && t.payload.path) || "index.html",
            size: 11,
            mode: "0644",
            sha256: "c1a0" + "3".repeat(60),
            mime: "text/plain; charset=utf-8",
            content_b64: Buffer.from("hello world").toString("base64"),
          },
        };
      }
      if (t.operation === "fs.upload") {
        t.result = {
          result: {
            path: t.payload && t.payload.path,
            bytes: Math.ceil(((t.payload && t.payload.content_b64) || "").length * 0.75),
            mode: (t.payload && t.payload.mode) || "0644",
            mime: "text/plain; charset=utf-8",
            created: !(t.payload && t.payload.overwrite),
            overwritten: !!(t.payload && t.payload.overwrite),
          },
        };
      }
      if (t.operation === "fs.chmod") {
        t.result = {
          result: {
            path: t.payload && t.payload.path,
            old_mode: "0644",
            new_mode: (t.payload && t.payload.mode) || "0600",
            type: "file",
          },
        };
      }
      if (t.operation === "fs.chown") {
        t.result = {
          result: {
            path: t.payload && t.payload.path,
            uid: 997,
            gid: 997,
            user: "sc-web-" + ((t.payload && t.payload.root) || "blog"),
            old_uid: 0,
            old_gid: 0,
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
    dnsProviders.length = 0;
    mockDbCredentials.length = 0;
        json(res, 200, { status: "reset", mock: true });
    return;
  }

  // ---- Faz 10: db-credentials (tek-seferlik reveal mock'u) ----
  const credsMatch = /^\/api\/v1\/nodes\/([^/]+)\/db-credentials$/.exec(path);
  if (method === "GET" && credsMatch) {
    json(res, 200, { credentials: mockDbCredentials.filter((c) => c.node_id === credsMatch[1]) });
    return;
  }
  const revealMatch = /^\/api\/v1\/db-credentials\/([^/]+)\/reveal$/.exec(path);
  if (method === "POST" && revealMatch) {
    const cred = mockDbCredentials.find((c) => c.id === revealMatch[1]);
    if (!cred) {
      json(res, 404, errorBody("not_found", "Kimlik kaydı bulunamadı.", 404));
      return;
    }
    if (cred.revealed_at) {
      json(res, 409, errorBody("already_revealed", "Şifre daha önce bir kez gösterildi; tekrar gösterilemez.", 409));
      return;
    }
    cred.revealed_at = new Date().toISOString();
    json(res, 200, {
      credential: { id: cred.id, node_id: cred.node_id, kind: cred.kind, username: cred.username, host: cred.host, fingerprint: cred.fingerprint, revealed_at: cred.revealed_at },
      password: "MockDemo-" + cred.fingerprint.slice(0, 8) + "-x7K9",
      one_time: true,
    });
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
    } else if (String(body.display_name || "").startsWith("dbfs-") || body.os_family === "dbfs") {
      attachDbFsTelemetry(node);
    } else if (String(body.display_name || "").startsWith("mail-") || body.os_family === "mail") {
      attachMailTelemetry(node);
    } else if (String(body.display_name || "").startsWith("vm-") || body.os_family === "vm") {
      attachVmTelemetry(node);
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
  // ---- Faz 09: DNS sağlayıcı kayıtları (kimlik bilgisi ASLA geri verilmez) ----
  if (method === "GET" && path === "/api/v1/dns-providers") {
    if (!session) { json(res, 401, errorBody("unauthorized", "Oturum gerekli.", 401)); return; }
    json(res, 200, { providers: dnsProviders.map(({ credentials_encrypted, ...pub }) => pub) });
    return;
  }
  if (method === "POST" && path === "/api/v1/dns-providers") {
    if (!session) { json(res, 401, errorBody("unauthorized", "Oturum gerekli.", 401)); return; }
    const body = await readBody();
    if (!body.name || !body.kind || !body.credentials || !body.credentials.api_key) {
      json(res, 400, errorBody("invalid_request", "name/kind/credentials zorunlu.", 400));
      return;
    }
    if (body.kind !== "mock") {
      json(res, 400, errorBody("invalid_request", "Sağlayıcı türü desteklenmiyor (desteklenen: mock).", 400));
      return;
    }
    const row = {
      id: uuid(),
      name: String(body.name),
      kind: "mock",
      capabilities: { record_types: ["A", "AAAA", "CNAME", "TXT", "MX"], ttl_min: 60, ttl_max: 86400, supports_dns01: true, wildcard: true, note: "yerleşik mock sağlayıcı (in-memory)" },
      credential_keys: ["api_key"],
      credentials_encrypted: Buffer.from(JSON.stringify(body.credentials)).toString("base64"),
      has_credentials: true,
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
    };
    dnsProviders.push(row);
    logAudit(actor, "dns.provider.create", "dns_provider", row.id, "success");
    const { credentials_encrypted, ...pub } = row;
    json(res, 201, pub);
    return;
  }
  const dnsDelMatch = /^\/api\/v1\/dns-providers\/([^/]+)$/.exec(path);
  if (method === "DELETE" && dnsDelMatch) {
    if (!session) { json(res, 401, errorBody("unauthorized", "Oturum gerekli.", 401)); return; }
    const idx = dnsProviders.findIndex((p) => p.id === dnsDelMatch[1]);
    if (idx < 0) { json(res, 404, errorBody("not_found", "Kayıt bulunamadı.", 404)); return; }
    dnsProviders.splice(idx, 1);
    logAudit(actor, "dns.provider.delete", "dns_provider", dnsDelMatch[1], "success");
    json(res, 200, { deleted: true });
    return;
  }

  // ---- Faz 09: SSL sertifika durumu ----
  if (method === "GET" && path === "/api/v1/certificates") {
    if (!session) { json(res, 401, errorBody("unauthorized", "Oturum gerekli.", 401)); return; }
    json(res, 200, { certificates });
    return;
  }
  const certGetMatch = /^\/api\/v1\/certificates\/([^/]+)$/.exec(path);
  if (method === "GET" && certGetMatch) {
    if (!session) { json(res, 401, errorBody("unauthorized", "Oturum gerekli.", 401)); return; }
    const rec = certificates.find((c) => c.id === certGetMatch[1]);
    if (!rec) { json(res, 404, errorBody("not_found", "Kayıt bulunamadı.", 404)); return; }
    json(res, 200, rec);
    return;
  }

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
