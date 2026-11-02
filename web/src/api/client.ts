// Tipled API istemcisi: tek fetch sarmalayıcısı; hata zarfı, CSRF başlığı ve
// 401 yayılımı burada toplanır (docs/architecture/api-and-protocol.md).

export interface ApiError {
  code: string;
  message: string;
  correlation_id: string;
  retryable: boolean;
  status: number;
}

/** Modül seviyesinde CSRF token'ı; AuthProvider güncel tutar. */
let csrfToken = "";

export function setCsrfToken(token: string): void {
  csrfToken = token;
}

/** 401 bildirimi: AuthProvider oturumu anon durumuna çeker. */
let onUnauthorized: (() => void) | null = null;

export function setUnauthorizedHandler(fn: (() => void) | null): void {
  onUnauthorized = fn;
}

const MUTATING = new Set(["POST", "PUT", "PATCH", "DELETE"]);

export async function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers);
  if (init?.body && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  if (init?.method && MUTATING.has(init.method) && csrfToken) {
    headers.set("X-CSRF-Token", csrfToken);
  }

  const res = await fetch(path, { credentials: "same-origin", ...init, headers });

  if (res.status === 401) {
    onUnauthorized?.();
  }

  const text = await res.text();
  let body: unknown = null;
  if (text) {
    try {
      body = JSON.parse(text);
    } catch {
      body = null;
    }
  }

  if (!res.ok) {
    const err = (body ?? {}) as Partial<ApiError>;
    const apiError: ApiError = {
      code: err.code ?? "unknown",
      message: err.message ?? `İstek başarısız (${res.status})`,
      correlation_id: err.correlation_id ?? "",
      retryable: err.retryable ?? false,
      status: res.status,
    };
    throw apiError;
  }
  return body as T;
}

export function isApiError(e: unknown): e is ApiError {
  return typeof e === "object" && e !== null && "code" in e && "status" in e;
}

// ---- Paylaşılan tipler ----

export interface SessionUser {
  id: string;
  username: string;
}

export interface LoginResponse {
  user: SessionUser;
  csrf_token: string;
  expires_at: string;
}

export interface SessionResponse {
  user_id: string;
  username: string;
  session_id: string;
  server_time: string;
}

export interface NodeRecord {
  id: string;
  display_name: string;
  hostname?: string | null;
  os_family: "linux" | "windows" | "unknown";
  os_name?: string | null;
  os_version?: string | null;
  arch?: string | null;
  agent_version?: string | null;
  status: "pending" | "active" | "offline" | "retired";
  tags: string[];
  group_ids: string[];
  created_at: string;
  updated_at: string;
  last_seen_at?: string | null;
}

export interface NodePage {
  nodes: NodeRecord[];
  next_cursor?: string;
  has_more: boolean;
}

export interface GroupRecord {
  id: string;
  name: string;
  description: string;
  created_at: string;
}

// Faz 04: agent'ın raporladığı sistem bilgileri ve metrik anlık görüntüsü.
export interface SystemFactsRecord {
  node_id: string;
  os_id: string;
  os_version_id: string;
  os_pretty_name: string;
  os_home_url: string;
  kernel_release: string;
  arch: string;
  systemd_version: string;
  package_manager: string;
  systemd_available: boolean;
  distro_family: string; // Faz 06
  distro_like: string; // Faz 06
  support_state: string; // Faz 06: supported|untested|unknown
  support_reason: string; // Faz 06
  source: string;
  collected_at: string;
}

export interface MetricsSnapshot {
  collected_at: string;
  source: string;
  cpu: {
    percent: number;
    logical_cores: number;
    load_1m: number;
    load_5m: number;
    load_15m: number;
  };
  memory: {
    total_bytes: number;
    available_bytes: number;
    used_bytes: number;
    used_percent: number;
  };
  disks: Array<{
    device: string;
    mount_point: string;
    fs_type: string;
    total_bytes: number;
    used_bytes: number;
    available_bytes: number;
  }>;
  networks: Array<{
    interface: string;
    rx_bytes: number;
    tx_bytes: number;
  }>;
  uptime_seconds: number;
}

export interface CapabilityRecord {
  node_id?: string;
  capability_id: string;
  version: string;
  available: boolean;
  reason_unavailable?: string | null;
  adapter_id?: string | null;
  adapter_version?: string | null;
}

export interface TaskRecord {
  id: string;
  operation: string;
  operation_version: string;
  node_id?: string | null;
  payload?: unknown;
  status:
    | "queued"
    | "dispatched"
    | "accepted"
    | "running"
    | "succeeded"
    | "failed"
    | "cancelled"
    | "expired"
    | "unknown"
    | "partial";
  actor: string;
  correlation_id: string;
  idempotency_key?: string | null;
  result?: unknown;
  error_category?: string | null;
  attempts: number;
  max_attempts: number;
  retry_safe: boolean;
  created_at: string;
  updated_at: string;
}

export interface TaskPage {
  tasks: TaskRecord[];
  next_cursor?: string;
  has_more: boolean;
}

