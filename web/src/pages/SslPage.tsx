import { useCallback, useEffect, useMemo, useState } from "react";
import { apiFetch, isApiError } from "../api/client";
import { EmptyState } from "../components/EmptyState";
import { StatusBadge, type BadgeVariant } from "../components/StatusBadge";
import { useI18n } from "../i18n/LocaleProvider";
import type { NodeRecord, TaskRecord } from "../api/client";

// SslPage is the Faz 09 certificate lifecycle view (ADR-0013). The issue
// wizard chains THREE typed tasks: web.cert.request (key+CSR generated ON
// the node; the private key never leaves it) → cert.issue (ACME DNS-01 on
// the control plane through the provider adapter) → web.cert.install
// (public PEM staged, nginx -t gate, atomic swap, reload). Failures are
// classified (rate_limit with Retry-After, challenge) and rendered.

interface CertRecord {
  id: string;
  name: string;
  sans: string[];
  provider_id: string;
  node_id?: string;
  state: string;
  not_after?: string;
  fingerprint?: string;
  last_error?: string;
  last_error_class?: string;
  retry_after_secs?: number;
  expires_in_days?: number;
  expiring_soon?: boolean;
  effective_state?: string;
}

interface ProviderRow {
  id: string;
  name: string;
  kind: string;
}

const DOMAIN_PATTERN = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$/;

async function runTask(
  operation: string,
  payload: unknown,
  nodeId: string | null,
  fallback: string,
): Promise<TaskRecord> {
  const created = await apiFetch<TaskRecord>("/api/v1/tasks", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(nodeId ? { operation, node_id: nodeId, payload } : { operation, payload }),
  });
  for (let attempt = 0; attempt < 20; attempt += 1) {
    await new Promise((r) => setTimeout(r, attempt === 0 ? 700 : 1000));
    const task = await apiFetch<TaskRecord>(`/api/v1/tasks/${encodeURIComponent(created.id)}`);
    if (task.status === "succeeded") return task;
    if (task.status === "failed" || task.status === "cancelled" || task.status === "expired") {
      throw new Error(task.error_category ?? fallback);
    }
  }
  throw new Error(fallback);
}

const stateVariant: Record<string, BadgeVariant> = {
  pending: "info",
  issuing: "info",
  renewing: "info",
  issued: "ok",
  renewal_failed: "error",
  revoked: "neutral",
  expired: "warn",
};

