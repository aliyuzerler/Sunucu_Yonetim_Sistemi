import { useEffect, useState } from "react";
import { apiFetch, isApiError } from "../api/client";

// Faz 10: Veritabanı + scoped dosya yöneticisi paneli (NodeDetail bölümü).
//
// Güvenlik notları (ADR-0014):
//   - db.user.create şifresi node'da üretilir; control-plane seal eder ve
//     TEK SEFERLİK reveal uç noktası üzerinden gösterir. Şifre bu bileşende
//     yalnız reveal yanıtında tutulur, state'te saklanmaz, konsola yazılmaz.
//   - fs.* kökleri yalnız site adıdır (docroot türetilir); host geneli
//     browse edilemez.
//   - Tüm işlemler görevdir; sonuç poll edilip rapor kartında gösterilir
//     (compose/web kalıbı).

export interface TaskLike {
  id: string;
  status: string;
  result?: unknown;
  error_category?: string;
}

interface DbInstance { kind: string; version?: string; socket: string; active?: string }
interface DbDatabase { name: string; size?: string }
interface DbUser { name: string; host?: string }
interface DbBackup { file: string; bytes: number; created: string }
interface FsEntry { name: string; type: string; size: number; mode: string; mod_time: string; link?: string }

export interface DbReport {
  instances?: DbInstance[];
  databases?: DbDatabase[];
  users?: DbUser[];
  backups?: DbBackup[];
  credentials?: {
    credential_id?: string;
    fingerprint?: string;
    one_time_reveal?: boolean;
    password_redacted?: string;
    redaction_reason?: string;
  };
  user?: { name?: string; host?: string };
  kind?: string;
  database?: string;
  file?: string;
  bytes?: number;
  sha256?: string;
  duration?: string;
  created?: boolean;
  already_exists?: boolean;
  deleted?: boolean;
  set?: boolean;
  level?: string;
  restored?: boolean;
}

export interface FsReport {
  listing?: { root: string; path: string; entries?: FsEntry[]; truncated?: boolean };
  file?: { path: string; size: number; mode: string; sha256: string; mime: string; content_b64: string };
  result?: {
    path?: string; bytes?: number; mode?: string; mime?: string;
    created?: boolean; overwritten?: boolean;
    old_mode?: string; new_mode?: string;
    uid?: number; gid?: number; user?: string;
  };
}

interface CredentialRow {
  id: string;
  kind: string;
  username: string;
  host?: string;
  fingerprint: string;
  revealed_at?: string | null;
}

const DB_KINDS = ["postgres", "mysql"] as const;
const GRANT_LEVELS = ["readonly", "readwrite", "all"] as const;

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

const inputCls =
  "rounded-lg border border-border bg-surface px-3 py-1.5 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent";
const btnCls =
  "rounded-lg border border-border bg-surface px-3 py-1.5 text-xs font-medium text-ink hover:bg-neutral-soft disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent";
const btnDangerCls =
  "rounded-lg border border-red-300 bg-surface px-3 py-1.5 text-xs font-medium text-red-700 hover:bg-red-50 disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent";