export interface OperationInfo {
  name: string;
  version: string;
  class: string;
  retry_safe: boolean;
  timeout_seconds: number;
}

export interface AuditEntry {
  id: number;
  actor: string;
  actor_id?: string;
  action: string;
  target_type: string;
  target_id: string;
  result: "success" | "failure" | "denied";
  correlation_id: string;
  metadata?: Record<string, unknown>;
  created_at: string;
}

export interface AuditPage {
  entries: AuditEntry[];
  next_cursor?: string;
  has_more: boolean;
}

// ---- Faz 13: izleme, log arama, alarm ve güvenlik tipleri ----

export interface AlertRule {
  id: string;
  name: string;
  node_id: string;
  metric: "cpu" | "memory" | "disk" | "metrics_freshness";
  dimension: string;
  op: "gt" | "lt";
  threshold: number;
  duration_seconds: number;
  severity: "info" | "warning" | "critical";
  enabled: boolean;
  created_at: string;
}

export type AlertStatus = "open" | "acknowledged" | "silenced" | "resolved";

export interface Alert {
  id: string;
  rule_id: string;
  node_id: string;
  dimension: string;
  fingerprint: string;
  status: AlertStatus;
  severity: "info" | "warning" | "critical";
  value: number;
  occurrences: number;
  title: string;
  detail: string;
  first_seen_at: string;
  last_seen_at: string;
  acknowledged_at: string | null;
  silenced_until: string | null;
  resolved_at: string | null;
}

export interface SecurityControl {
  id: string;
  state: "ok" | "warn" | "alert" | "unknown";
  evidence: string;
  checked_at: string;
}

export interface SecurityReport {
  generated_at: string;
  controls: SecurityControl[];
}

export interface LogEntry {
  timestamp: string;
  severity: number;
  severity_name: string;
  unit?: string;
  identifier?: string;
  message: string;
}

export interface LogSearchResult {
  entries: LogEntry[];
  source: string;
}

// ---- Faz 14: yedekleme + bulut sağlayıcı tipleri ----

export type BackupScope = "control_plane" | "node";

export interface BackupRecord {
  id: string;
  scope: BackupScope;
  node_id?: string;
  kind: string;
  status: "running" | "success" | "failed";
  verify_state: "unverified" | "verified" | "failed";
  verify_at?: string | null;
  verify_detail?: string;
  size_bytes: number;
  sha256?: string;
  snapshot_id?: string;
  storage_path?: string;
  task_id?: string;
  detail?: string;
  created_at: string;
  updated_at: string;
}

export interface RestoreDrill {
  id: string;
  backup_id: string;
  node_id?: string;
  target: string;
  status: "running" | "success" | "failed";
  detail?: string;
  tables_restored: number;
  rows_restored: number;
  task_id?: string;
  actor?: string;
  created_at: string;
}

// Faz 15: eklenti sözleşmesi (ADR-0019) ve imzalı güncelleme kayıt defteri
// (ADR-0020) UI tipleri.
export interface ExtensionPermRequest {
  action: string;
  risk: "low" | "medium" | "high";
  reason: string;
}

export interface ExtensionGrant {
  action: string;
  risk: "low" | "medium" | "high";
  granted_by: string;
  granted_at: string;
  revoked_at?: string | null;
}

export interface ExtensionRow {
  id: string;
  name: string;
  display_name: string;
  vendor: string;
  version: string;
  api_version: string;
  min_cp_version: string;
  artifact_sha256: string;
  state: "pending" | "approved" | "revoked";
  manifest?: unknown;
  requested_permissions: ExtensionPermRequest[];
  granted_permissions: ExtensionGrant[];
  delegated_ops: string[];
  capabilities: string[];
  api_compatible: boolean;
  registered_by: string;
  registered_at: string;
  updated_at: string;
}

export interface UpdateArtifact {
  id: string;
  target: "agent_linux_amd64" | "agent_windows_amd64" | "control_plane";
  version: string;
  file_name: string;
  size_bytes: number;
  sha256: string;
  signature: string;
  min_prev?: string;
  api_version: string;
  notes?: string;
  state: "published" | "revoked";
  manifest?: unknown;
  published_by: string;
  published_at: string;
}

export interface CloudProvider {
  id: string;
  name: string;
  kind: "s3";
  endpoint: string;
  region: string;
  credential_keys: string[];
  created_at: string;
  updated_at: string;
}

export interface CloudResource {
  name: string;
  kind: string;
  extra?: Record<string, string>;
}

export interface CloudMatrixEntry {
  action: "list" | "read" | "delete";
  provider_permission: string;
  risk: string;
  confirm_required: boolean;
}

export interface CloudProbe {
  action: string;
  ok: boolean;
  reason?: string;
}

export interface CloudVerifyReport {
  provider: string;
  endpoint: string;
  region: string;
  matrix: { provider: string; entries: CloudMatrixEntry[] };
  probes: CloudProbe[];
  checked_at: string;
}