export function SslPage() {
  const { t } = useI18n();
  const [certs, setCerts] = useState<CertRecord[] | null>(null);
  const [providers, setProviders] = useState<ProviderRow[] | null>(null);
  const [nodes, setNodes] = useState<NodeRecord[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  // wizard state
  const [nodeId, setNodeId] = useState("");
  const [providerId, setProviderId] = useState("");
  const [domain, setDomain] = useState("");
  const [sansInput, setSansInput] = useState("");
  const [step, setStep] = useState<"idle" | "request" | "issue" | "install" | "done">("idle");
  const [wizardBusy, setWizardBusy] = useState(false);
  const [wizardError, setWizardError] = useState<string | null>(null);
  const [issuedCert, setIssuedCert] = useState<{ certPem: string; chainPem: string; nodeId: string } | null>(null);

  const [renewBusy, setRenewBusy] = useState<string | null>(null);
  const [revokeBusy, setRevokeBusy] = useState<string | null>(null);
  const [revokeReason, setRevokeReason] = useState(0);

  const reload = useCallback(async () => {
    try {
      const [c, p, n] = await Promise.all([
        apiFetch<{ certificates: CertRecord[] }>("/api/v1/certificates"),
        apiFetch<{ providers: ProviderRow[] }>("/api/v1/dns-providers"),
        apiFetch<{ nodes: NodeRecord[] }>("/api/v1/nodes"),
      ]);
      setCerts(c.certificates);
      setProviders(p.providers);
      setNodes(n.nodes);
      setError(null);
    } catch (err) {
      setError(isApiError(err) ? err.message : t("common.error.generic"));
    }
  }, [t]);

  // İlk yükleme: asenkron IIFE + cancelled bayrağı (set-state-in-effect).
  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const [c, p, n] = await Promise.all([
          apiFetch<{ certificates: CertRecord[] }>("/api/v1/certificates"),
          apiFetch<{ providers: ProviderRow[] }>("/api/v1/dns-providers"),
          apiFetch<{ nodes: NodeRecord[] }>("/api/v1/nodes"),
        ]);
        if (cancelled) return;
        setCerts(c.certificates);
        setProviders(p.providers);
        setNodes(n.nodes);
      } catch (err) {
        if (!cancelled) setError(isApiError(err) ? err.message : t("common.error.generic"));
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [t]);

  const providersById = useMemo(() => {
    const m = new Map<string, ProviderRow>();
    for (const p of providers ?? []) m.set(p.id, p);
    return m;
  }, [providers]);

  function wizardErrorText(err: unknown): string {
    const msg = err instanceof Error ? err.message : t("ssl.err.generic");
    if (msg.includes("rate_limit")) {
      return t("ssl.err.rate_limit", { secs: "—" });
    }
    if (msg.includes("challenge")) return t("ssl.err.challenge");
    return msg;
  }

  async function issueWizard(e: { preventDefault: () => void }) {
    e.preventDefault();
    setWizardError(null);
    if (!nodeId || !providerId) {
      setWizardError(t("ssl.wizard.node") + " / " + t("ssl.wizard.provider"));
      return;
    }
    if (!DOMAIN_PATTERN.test(domain.trim())) {
      setWizardError(t("ssl.wizard.domain"));
      return;
    }
    const sans = sansInput
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);
    for (const s of sans) {
      if (!DOMAIN_PATTERN.test(s)) {
        setWizardError(t("ssl.wizard.sans"));
        return;
      }
    }
    setWizardBusy(true);
    try {
      // 1) web.cert.request — anahtar + CSR sunucuda üretilir.
      setStep("request");
      const reqTask = await runTask(
        "web.cert.request",
        { domain: domain.trim(), sans },
        nodeId,
        t("common.error.generic"),
      );
      const reqResult = (reqTask.result ?? {}) as {
        request?: { csr_pem?: string; sans?: string[] };
      };
      const csrPem = reqResult.request?.csr_pem ?? "";
      if (!csrPem) throw new Error(t("ssl.err.generic"));

      // 2) cert.issue — ACME DNS-01 (control-plane).
      setStep("issue");
      const issueTask = await runTask(
        "cert.issue",
        {
          name: domain.trim(),
          sans,
          provider_id: providerId,
          node_id: nodeId,
          csr_pem: csrPem,
        },
        null,
        t("common.error.generic"),
      );
      const issueResult = (issueTask.result ?? {}) as {
        cert_pem?: string;
        chain_pem?: string;
      };
      if (!issueResult.cert_pem) throw new Error(t("ssl.err.generic"));

      // 3) web.cert.install — nginx -t kapısı + atomik takas + reload.
      setStep("install");
      await runTask(
        "web.cert.install",
        { domain: domain.trim(), cert_pem: issueResult.cert_pem, chain_pem: issueResult.chain_pem ?? "" },
        nodeId,
        t("common.error.generic"),
      );
      setIssuedCert({
        certPem: issueResult.cert_pem,
        chainPem: issueResult.chain_pem ?? "",
        nodeId,
      });
      setStep("done");
      await reload();
    } catch (err) {
      setWizardError(wizardErrorText(err));
    } finally {
      setWizardBusy(false);
    }
  }

  async function renewCert(cert: CertRecord) {
    setRenewBusy(cert.id);
    setWizardError(null);
    try {
      const task = await runTask("cert.renew", { cert_id: cert.id }, null, t("common.error.generic"));
      const result = (task.result ?? {}) as { cert_pem?: string; chain_pem?: string };
      // Başarılı yenilemeden sonra sunucuya kur (canlı sertifika yalnız
      // doğrulanmış yeni materyalle değişir; başarısızlıkta dokunulmaz).
      if (result.cert_pem && cert.node_id) {
        await runTask(
          "web.cert.install",
          { domain: cert.name, cert_pem: result.cert_pem, chain_pem: result.chain_pem ?? "" },
          cert.node_id,
          t("common.error.generic"),
        );
      }
      await reload();
    } catch (err) {
      setWizardError(wizardErrorText(err));
    } finally {
      setRenewBusy(null);
    }
  }

  async function revokeCert(cert: CertRecord) {
    setRevokeBusy(cert.id);
    setWizardError(null);
    try {
      await runTask("cert.revoke", { cert_id: cert.id, reason: revokeReason }, null, t("common.error.generic"));
      await reload();
    } catch (err) {
      setWizardError(wizardErrorText(err));
    } finally {
      setRevokeBusy(null);
    }
  }

  return (
    <div className="flex flex-col gap-4 p-6">
      <div>
        <p className="text-[11px] text-ink-soft">{t("nav.group.hosting")}</p>
        <h1 className="text-xl font-semibold text-ink">{t("ssl.title")}</h1>
        <p className="mt-1 max-w-3xl text-xs leading-relaxed text-ink-soft">{t("ssl.note")}</p>
      </div>

      {error && (
        <p role="alert" className="rounded-lg bg-err-soft px-3 py-2 text-xs text-err">
          {error}
        </p>
      )}
      {wizardError && (
        <p role="alert" className="rounded-lg bg-err-soft px-3 py-2 text-xs text-err">
          {wizardError}
        </p>
      )}

      {/* ---- Sertifika listesi ---- */}
      <section aria-label={t("ssl.list.title")} className="rounded-xl border border-border bg-panel shadow-sm">
        <div className="border-b border-border px-4 py-3">
          <h2 className="text-sm font-semibold text-ink">{t("ssl.list.title")}</h2>
        </div>
        {certs === null ? (
          <p className="p-6 text-sm text-ink-soft">{t("common.loading")}</p>
        ) : certs.length === 0 ? (
          <EmptyState title={t("ssl.list.empty")} description={t("ssl.list.emptyHint")} />
        ) : (
          <table className="w-full text-left text-sm">
            <caption className="sr-only">{t("ssl.list.title")}</caption>
            <thead>
              <tr className="border-b border-border text-xs uppercase tracking-wide text-ink-soft">
                <th scope="col" className="px-4 py-2 font-medium">{t("ssl.col.name")}</th>
                <th scope="col" className="px-4 py-2 font-medium">{t("ssl.col.state")}</th>
                <th scope="col" className="px-4 py-2 font-medium">{t("ssl.col.provider")}</th>
                <th scope="col" className="px-4 py-2 font-medium">{t("ssl.col.expires")}</th>
                <th scope="col" className="px-4 py-2 font-medium">{t("ssl.col.lastError")}</th>
                <th scope="col" className="px-4 py-2 font-medium"><span className="sr-only">/</span></th>
              </tr>
            </thead>
            <tbody>
              {certs.map((c) => (
                <tr key={c.id} className="border-b border-border/60 last:border-0">
                  <td className="px-4 py-2.5">
                    <p className="font-medium text-ink">{c.name}</p>
                    {c.sans.length > 1 && (
                      <p className="text-[11px] text-ink-soft">+{c.sans.length - 1} SAN</p>
                    )}
                  </td>
                  <td className="px-4 py-2.5">
                    <StatusBadge variant={stateVariant[c.effective_state ?? c.state] ?? "neutral"}>
                      {t(`ssl.state.${c.effective_state ?? c.state}` as Parameters<typeof t>[0])}
                    </StatusBadge>
                  </td>
                  <td className="px-4 py-2.5 text-xs text-ink-soft">
                    {providersById.get(c.provider_id)?.name ?? "—"}
                  </td>
                  <td className="px-4 py-2.5 text-xs">
                    {c.not_after ? (
                      <>
                        {t("ssl.expires.days", { days: c.expires_in_days ?? 0 })}
                        {c.expiring_soon && (
                          <span className="ml-1 rounded-full bg-warn-soft px-1.5 py-0.5 text-[10px] text-warn">
                            {t("ssl.expires.expiringSoon")}
                          </span>
                        )}
                      </>
                    ) : (
                      "—"
                    )}
                  </td>
                  <td className="max-w-[16rem] px-4 py-2.5 text-[11px] text-err">
                    {c.last_error ? (
                      <>
                        <span className="font-medium">{c.last_error_class}</span>
                        {c.last_error_class === "rate_limit" && c.retry_after_secs ? (
                          <> · Retry-After: {c.retry_after_secs}s</>
                        ) : null}
                      </>
                    ) : (
                      "—"
                    )}
                  </td>
                  <td className="whitespace-nowrap px-4 py-2.5 text-right">
                    <button
                      type="button"
                      onClick={() => void renewCert(c)}
                      disabled={renewBusy === c.id}
                      className="mr-1 rounded-md border border-border px-2 py-1 text-xs hover:bg-surface disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent"
                    >
                      {t("ssl.renew")}
                    </button>
                    <button
                      type="button"
                      onClick={() => void revokeCert(c)}
                      disabled={revokeBusy === c.id || (c.effective_state ?? c.state) === "revoked"}
                      className="rounded-md border border-border px-2 py-1 text-xs text-err hover:bg-err-soft disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent"
                    >
                      {t("ssl.revoke")}
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        {certs && certs.length > 0 && (
          <div className="flex items-center gap-2 border-t border-border px-4 py-2 text-xs text-ink-soft">
            {t("ssl.revokeReason")}
            <select
              aria-label={t("ssl.revokeReason")}
              value={revokeReason}
              onChange={(e) => setRevokeReason(Number(e.target.value))}
              className="rounded-md border border-border bg-surface px-2 py-1 text-xs"
            >
              <option value={0}>{t("ssl.reason.0")}</option>
              <option value={1}>{t("ssl.reason.1")}</option>
              <option value={4}>{t("ssl.reason.4")}</option>
              <option value={5}>{t("ssl.reason.5")}</option>
            </select>
          </div>
        )}
      </section>

      {/* ---- Üretim sihirbazı ---- */}
      <section aria-label={t("ssl.wizard.title")} className="rounded-xl border border-border bg-panel shadow-sm">
        <div className="border-b border-border px-4 py-3">
          <h2 className="text-sm font-semibold text-ink">{t("ssl.wizard.title")}</h2>
        </div>
        <form onSubmit={issueWizard} className="flex flex-wrap items-end gap-3 px-4 py-3">
          <label className="text-xs text-ink-soft">
            {t("ssl.wizard.node")}
            <select
              value={nodeId}
              onChange={(e) => setNodeId(e.target.value)}
              className="mt-1 block w-40 rounded-md border border-border bg-surface px-2 py-1.5 text-sm text-ink"
            >
              <option value="">—</option>
              {(nodes ?? []).map((n) => (
                <option key={n.id} value={n.id}>
                  {n.display_name}
                </option>
              ))}
            </select>
          </label>
          <label className="text-xs text-ink-soft">
            {t("ssl.wizard.provider")}
            <select
              value={providerId}
              onChange={(e) => setProviderId(e.target.value)}
              className="mt-1 block w-44 rounded-md border border-border bg-surface px-2 py-1.5 text-sm text-ink"
            >
              <option value="">—</option>
              {(providers ?? []).map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name} ({p.kind})
                </option>
              ))}
            </select>
          </label>
          <label className="text-xs text-ink-soft">
            {t("ssl.wizard.domain")}
            <input
              value={domain}
              onChange={(e) => setDomain(e.target.value)}
              placeholder="example.com"
              className="mt-1 block w-48 rounded-md border border-border bg-surface px-2 py-1.5 text-sm text-ink"
            />
          </label>
          <label className="text-xs text-ink-soft">
            {t("ssl.wizard.sans")}
            <input
              value={sansInput}
              onChange={(e) => setSansInput(e.target.value)}
              placeholder="www.example.com, app.example.com"
              className="mt-1 block w-64 rounded-md border border-border bg-surface px-2 py-1.5 text-sm text-ink"
            />
          </label>
          <button
            type="submit"
            disabled={wizardBusy}
            className="rounded-md bg-accent px-3 py-1.5 text-xs font-medium text-white disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent"
          >
            {t("ssl.wizard.issue")}
          </button>
        </form>
        <ol className="flex flex-wrap gap-2 border-t border-border px-4 py-3 text-[11px]">
          {(["request", "issue", "install"] as const).map((s) => (
            <li
              key={s}
              className={`rounded-full px-2 py-0.5 ${
                step === s
                  ? "bg-accent-soft font-medium text-accent"
                  : (["request", "issue", "install"].indexOf(step) > ["request", "issue", "install"].indexOf(s)) || step === "done"
                    ? "bg-ok-soft text-ok"
                    : "bg-surface text-ink-soft"
              }`}
            >
              {t(`ssl.wizard.step${s[0].toUpperCase()}${s.slice(1)}` as Parameters<typeof t>[0])}
            </li>
          ))}
          {step === "done" && <li className="rounded-full bg-ok-soft px-2 py-0.5 text-ok">{t("ssl.wizard.done")}</li>}
        </ol>
        {issuedCert && (
          <p className="border-t border-border px-4 py-2 text-[11px] text-ink-soft">
            {t("ssl.wizard.installAfterIssue")} ✓ · {issuedCert.certPem.length} bytes PEM
          </p>
        )}
      </section>
    </div>
  );
}
