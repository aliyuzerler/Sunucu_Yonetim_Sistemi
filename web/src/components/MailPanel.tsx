import { useEffect, useState } from "react";
import { apiFetch, isApiError } from "../api/client";
import { CopyButton } from "./CopyButton";

// Faz 11: E-posta servisi paneli (NodeDetail bölümü).
//
// Güvenlik notları (ADR-0015):
//   - Aile node'da --mail-enable ile opt-in'dir; panel preflight'ı
//     RAPORLAR, kurulum teklif ETMEZ (kriter 1).
//   - mail.mailbox.create şifresi node'da üretilir; control-plane seal
//     eder ve TEK SEFERLİK reveal uç noktası üzerinden gösterir. Şifre bu
//     bileşende yalnız reveal yanıtında tutulur, state'te saklanmaz.
//   - mail.queue.list alıcı adreslerini ve gövdeyi ASLA göstermez
//     (kriter 5); tabloda yalnız kuyruk kimliği/boyut/yaş/gönderici/neden.
//   - Deliverability kartı SPF/DKIM/DMARC/PTR gereksinimlerini AÇIKLAR;
//     bu kayıtlar dış DNS'te tutulur (kriter 3) — panel DNS yönetmez.

export interface TaskLike {
  id: string;
  status: string;
  result?: unknown;
  error_category?: string;
}

interface BinStatus { name: string; found: boolean; version?: string }
interface PortStatus { port: number; occupied: boolean; state: string }
interface PreflightReport {
  postfix_installed: boolean;
  dovecot_installed: boolean;
  rspamd_installed: boolean;
  binaries?: BinStatus[];
  ports?: PortStatus[];
  vmail_user: boolean;
  managed_dir_ready: boolean;
  postfix_wired: boolean;
  dovecot_wired: boolean;
  hostname: string;
  ptr?: { ip?: string; names?: string[]; state: string };
  notes?: string[];
}
interface StatusReport {
  services?: Record<string, string>;
  queue?: { total: number; bytes: number; deferred: number; probe_mode: string };
  domains: number;
  mailboxes: number;
  aliases: number;
  postfix_wired: boolean;
  dovecot_wired: boolean;
}
interface DomainRow { name: string }
interface MailboxRow { address: string; maildir: string; quota_bytes: number }
interface AliasRow { alias: string; target: string }
interface QueueRow {
  queue_id: string;
  queue_name: string;
  size_bytes: number;
  age_seconds: number;
  sender?: string;
  reason?: string;
}
interface ConfigCheckReport {
  postfix?: { state: string; detail?: string };
  dovecot?: { state: string; detail?: string };
  rspamd?: { state: string; detail?: string };
}
interface MailboxCreateReport {
  applied?: boolean;
  reloaded?: boolean;
  mailbox?: { address?: string; maildir?: string; quota_bytes?: number };
  credentials?: {
    credential_id?: string;
    fingerprint?: string;
    one_time_reveal?: boolean;
    password_redacted?: string;
    redaction_reason?: string;
  };
}
interface CredentialRow {
  id: string;
  kind: string;
  username: string;
  fingerprint: string;
  revealed_at?: string | null;
}

function bytesLabel(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i += 1;
  }
  return `${v.toFixed(v >= 10 || i === 0 ? 0 : 1)} ${units[i]}`;
}