export function DbFsPanel(props: {
  nodeId: string;
  isDb: boolean;
  isFs: boolean;
  submitTask: (op: string, payload: Record<string, unknown>) => Promise<TaskLike | null>;
  t: (k: string) => string;
}) {
  const { nodeId, isDb, isFs, submitTask, t } = props;

  // ---- paylaşılan görev akışı ----
  const [pending, setPending] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [dbReport, setDbReport] = useState<DbReport | null>(null);
  const [fsReport, setFsReport] = useState<FsReport | null>(null);
  const [taskId, setTaskId] = useState<string | null>(null);
  const [taskOp, setTaskOp] = useState<string>("");
  const [revealed, setRevealed] = useState<{ id: string; password: string } | null>(null);

  // ---- db form state ----
  const [dbKind, setDbKind] = useState<string>("postgres");
  const [dbName, setDbName] = useState("");
  const [dbOwner, setDbOwner] = useState("");
  const [dbUserName, setDbUserName] = useState("");
  const [dbUserHost, setDbUserHost] = useState("");
  const [grantLevel, setGrantLevel] = useState<string>("readonly");
  const [backupFile, setBackupFile] = useState("");

  // ---- fs form state ----
  const [fsRoot, setFsRoot] = useState("");
  const [fsPath, setFsPath] = useState("");
  const [fsMode, setFsMode] = useState("0644");
  const [fsOverwrite, setFsOverwrite] = useState(false);
  const [fsUploadPath, setFsUploadPath] = useState("");
  const [fsContent, setFsContent] = useState("");

  // ---- credentials listesi (reveal kartı için) ----
  const [creds, setCreds] = useState<CredentialRow[]>([]);
  const loadCreds = () => {
    apiFetch<{ credentials: CredentialRow[] }>(
      `/api/v1/nodes/${encodeURIComponent(nodeId)}/db-credentials`,
    )
      .then((res) => setCreds(res.credentials ?? []))
      .catch(() => setCreds([]));
  };
  useEffect(loadCreds, [nodeId]);

  async function run(op: string, payload: Record<string, unknown>) {
    setError(null);
    setDbReport(null);
    setFsReport(null);
    setPending(op);
    const created = await submitTask(op, payload);
    setPending(null);
    if (!created) return;
    setTaskId(created.id);
  }

  // Görev poll: succeed → report; failed → error (web kalıbı). taskOp,
  // taskId ile aynı render batch'inde set edilir → effect güncel op görür.
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
          if (taskOp.startsWith("db.")) {
            setDbReport((task.result ?? null) as DbReport | null);
            if (taskOp === "db.user.create") loadCreds();
          } else if (taskOp.startsWith("fs.")) {
            setFsReport((task.result ?? null) as FsReport | null);
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

  // ---- reveal (tek seferlik) ----
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

  function downloadFile(r: NonNullable<FsReport["file"]>) {
    try {
      const bin = atob(r.content_b64);
      const bytes = new Uint8Array(bin.length);
      for (let i = 0; i < bin.length; i += 1) bytes[i] = bin.charCodeAt(i);
      const blob = new Blob([bytes]);
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = r.path.split("/").pop() ?? "file";
      a.click();
      URL.revokeObjectURL(url);
    } catch {
      setError(t("nodedetail.fs.downloadError"));
    }
  }

  async function onUploadFile(f: File | null) {
    if (!f) return;
    const name = f.name;
    const path = (fsUploadPath.trim() || name).replace(/\/+$/, "");
    setFsUploadPath(path);
    if (f.size > 512 * 1024) {
      setError(t("nodedetail.fs.tooLarge"));
      return;
    }
    const buf = new Uint8Array(await f.arrayBuffer());
    let bin = "";
    for (let i = 0; i < buf.length; i += 1) bin += String.fromCharCode(buf[i]);
    setFsContent(btoa(bin));
  }

  const v = (s: string) => s.trim();

  return (
    <div className="space-y-4">
      {/* ================= VERİTABANLARI ================= */}
      {isDb && (
        <section aria-label={t("nodedetail.db.title")} className="rounded-xl border border-border bg-panel p-4 shadow-sm" data-testid="db-panel">
          <h3 className="text-sm font-semibold text-ink">{t("nodedetail.db.title")}</h3>
          <p className="mt-1 text-[11px] text-ink-soft">{t("nodedetail.db.note")}</p>

          <div className="mt-3 flex flex-wrap items-end gap-2">
            <label className="flex flex-col gap-1">
              <span className="text-xs font-medium text-ink">{t("nodedetail.db.kind")}</span>
              <select value={dbKind} onChange={(e) => setDbKind(e.target.value)} className={inputCls}>
                {DB_KINDS.map((k) => (
                  <option key={k} value={k}>{k}</option>
                ))}
              </select>
            </label>
            <button type="button" disabled={pending !== null} onClick={() => guardedRun("db.instance.list", {})} className={btnCls}>
              {t("nodedetail.db.instanceList")}
            </button>
            <button type="button" disabled={pending !== null} onClick={() => guardedRun("db.database.list", { kind: dbKind, limit: 200 })} className={btnCls}>
              {t("nodedetail.db.databaseList")}
            </button>
            <button type="button" disabled={pending !== null} onClick={() => guardedRun("db.user.list", { kind: dbKind, limit: 200 })} className={btnCls}>
              {t("nodedetail.db.userList")}
            </button>
            <button type="button" disabled={pending !== null} onClick={() => guardedRun("db.backup.list", { kind: dbKind, limit: 200 })} className={btnCls}>
              {t("nodedetail.db.backupList")}
            </button>
          </div>

          {/* database create/delete */}
          <div className="mt-3 flex flex-wrap items-end gap-2">
            <label className="flex flex-col gap-1">
              <span className="text-xs font-medium text-ink">{t("nodedetail.db.dbName")}</span>
              <input value={dbName} onChange={(e) => { setDbName(e.target.value); setError(null); }} placeholder="app_db" className={`${inputCls} w-40 font-mono`} />
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-xs font-medium text-ink">{t("nodedetail.db.owner")}</span>
              <input value={dbOwner} onChange={(e) => setDbOwner(e.target.value)} placeholder="app_u" className={`${inputCls} w-36 font-mono`} />
            </label>
            <button type="button" disabled={pending !== null || !v(dbName)} onClick={() => guardedRun("db.database.create", { kind: dbKind, name: v(dbName), ...(v(dbOwner) ? { owner: v(dbOwner) } : {}) })} className={btnCls}>
              {t("nodedetail.db.databaseCreate")}
            </button>
            <button type="button" disabled={pending !== null || !v(dbName)} onClick={() => guardedRun("db.database.delete", { kind: dbKind, database: v(dbName) })} className={btnDangerCls}>
              {t("nodedetail.db.databaseDelete")}
            </button>
          </div>

          {/* user create/delete + grant */}
          <div className="mt-3 flex flex-wrap items-end gap-2">
            <label className="flex flex-col gap-1">
              <span className="text-xs font-medium text-ink">{t("nodedetail.db.userName")}</span>
              <input value={dbUserName} onChange={(e) => { setDbUserName(e.target.value); setError(null); }} placeholder="app_ro" className={`${inputCls} w-36 font-mono`} />
            </label>
            {dbKind === "mysql" && (
              <label className="flex flex-col gap-1">
                <span className="text-xs font-medium text-ink">{t("nodedetail.db.userHost")}</span>
                <input value={dbUserHost} onChange={(e) => setDbUserHost(e.target.value)} placeholder="localhost" className={`${inputCls} w-32 font-mono`} />
              </label>
            )}
            <button
              type="button"
              disabled={pending !== null || !v(dbUserName)}
              onClick={() => guardedRun("db.user.create", { kind: dbKind, name: v(dbUserName), ...(dbKind === "mysql" && v(dbUserHost) ? { host: v(dbUserHost) } : {}) })}
              className={btnCls}
            >
              {t("nodedetail.db.userCreate")}
            </button>
            <button
              type="button"
              disabled={pending !== null || !v(dbUserName)}
              onClick={() => guardedRun("db.user.delete", { kind: dbKind, name: v(dbUserName), ...(dbKind === "mysql" && v(dbUserHost) ? { host: v(dbUserHost) } : {}) })}
              className={btnDangerCls}
            >
              {t("nodedetail.db.userDelete")}
            </button>
            <label className="flex flex-col gap-1">
              <span className="text-xs font-medium text-ink">{t("nodedetail.db.grantLevel")}</span>
              <select value={grantLevel} onChange={(e) => setGrantLevel(e.target.value)} className={inputCls}>
                {GRANT_LEVELS.map((l) => (
                  <option key={l} value={l}>{l}</option>
                ))}
              </select>
            </label>
            <button
              type="button"
              disabled={pending !== null || !v(dbUserName) || !v(dbName)}
              onClick={() => guardedRun("db.grant.set", { kind: dbKind, user: v(dbUserName), database: v(dbName), level: grantLevel, ...(dbKind === "mysql" && v(dbUserHost) ? { host: v(dbUserHost) } : {}) })}
              className={btnCls}
            >
              {t("nodedetail.db.grantSet")}
            </button>
          </div>

          {/* backup / restore */}
          <div className="mt-3 flex flex-wrap items-end gap-2">
            <button type="button" disabled={pending !== null || !v(dbName)} onClick={() => guardedRun("db.backup", { kind: dbKind, database: v(dbName) })} className={btnCls}>
              {t("nodedetail.db.backup")}
            </button>
            <label className="flex flex-col gap-1">
              <span className="text-xs font-medium text-ink">{t("nodedetail.db.backupFile")}</span>
              <input value={backupFile} onChange={(e) => setBackupFile(e.target.value)} placeholder="app_db-20261102T090507Z.sql" className={`${inputCls} w-72 font-mono`} />
            </label>
            <button type="button" disabled={pending !== null || !v(dbName) || !v(backupFile)} onClick={() => guardedRun("db.restore", { kind: dbKind, database: v(dbName), backup: v(backupFile) })} className={btnDangerCls}>
              {t("nodedetail.db.restore")}
            </button>
          </div>

          {error && <p className="mt-2 text-xs text-red-700" data-testid="dbfs-error">{error}</p>}

          {/* DB rapor kartı */}
          {dbReport && (
            <div className="mt-3 rounded-lg border border-border bg-surface p-3" data-testid="db-report">
              {dbReport.instances && (
                <div>
                  <p className="text-xs font-medium text-ink">{t("nodedetail.db.instances")}</p>
                  {dbReport.instances.length === 0 ? (
                    <p className="mt-1 text-xs text-ink-soft">{t("nodedetail.db.none")}</p>
                  ) : (
                    <table className="mt-1 w-full text-left text-xs text-ink">
                      <tbody>
                        {dbReport.instances.map((i) => (
                          <tr key={i.kind} className="border-t border-border">
                            <td className="py-1 pr-2 font-mono">{i.kind}</td>
                            <td className="py-1 pr-2">{i.version ?? "—"}</td>
                            <td className="py-1 pr-2 font-mono">{i.socket}</td>
                            <td className="py-1">{i.active ?? "—"}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  )}
                </div>
              )}
              {dbReport.databases && (
                <div>
                  <p className="text-xs font-medium text-ink">{t("nodedetail.db.databases")}</p>
                  <p className="mt-1 text-xs text-ink">
                    {dbReport.databases.length === 0
                      ? t("nodedetail.db.none")
                      : dbReport.databases.map((d) => `${d.name}${d.size ? ` (${d.size})` : ""}`).join(", ")}
                  </p>
                </div>
              )}
              {dbReport.users && (
                <div>
                  <p className="text-xs font-medium text-ink">{t("nodedetail.db.users")}</p>
                  <p className="mt-1 text-xs text-ink">
                    {dbReport.users.length === 0
                      ? t("nodedetail.db.none")
                      : dbReport.users.map((u) => (u.host ? `${u.name}@${u.host}` : u.name)).join(", ")}
                  </p>
                </div>
              )}
              {dbReport.backups && (
                <div>
                  <p className="text-xs font-medium text-ink">{t("nodedetail.db.backups")}</p>
                  {dbReport.backups.length === 0 ? (
                    <p className="mt-1 text-xs text-ink-soft">{t("nodedetail.db.none")}</p>
                  ) : (
                    <table className="mt-1 w-full text-left text-xs text-ink">
                      <tbody>
                        {dbReport.backups.map((b) => (
                          <tr key={b.file} className="border-t border-border">
                            <td className="py-1 pr-2 font-mono">{b.file}</td>
                            <td className="py-1 pr-2">{bytesLabel(b.bytes)}</td>
                            <td className="py-1">{b.created}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  )}
                </div>
              )}
              {dbReport.credentials && (
                <div className="mt-2 rounded border border-border p-2" data-testid="db-credential-note">
                  {dbReport.credentials.password_redacted ? (
                    <p className="text-xs text-red-700">
                      {t("nodedetail.db.passwordRedacted")}: {dbReport.credentials.redaction_reason}
                    </p>
                  ) : (
                    <>
                      <p className="text-xs font-medium text-ink">{t("nodedetail.db.passwordGenerated")}</p>
                      <p className="mt-1 text-xs text-ink-soft">
                        {t("nodedetail.db.passwordOneTime")} — {t("nodedetail.db.fingerprint")}:{" "}
                        <span className="font-mono">{dbReport.credentials.fingerprint}</span>
                      </p>
                    </>
                  )}
                </div>
              )}
              {(dbReport.created !== undefined || dbReport.already_exists !== undefined) && (
                <p className="mt-2 text-xs text-ink">
                  {dbReport.database}: {dbReport.already_exists ? t("nodedetail.db.alreadyExists") : t("nodedetail.db.created")}
                </p>
              )}
              {dbReport.set && (
                <p className="mt-2 text-xs text-ink">
                  {t("nodedetail.db.grantDone")}: {dbReport.level} → {dbReport.database}
                </p>
              )}
              {dbReport.file && (
                <p className="mt-2 font-mono text-[11px] text-ink-soft">
                  → {dbReport.file} · {bytesLabel(dbReport.bytes ?? 0)}
                  {dbReport.sha256 ? ` · sha256:${dbReport.sha256.slice(0, 12)}…` : ""}
                  {dbReport.duration ? ` · ${dbReport.duration}` : ""}
                </p>
              )}
              {dbReport.restored && (
                <p className="mt-2 text-xs text-ink">{t("nodedetail.db.restoreDone")}</p>
              )}
            </div>
          )}

          {/* Kimlik listesi + tek-seferlik reveal */}
          {creds.length > 0 && (
            <div className="mt-3 rounded-lg border border-border bg-surface p-3" data-testid="db-credentials">
              <p className="text-xs font-medium text-ink">{t("nodedetail.db.credentials")}</p>
              <table className="mt-1 w-full text-left text-xs text-ink">
                <tbody>
                  {creds.map((c) => (
                    <tr key={c.id} className="border-t border-border">
                      <td className="py-1 pr-2 font-mono">{c.kind}/{c.username}{c.host ? `@${c.host}` : ""}</td>
                      <td className="py-1 pr-2 font-mono text-ink-soft">{c.fingerprint}</td>
                      <td className="py-1">
                        {revealed && revealed.id === c.id ? (
                          <span className="font-mono text-ink" data-testid="revealed-password">{revealed.password}</span>
                        ) : c.revealed_at ? (
                          <span className="text-ink-soft">{t("nodedetail.db.revealed")}</span>
                        ) : (
                          <button type="button" onClick={() => void reveal(c.id)} className={btnCls} data-testid={`reveal-${c.username}`}>
                            {t("nodedetail.db.revealOnce")}
                          </button>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
              <p className="mt-1 text-[10px] text-ink-soft">{t("nodedetail.db.revealWarning")}</p>
            </div>
          )}
        </section>
      )}

      {/* ================= DOSYA YÖNETİCİSİ ================= */}
      {isFs && (
        <section aria-label={t("nodedetail.fs.title")} className="rounded-xl border border-border bg-panel p-4 shadow-sm" data-testid="fs-panel">
          <h3 className="text-sm font-semibold text-ink">{t("nodedetail.fs.title")}</h3>
          <p className="mt-1 text-[11px] text-ink-soft">{t("nodedetail.fs.note")}</p>

          <div className="mt-3 flex flex-wrap items-end gap-2">
            <label className="flex flex-col gap-1">
              <span className="text-xs font-medium text-ink">{t("nodedetail.fs.root")}</span>
              <input value={fsRoot} onChange={(e) => { setFsRoot(e.target.value); setError(null); }} placeholder="blog" className={`${inputCls} w-36 font-mono`} />
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-xs font-medium text-ink">{t("nodedetail.fs.path")}</span>
              <input value={fsPath} onChange={(e) => { setFsPath(e.target.value); setError(null); }} placeholder="uploads/img.png" className={`${inputCls} w-64 font-mono`} />
            </label>
            <button
              type="button"
              disabled={pending !== null || !v(fsRoot)}
              onClick={() => guardedRun("fs.list", { root: v(fsRoot), path: v(fsPath), limit: 200 })}
              className={btnCls}
            >
              {t("nodedetail.fs.list")}
            </button>
          </div>

          <div className="mt-2 flex flex-wrap items-end gap-2">
            <label className="flex flex-col gap-1">
              <span className="text-xs font-medium text-ink">{t("nodedetail.fs.uploadPath")}</span>
              <input value={fsUploadPath} onChange={(e) => { setFsUploadPath(e.target.value); setError(null); }} placeholder="uploads/logo.svg" className={`${inputCls} w-56 font-mono`} />
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-xs font-medium text-ink">{t("nodedetail.fs.file")}</span>
              <input type="file" onChange={(e) => void onUploadFile(e.target.files?.[0] ?? null)} className="text-xs text-ink" />
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-xs font-medium text-ink">{t("nodedetail.fs.mode")}</span>
              <input value={fsMode} onChange={(e) => setFsMode(e.target.value)} className={`${inputCls} w-20 font-mono`} />
            </label>
            <label className="flex items-center gap-1 text-xs text-ink">
              <input type="checkbox" checked={fsOverwrite} onChange={(e) => setFsOverwrite(e.target.checked)} />
              {t("nodedetail.fs.overwrite")}
            </label>
            <button
              type="button"
              disabled={pending !== null || !v(fsRoot) || !fsContent}
              onClick={() => guardedRun("fs.upload", { root: v(fsRoot), path: v(fsUploadPath), content_b64: fsContent, ...(fsMode !== "" ? { mode: fsMode } : {}), overwrite: fsOverwrite })}
              className={btnCls}
            >
              {t("nodedetail.fs.upload")}
            </button>
            <button
              type="button"
              disabled={pending !== null || !v(fsRoot) || !v(fsPath)}
              onClick={() => guardedRun("fs.download", { root: v(fsRoot), path: v(fsPath) })}
              className={btnCls}
            >
              {t("nodedetail.fs.download")}
            </button>
            <button
              type="button"
              disabled={pending !== null || !v(fsRoot) || !v(fsPath) || !fsMode}
              onClick={() => guardedRun("fs.chmod", { root: v(fsRoot), path: v(fsPath), mode: fsMode })}
              className={btnCls}
            >
              {t("nodedetail.fs.chmod")}
            </button>
            <button
              type="button"
              disabled={pending !== null || !v(fsRoot) || !v(fsPath)}
              onClick={() => guardedRun("fs.chown", { root: v(fsRoot), path: v(fsPath) })}
              className={btnCls}
            >
              {t("nodedetail.fs.chown")}
            </button>
          </div>

          {error && <p className="mt-2 text-xs text-red-700">{error}</p>}

          {fsReport?.listing && (
            <div className="mt-3 rounded-lg border border-border bg-surface p-3" data-testid="fs-report">
              <p className="text-xs font-medium text-ink">
                {t("nodedetail.fs.listing")}: <span className="font-mono">{fsReport.listing.root}/{fsReport.listing.path || ""}</span>
                {fsReport.listing.truncated ? ` · ${t("nodedetail.fs.truncated")}` : ""}
              </p>
              {!fsReport.listing.entries || fsReport.listing.entries.length === 0 ? (
                <p className="mt-1 text-xs text-ink-soft">{t("nodedetail.db.none")}</p>
              ) : (
                <table className="mt-1 w-full text-left text-xs text-ink">
                  <thead>
                    <tr className="text-ink-soft">
                      <th className="py-1 pr-2 font-medium">{t("nodedetail.fs.colName")}</th>
                      <th className="py-1 pr-2 font-medium">{t("nodedetail.fs.colType")}</th>
                      <th className="py-1 pr-2 font-medium">{t("nodedetail.fs.colSize")}</th>
                      <th className="py-1 pr-2 font-medium">{t("nodedetail.fs.colMode")}</th>
                      <th className="py-1 font-medium">{t("nodedetail.fs.colModified")}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {fsReport.listing.entries.map((e) => (
                      <tr key={e.name} className="border-t border-border">
                        <td className="py-1 pr-2 font-mono">
                          {e.name}
                          {e.link ? <span className="text-ink-soft"> → {e.link}</span> : null}
                        </td>
                        <td className="py-1 pr-2">{e.type}</td>
                        <td className="py-1 pr-2">{e.type === "file" ? bytesLabel(e.size) : "—"}</td>
                        <td className="py-1 pr-2 font-mono">{e.mode}</td>
                        <td className="py-1">{e.mod_time}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              )}
            </div>
          )}

          {fsReport?.file && (
            <div className="mt-3 rounded-lg border border-border bg-surface p-3" data-testid="fs-download">
              <p className="text-xs font-medium text-ink">
                {t("nodedetail.fs.downloaded")}: <span className="font-mono">{fsReport.file.path}</span> · {bytesLabel(fsReport.file.size)} · {fsReport.file.mime}
              </p>
              <p className="mt-1 font-mono text-[11px] text-ink-soft">sha256:{fsReport.file.sha256.slice(0, 16)}…</p>
              <button type="button" onClick={() => downloadFile(fsReport.file!)} className={`mt-2 ${btnCls}`} data-testid="fs-save">
                {t("nodedetail.fs.save")}
              </button>
            </div>
          )}

          {fsReport?.result && (
            <div className="mt-3 rounded-lg border border-border bg-surface p-3" data-testid="fs-result">
              <p className="text-xs text-ink">
                {fsReport.result.created ? t("nodedetail.fs.created") : ""}
                {fsReport.result.overwritten ? t("nodedetail.fs.overwritten") : ""}
                {fsReport.result.new_mode ? `${t("nodedetail.fs.modeChanged")}: ${fsReport.result.old_mode} → ${fsReport.result.new_mode}` : ""}
                {fsReport.result.user ? `${t("nodedetail.fs.ownerChanged")}: ${fsReport.result.user} (${fsReport.result.uid}:${fsReport.result.gid})` : ""}
                {fsReport.result.mime ? ` · ${fsReport.result.mime}` : ""}
                {fsReport.result.bytes !== undefined ? ` · ${bytesLabel(fsReport.result.bytes)}` : ""}
              </p>
            </div>
          )}
        </section>
      )}
    </div>
  );
}