function ageLabel(seconds: number): string {
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h`;
  return `${Math.floor(seconds / 86400)}d`;
}

const inputCls =
  "rounded-lg border border-border bg-surface px-3 py-1.5 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent";
const btnCls =
  "rounded-lg border border-border bg-surface px-3 py-1.5 text-xs font-medium text-ink hover:bg-neutral-soft disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent";
const btnDangerCls =
  "rounded-lg border border-red-300 bg-surface px-3 py-1.5 text-xs font-medium text-red-700 hover:bg-red-50 disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent";

const LIMIT_KEYS = [
  "message_size_limit",
  "mailbox_size_limit",
  "smtpd_client_message_rate_limit",
  "smtpd_client_recipient_rate_limit",
  "smtpd_client_connection_count_limit",
  "anvil_rate_time_unit",
] as const;

export function MailPanel(props: {
  nodeId: string;
  submitTask: (op: string, payload: Record<string, unknown>) => Promise<TaskLike | null>;
  t: (k: string) => string;
}) {
  const { nodeId, submitTask, t } = props;

  const [pending, setPending] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [report, setReport] = useState<Record<string, unknown> | null>(null);
  const [taskId, setTaskId] = useState<string | null>(null);
  const [taskOp, setTaskOp] = useState<string>("");

  // form state
  const [pfDomain, setPfDomain] = useState("");
  const [mbDomain, setMbDomain] = useState("");
  const [mbLocal, setMbLocal] = useState("");
  const [mbQuotaAddr, setMbQuotaAddr] = useState("");
  const [mbQuotaMB, setMbQuotaMB] = useState("1024");
  const [alAliasDomain, setAlAliasDomain] = useState("");
  const [alAliasLocal, setAlAliasLocal] = useState("");
  const [alTarget, setAlTarget] = useState("");
  const [limitValues, setLimitValues] = useState<Record<string, string>>({});

  const [preflight, setPreflight] = useState<PreflightReport | null>(null);
  const [status, setStatus] = useState<StatusReport | null>(null);
  const [domains, setDomains] = useState<DomainRow[]>([]);
  const [mailboxes, setMailboxes] = useState<MailboxRow[]>([]);
  const [aliases, setAliases] = useState<AliasRow[]>([]);
  const [queue, setQueue] = useState<QueueRow[]>([]);
  const [configCheck, setConfigCheck] = useState<ConfigCheckReport | null>(null);
  const [limits, setLimits] = useState<Record<string, string>>({});
  const [mailboxCreated, setMailboxCreated] = useState<MailboxCreateReport | null>(null);
  const [revealed, setRevealed] = useState<{ id: string; password: string } | null>(null);

  // Mailbox şifreleri (kind="mailbox") — mevcut db-credentials ucundan.
  const [creds, setCreds] = useState<CredentialRow[]>([]);
  const loadCreds = () => {
    apiFetch<{ credentials: CredentialRow[] }>(
      `/api/v1/nodes/${encodeURIComponent(nodeId)}/db-credentials`,
    )
      .then((res) => setCreds((res.credentials ?? []).filter((c) => c.kind === "mailbox")))
      .catch(() => setCreds([]));
  };
  useEffect(loadCreds, [nodeId]);

  async function run(op: string, payload: Record<string, unknown>) {
    setError(null);
    setReport(null);
    setPending(op);
    const created = await submitTask(op, payload);
    setPending(null);
    if (!created) return;
    setTaskId(created.id);
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
          if (taskOp === "mail.preflight" && result) {
            setPreflight(result.preflight as PreflightReport);
          } else if (taskOp === "mail.status" && result) {
            setStatus(result.status as StatusReport);
          } else if (taskOp === "mail.domain.list" && result) {
            setDomains((result.domains as DomainRow[]) ?? []);
          } else if (taskOp === "mail.mailbox.list" && result) {
            setMailboxes((result.mailboxes as MailboxRow[]) ?? []);
          } else if (taskOp === "mail.alias.list" && result) {
            setAliases((result.aliases as AliasRow[]) ?? []);
          } else if (taskOp === "mail.queue.list" && result) {
            setQueue((result.queue as QueueRow[]) ?? []);
          } else if (taskOp === "mail.config.check" && result) {
            setConfigCheck(result.config_check as ConfigCheckReport);
          } else if (taskOp === "mail.limits.get" && result) {
            setLimits((result.limits as Record<string, string>) ?? {});
          } else if (taskOp === "mail.mailbox.create" && result) {
            setMailboxCreated(result as MailboxCreateReport);
            loadCreds();
          } else if (
            (taskOp === "mail.mailbox.delete" || taskOp === "mail.domain.create" ||
              taskOp === "mail.domain.delete") && result
          ) {
            setMailboxCreated(null);
          }
          return;
        }
        if (task.status === "failed" || task.status === "cancelled" || task.status === "expired") {
          setError(task.error_category ?? t("common.error.generic"));
          return;
        }
        if (attempts < 15) window.setTimeout(() => void tick(), 1000);
      } catch {
        if (!cancelled && attempts < 15) window.setTimeout(() => void tick(), 1000);
      }
    };
    void tick();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [taskId]);

  function guardedRun(op: string, payload: Record<string, unknown>) {
    setTaskOp(op);
    void run(op, payload);
  }

  // ---- reveal (tek seferlik, db-credentials ucunu paylaşır) ----
  async function reveal(id: string) {
    setError(null);
    try {
      const res = await apiFetch<{ password: string }>(
        `/api/v1/db-credentials/${encodeURIComponent(id)}/reveal`,
        { method: "POST" },
      );
      setRevealed({ id, password: res.password });
      loadCreds();
    } catch (err) {
      setError(isApiError(err) ? err.message : t("common.error.generic"));
    }
  }

  const sectionCls = "rounded-xl border border-border bg-panel p-4 shadow-sm";
  const titleCls = "text-sm font-semibold text-ink";
  const noteCls = "mt-1 text-[11px] text-ink-soft";

  return (
    <section aria-label={t("nodedetail.mail.title")} className={sectionCls} data-testid="mail-panel">
      <h3 className={titleCls}>{t("nodedetail.mail.title")}</h3>
      <p className={noteCls}>{t("nodedetail.mail.note")}</p>
      {error && (
        <p role="alert" className="mt-2 text-xs text-err">
          {error}
        </p>
      )}

      {/* Preflight (kriter 1): kurulum YAPMAZ, yalnız rapor verir */}
      <div className="mt-3 rounded-lg border border-border bg-surface p-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h4 className="text-xs font-semibold text-ink">{t("nodedetail.mail.preflight.title")}</h4>
          <button
            type="button"
            disabled={pending !== null}
            onClick={() => guardedRun("mail.preflight", {})}
            className={btnCls}
          >
            {t("nodedetail.mail.preflight.run")}
          </button>
        </div>
        {pending === "mail.preflight" && (
          <p className={noteCls}>{t("common.loading")}</p>
        )}
        {preflight && (
          <div className="mt-2" data-testid="mail-preflight">
            <div className="grid grid-cols-2 gap-x-4 gap-y-1 text-xs text-ink sm:grid-cols-4">
              <span>
                {t("nodedetail.mail.preflight.postfix")}:{" "}
                <b>{preflight.postfix_installed ? "✓" : "✗"}</b>
              </span>
              <span>
                {t("nodedetail.mail.preflight.dovecot")}:{" "}
                <b>{preflight.dovecot_installed ? "✓" : "✗"}</b>
              </span>
              <span>
                {t("nodedetail.mail.preflight.rspamd")}:{" "}
                <b>{preflight.rspamd_installed ? "✓" : "✗"}</b>
              </span>
              <span>
                {t("nodedetail.mail.preflight.vmail")}:{" "}
                <b>{preflight.vmail_user ? "✓" : "✗"}</b>
              </span>
            </div>
            <div className="mt-2 flex flex-wrap gap-2 text-[11px] text-ink-soft">
              {(preflight.ports ?? []).map((p) => (
                <span
                  key={p.port}
                  className={`rounded border px-1.5 py-0.5 ${
                    p.state === "free"
                      ? "border-green-300 text-green-700"
                      : p.state === "occupied"
                        ? "border-amber-300 text-amber-700"
                        : "border-border"
                  }`}
                >
                  {p.port}: {t(`nodedetail.mail.preflight.port_${p.state === "free" ? "free" : p.state === "occupied" ? "occupied" : "unknown"}`)}
                </span>
              ))}
            </div>
            {(preflight.notes ?? []).length > 0 && (
              <ul className="mt-2 list-disc pl-4 text-[11px] text-ink-soft">
                {(preflight.notes ?? []).map((n, i) => (
                  <li key={i}>{n}</li>
                ))}
              </ul>
            )}
          </div>
        )}
      </div>

      {/* Status */}
      <div className="mt-3 rounded-lg border border-border bg-surface p-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h4 className="text-xs font-semibold text-ink">{t("nodedetail.mail.status.title")}</h4>
          <button
            type="button"
            disabled={pending !== null}
            onClick={() => guardedRun("mail.status", {})}
            className={btnCls}
          >
            {t("nodedetail.mail.status.run")}
          </button>
        </div>
        {status && (
          <div className="mt-2 grid grid-cols-2 gap-x-4 gap-y-1 text-xs text-ink sm:grid-cols-5" data-testid="mail-status">
            <span>{t("nodedetail.mail.status.domains")}: <b>{status.domains}</b></span>
            <span>{t("nodedetail.mail.status.mailboxes")}: <b>{status.mailboxes}</b></span>
            <span>{t("nodedetail.mail.status.aliases")}: <b>{status.aliases}</b></span>
            <span>{t("nodedetail.mail.status.queue")}: <b>{status.queue?.total ?? 0}</b></span>
            <span>
              postfix: <b>{status.services?.postfix ?? "?"}</b>
            </span>
          </div>
        )}
      </div>

      {/* Deliverability (kriter 3): dış DNS bağımlılıkları AÇIKLANIR */}
      <div className="mt-3 rounded-lg border border-border bg-surface p-3" data-testid="mail-deliverability">
        <h4 className="text-xs font-semibold text-ink">{t("nodedetail.mail.deliverability.title")}</h4>
        <p className={noteCls}>{t("nodedetail.mail.deliverability.intro")}</p>
        <dl className="mt-2 grid gap-2 text-xs text-ink sm:grid-cols-2">
          <div className="rounded border border-border p-2">
            <dt className="font-semibold">SPF</dt>
            <dd className="mt-0.5 text-ink-soft">{t("nodedetail.mail.deliverability.spf")}</dd>
            <dd className="mt-1 font-mono text-[10px] text-ink-soft">example.com. IN TXT "v=spf1 mx -all"</dd>
          </div>
          <div className="rounded border border-border p-2">
            <dt className="font-semibold">DKIM</dt>
            <dd className="mt-0.5 text-ink-soft">{t("nodedetail.mail.deliverability.dkim")}</dd>
            <dd className="mt-1 font-mono text-[10px] text-ink-soft">selector._domainkey.example.com. IN TXT "v=DKIM1; k=rsa; p=…"</dd>
          </div>
          <div className="rounded border border-border p-2">
            <dt className="font-semibold">DMARC</dt>
            <dd className="mt-0.5 text-ink-soft">{t("nodedetail.mail.deliverability.dmarc")}</dd>
            <dd className="mt-1 font-mono text-[10px] text-ink-soft">_dmarc.example.com. IN TXT "v=DMARC1; p=quarantine; rua=mailto:dmarc@example.com"</dd>
          </div>
          <div className="rounded border border-border p-2">
            <dt className="font-semibold">PTR / rDNS</dt>
            <dd className="mt-0.5 text-ink-soft">{t("nodedetail.mail.deliverability.ptr")}</dd>
            <dd className="mt-1 text-[10px] text-ink-soft">{t("nodedetail.mail.deliverability.ptrNote")}</dd>
          </div>
        </dl>
      </div>

      {/* Domainler */}
      <div className="mt-3 rounded-lg border border-border bg-surface p-3">
        <h4 className="text-xs font-semibold text-ink">{t("nodedetail.mail.domains.title")}</h4>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (!pfDomain.trim()) return;
            guardedRun("mail.domain.create", { domain: pfDomain.trim().toLowerCase() });
          }}
          className="mt-2 flex flex-wrap items-end gap-2"
        >
          <label className="flex flex-col gap-1">
            <span className="text-[11px] font-medium text-ink">{t("nodedetail.mail.domains.name")}</span>
            <input
              value={pfDomain}
              onChange={(e) => setPfDomain(e.target.value)}
              placeholder="mail.example.com"
              className={`${inputCls} w-56`}
            />
          </label>
          <button type="submit" disabled={pending !== null} className={btnCls}>
            {t("nodedetail.mail.domains.create")}
          </button>
          <button
            type="button"
            disabled={pending !== null}
            onClick={() => guardedRun("mail.domain.list", {})}
            className={btnCls}
          >
            {t("nodedetail.mail.domains.list")}
          </button>
        </form>
        {domains.length > 0 && (
          <ul className="mt-2 space-y-1" data-testid="mail-domains">
            {domains.map((d) => (
              <li key={d.name} className="flex items-center justify-between gap-2 rounded border border-border px-2 py-1 text-xs text-ink">
                <span className="font-mono">{d.name}</span>
                <button
                  type="button"
                  disabled={pending !== null}
                  onClick={() => guardedRun("mail.domain.delete", { domain: d.name })}
                  className={btnDangerCls}
                >
                  {t("nodedetail.mail.domains.delete")}
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>

      {/* Posta kutuları + şifre + kota */}
      <div className="mt-3 rounded-lg border border-border bg-surface p-3">
        <h4 className="text-xs font-semibold text-ink">{t("nodedetail.mail.mailboxes.title")}</h4>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (!mbDomain.trim() || !mbLocal.trim()) return;
            guardedRun("mail.mailbox.create", {
              domain: mbDomain.trim().toLowerCase(),
              local: mbLocal.trim().toLowerCase(),
            });
          }}
          className="mt-2 flex flex-wrap items-end gap-2"
        >
          <label className="flex flex-col gap-1">
            <span className="text-[11px] font-medium text-ink">{t("nodedetail.mail.mailboxes.domain")}</span>
            <input value={mbDomain} onChange={(e) => setMbDomain(e.target.value)} className={`${inputCls} w-44`} placeholder="mail.example.com" />
          </label>
          <label className="flex flex-col gap-1">
            <span className="text-[11px] font-medium text-ink">{t("nodedetail.mail.mailboxes.local")}</span>
            <input value={mbLocal} onChange={(e) => setMbLocal(e.target.value)} className={`${inputCls} w-36`} placeholder="ali" />
          </label>
          <button type="submit" disabled={pending !== null} className={btnCls}>
            {t("nodedetail.mail.mailboxes.create")}
          </button>
          <button
            type="button"
            disabled={pending !== null}
            onClick={() => guardedRun("mail.mailbox.list", {})}
            className={btnCls}
          >
            {t("nodedetail.mail.mailboxes.list")}
          </button>
        </form>

        {mailboxCreated?.credentials && (
          <div className="mt-2 rounded border border-amber-300 bg-amber-50 p-2 text-[11px] text-ink" data-testid="mail-credential-note">
            {t("nodedetail.mail.mailboxes.credNote")}
            {mailboxCreated.credentials.redaction_reason ? (
              <p className="mt-1 text-red-700">{mailboxCreated.credentials.redaction_reason}</p>
            ) : (
              <p className="mt-1 font-mono">{mailboxCreated.credentials.fingerprint}</p>
            )}
          </div>
        )}

        {mailboxes.length > 0 && (
          <ul className="mt-2 space-y-1" data-testid="mail-mailboxes">
            {mailboxes.map((m) => (
              <li key={m.address} className="flex flex-wrap items-center justify-between gap-2 rounded border border-border px-2 py-1 text-xs text-ink">
                <span className="font-mono">
                  {m.address} · {bytesLabel(m.quota_bytes)}
                </span>
                <span className="flex items-center gap-1">
                  <input
                    aria-label={t("nodedetail.mail.mailboxes.quotaFor") + " " + m.address}
                    value={mbQuotaAddr === m.address ? mbQuotaMB : ""}
                    onChange={(e) => {
                      setMbQuotaAddr(m.address);
                      setMbQuotaMB(e.target.value);
                    }}
                    placeholder={String(Math.round(m.quota_bytes / (1024 * 1024)))}
                    className={`${inputCls} w-24`}
                  />
                  <span className="text-[10px] text-ink-soft">MiB</span>
                  <button
                    type="button"
                    disabled={pending !== null}
                    onClick={() =>
                      guardedRun("mail.quota.set", {
                        mailbox: m.address,
                        bytes: Math.round(Number(mbQuotaMB) * 1024 * 1024),
                      })
                    }
                    className={btnCls}
                  >
                    {t("nodedetail.mail.mailboxes.quotaSet")}
                  </button>
                  <button
                    type="button"
                    disabled={pending !== null}
                    onClick={() => guardedRun("mail.mailbox.delete", { mailbox: m.address })}
                    className={btnDangerCls}
                  >
                    {t("nodedetail.mail.mailboxes.delete")}
                  </button>
                </span>
              </li>
            ))}
          </ul>
        )}

        {/* Mailbox şifreleri: TEK SEFERLİK reveal */}
        {creds.length > 0 && (
          <div className="mt-2 rounded-lg border border-border p-2" data-testid="mail-credentials">
            <h5 className="text-[11px] font-semibold text-ink">{t("nodedetail.mail.credentials.title")}</h5>
            <ul className="mt-1 space-y-1">
              {creds.map((c) => (
                <li key={c.id} className="flex flex-wrap items-center justify-between gap-2 text-[11px] text-ink">
                  <span className="font-mono">
                    {c.username} · {c.fingerprint} ·{" "}
                    {c.revealed_at ? t("nodedetail.mail.credentials.revealed") : t("nodedetail.mail.credentials.pending")}
                  </span>
                  <button
                    type="button"
                    disabled={Boolean(c.revealed_at)}
                    onClick={() => void reveal(c.id)}
                    className={btnCls}
                  >
                    {t("nodedetail.mail.credentials.reveal")}
                  </button>
                </li>
              ))}
            </ul>
            {revealed && (
              <p className="mt-1 flex flex-wrap items-center gap-2 rounded border border-amber-300 bg-amber-50 p-1.5 font-mono text-ink" data-testid="mail-revealed-password">
                <span>{revealed.password}</span>
                {/* UX denetimi m.4.4: tek seferlik şifre tek tıkla kopyalanır. */}
                <CopyButton value={revealed.password} />
                <span className="font-sans text-[10px] text-ink-soft">{t("nodedetail.mail.credentials.onceWarning")}</span>
              </p>
            )}
          </div>
        )}
      </div>

      {/* Takma adlar */}
      <div className="mt-3 rounded-lg border border-border bg-surface p-3">
        <h4 className="text-xs font-semibold text-ink">{t("nodedetail.mail.aliases.title")}</h4>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (!alAliasDomain.trim() || !alAliasLocal.trim() || !alTarget.trim()) return;
            guardedRun("mail.alias.create", {
              alias: `${alAliasLocal.trim().toLowerCase()}@${alAliasDomain.trim().toLowerCase()}`,
              target: alTarget.trim().toLowerCase(),
            });
          }}
          className="mt-2 flex flex-wrap items-end gap-2"
        >
          <label className="flex flex-col gap-1">
            <span className="text-[11px] font-medium text-ink">{t("nodedetail.mail.aliases.alias")}</span>
            <span className="flex items-center gap-1">
              <input value={alAliasLocal} onChange={(e) => setAlAliasLocal(e.target.value)} className={`${inputCls} w-28`} placeholder="info" />
              <span className="text-ink-soft">@</span>
              <input value={alAliasDomain} onChange={(e) => setAlAliasDomain(e.target.value)} className={`${inputCls} w-40`} placeholder="mail.example.com" />
            </span>
          </label>
          <label className="flex flex-col gap-1">
            <span className="text-[11px] font-medium text-ink">{t("nodedetail.mail.aliases.target")}</span>
            <input value={alTarget} onChange={(e) => setAlTarget(e.target.value)} className={`${inputCls} w-56`} placeholder="ali@mail.example.com" />
          </label>
          <button type="submit" disabled={pending !== null} className={btnCls}>
            {t("nodedetail.mail.aliases.create")}
          </button>
          <button
            type="button"
            disabled={pending !== null}
            onClick={() => guardedRun("mail.alias.list", {})}
            className={btnCls}
          >
            {t("nodedetail.mail.aliases.list")}
          </button>
        </form>
        {aliases.length > 0 && (
          <ul className="mt-2 space-y-1" data-testid="mail-aliases">
            {aliases.map((a) => (
              <li key={a.alias} className="flex items-center justify-between gap-2 rounded border border-border px-2 py-1 text-xs text-ink">
                <span className="font-mono">
                  {a.alias} → {a.target}
                </span>
                <button
                  type="button"
                  disabled={pending !== null}
                  onClick={() => guardedRun("mail.alias.delete", { alias: a.alias })}
                  className={btnDangerCls}
                >
                  {t("nodedetail.mail.aliases.delete")}
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>

      {/* Kuyruk (kriter 5): içeriksiz görüntü */}
      <div className="mt-3 rounded-lg border border-border bg-surface p-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h4 className="text-xs font-semibold text-ink">{t("nodedetail.mail.queue.title")}</h4>
          <button
            type="button"
            disabled={pending !== null}
            onClick={() => guardedRun("mail.queue.list", {})}
            className={btnCls}
          >
            {t("nodedetail.mail.queue.refresh")}
          </button>
        </div>
        <p className={noteCls}>{t("nodedetail.mail.queue.note")}</p>
        {queue.length > 0 && (
          <table className="mt-2 w-full text-left text-[11px] text-ink" data-testid="mail-queue">
            <thead>
              <tr className="text-ink-soft">
                <th className="py-1">ID</th>
                <th className="py-1">{t("nodedetail.mail.queue.queueName")}</th>
                <th className="py-1">{t("nodedetail.mail.queue.size")}</th>
                <th className="py-1">{t("nodedetail.mail.queue.age")}</th>
                <th className="py-1">{t("nodedetail.mail.queue.sender")}</th>
                <th className="py-1">{t("nodedetail.mail.queue.reason")}</th>
              </tr>
            </thead>
            <tbody>
              {queue.map((q) => (
                <tr key={q.queue_id} className="border-t border-border">
                  <td className="py-1 font-mono">{q.queue_id}</td>
                  <td className="py-1">{q.queue_name}</td>
                  <td className="py-1">{bytesLabel(q.size_bytes)}</td>
                  <td className="py-1">{ageLabel(q.age_seconds)}</td>
                  <td className="py-1 font-mono">{q.sender ?? "—"}</td>
                  <td className="py-1">{q.reason ?? "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        {queue.length === 0 && report !== null && taskOp === "mail.queue.list" && (
          <p className={noteCls}>{t("nodedetail.mail.queue.empty")}</p>
        )}
      </div>

      {/* Limitler (kriter 5): spam/abuse limitleri, kapalı enum */}
      <div className="mt-3 rounded-lg border border-border bg-surface p-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h4 className="text-xs font-semibold text-ink">{t("nodedetail.mail.limits.title")}</h4>
          <span className="flex gap-1">
            <button
              type="button"
              disabled={pending !== null}
              onClick={() => guardedRun("mail.limits.get", {})}
              className={btnCls}
            >
              {t("nodedetail.mail.limits.get")}
            </button>
            <button
              type="button"
              disabled={pending !== null || Object.keys(limitValues).length === 0}
              onClick={() => {
                const payload: Record<string, number> = {};
                for (const [k, v] of Object.entries(limitValues)) {
                  const n = Number(v);
                  if (Number.isFinite(n)) payload[k] = n;
                }
                guardedRun("mail.limits.set", { limits: payload });
              }}
              className={btnCls}
            >
              {t("nodedetail.mail.limits.set")}
            </button>
          </span>
        </div>
        <p className={noteCls}>{t("nodedetail.mail.limits.note")}</p>
        <div className="mt-2 grid gap-2 sm:grid-cols-2">
          {LIMIT_KEYS.map((k) => (
            <label key={k} className="flex flex-col gap-0.5 text-[11px] text-ink">
              <span className="font-mono">{k}</span>
              <input
                value={limitValues[k] ?? limits[k] ?? ""}
                onChange={(e) => setLimitValues((prev) => ({ ...prev, [k]: e.target.value }))}
                className={inputCls}
              />
            </label>
          ))}
        </div>
      </div>

      {/* Config check (kriter 4 okuma görünümü) */}
      <div className="mt-3 rounded-lg border border-border bg-surface p-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h4 className="text-xs font-semibold text-ink">{t("nodedetail.mail.configcheck.title")}</h4>
          <button
            type="button"
            disabled={pending !== null}
            onClick={() => guardedRun("mail.config.check", {})}
            className={btnCls}
          >
            {t("nodedetail.mail.configcheck.run")}
          </button>
        </div>
        {configCheck && (
          <div className="mt-2 flex flex-wrap gap-2 text-[11px]" data-testid="mail-configcheck">
            {(["postfix", "dovecot", "rspamd"] as const).map((c) => {
              const o = configCheck[c];
              const cls =
                o?.state === "pass"
                  ? "border-green-300 text-green-700"
                  : o?.state === "fail"
                    ? "border-red-300 text-red-700"
                    : "border-border text-ink-soft";
              return (
                <span key={c} className={`rounded border px-1.5 py-0.5 ${cls}`}>
                  {c}: {t(`nodedetail.mail.configcheck.${o?.state ?? "absent"}`)}
                </span>
              );
            })}
          </div>
        )}
      </div>
    </section>
  );
}
