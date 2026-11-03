import { useCallback, useEffect, useState } from "react";
import type { FormEvent } from "react";
import { apiFetch, isApiError } from "../api/client";
import type { GroupRecord, NodePage } from "../api/client";
import { CopyButton } from "../components/CopyButton";
import { EmptyState } from "../components/EmptyState";
import { StatusBadge } from "../components/StatusBadge";
import type { BadgeVariant } from "../components/StatusBadge";
import { useI18n } from "../i18n/LocaleProvider";

// EnrollmentToken is the API view (hashes never leave the server).
export interface EnrollmentToken {
  id: string;
  scope_type: "node" | "group";
  node_id?: string | null;
  group_id?: string | null;
  created_by: string;
  expires_at: string;
  consumed_at?: string | null;
  revoked_at?: string | null;
  created_at: string;
}

function tokenState(t: EnrollmentToken): "pending" | "consumed" | "revoked" | "expired" {
  if (t.revoked_at) return "revoked";
  if (t.consumed_at) return "consumed";
  if (new Date(t.expires_at).getTime() < Date.now()) return "expired";
  return "pending";
}

export function EnrollmentPage() {
  const { t } = useI18n();
  const [tokens, setTokens] = useState<EnrollmentToken[] | null>(null);
  const [nodes, setNodes] = useState<{ id: string; display_name: string }[]>([]);
  const [groups, setGroups] = useState<GroupRecord[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [reloadKey, setReloadKey] = useState(0);
  const [revealed, setRevealed] = useState<{ plaintext: string; expires_at: string } | null>(null);

  const reload = useCallback(() => setReloadKey((k) => k + 1), []);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const [list, nodeList, groupList] = await Promise.all([
          apiFetch<{ tokens: EnrollmentToken[] }>("/api/v1/agent-enrollments"),
          apiFetch<NodePage>("/api/v1/nodes?limit=200"),
          apiFetch<{ groups: GroupRecord[] }>("/api/v1/node-groups"),
        ]);
        if (cancelled) return;
        setTokens(list.tokens);
        setNodes(nodeList.nodes.map((n) => ({ id: n.id, display_name: n.display_name })));
        setGroups(groupList.groups);
        setError(null);
      } catch (err) {
        if (!cancelled) {
          setError(isApiError(err) ? err.message : t("common.error.generic"));
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [reloadKey, t]);

  async function issueToken(payload: {
    scope_type: "node" | "group";
    node_id?: string;
    group_id?: string;
    ttl_minutes: number;
  }): Promise<boolean> {
    try {
      const res = await apiFetch<{ plaintext: string; expires_at: string }>(
        "/api/v1/agent-enrollments",
        { method: "POST", body: JSON.stringify(payload) },
      );
      setRevealed(res);
      reload();
      return true;
    } catch (err) {
      setError(isApiError(err) ? err.message : t("common.error.generic"));
      return false;
    }
  }

  async function revokeToken(id: string) {
    setError(null);
    try {
      await apiFetch(`/api/v1/agent-enrollments/${id}/revoke`, { method: "POST" });
      reload();
    } catch (err) {
      setError(isApiError(err) ? err.message : t("common.error.generic"));
    }
  }

  const loading = tokens === null && error === null;

  return (
    <div className="flex flex-col gap-4 p-6">
      <div>
        <p className="text-[11px] text-ink-soft">{t("nav.group.settings")}</p>
        <h1 className="text-xl font-semibold text-ink">{t("nav.item.enrollment")}</h1>
      </div>

      {error && (
        <p role="alert" className="rounded-lg bg-err-soft px-3 py-2 text-xs text-err">
          {error}
        </p>
      )}

      <TokenIssueForm nodes={nodes} groups={groups} onIssue={issueToken} />

      {revealed && (
        <section
          aria-label={t("enrollment.reveal.title")}
          className="rounded-xl border border-warn/40 bg-warn-soft p-4"
        >
          <h2 className="text-sm font-semibold text-ink">{t("enrollment.reveal.title")}</h2>
          <p className="mt-1 text-xs text-ink-soft">{t("enrollment.reveal.description")}</p>
          <div className="mt-2 flex items-stretch gap-2">
            <code className="min-w-0 flex-1 overflow-x-auto rounded-lg bg-surface px-3 py-2 text-xs text-ink">
              {revealed.plaintext}
            </code>
            <CopyButton value={revealed.plaintext} className="shrink-0 self-center" />
          </div>
          <p className="mt-2 text-[11px] text-ink-soft">
            {t("enrollment.reveal.command", {
              expires: new Date(revealed.expires_at).toLocaleString(),
            })}
          </p>
          <button
            type="button"
            onClick={() => setRevealed(null)}
            className="mt-2 rounded-lg border border-border bg-panel px-3 py-1.5 text-xs font-medium text-ink hover:bg-neutral-soft focus-visible:outline-2 focus-visible:outline-accent"
          >
            {t("enrollment.reveal.dismiss")}
          </button>
        </section>
      )}

      <section aria-label={t("nav.item.enrollment")} className="rounded-xl border border-border bg-panel shadow-sm">
        {loading ? (
          <p className="p-6 text-sm text-ink-soft">{t("common.loading")}</p>
        ) : !tokens || tokens.length === 0 ? (
          <EmptyState
            title={t("enrollment.empty.title")}
            description={t("enrollment.empty.description")}
          />
        ) : (
          <table className="w-full text-left text-sm">
            <caption className="sr-only">{t("enrollment.table.caption")}</caption>
            <thead>
              <tr className="border-b border-border text-xs uppercase tracking-wide text-ink-soft">
                <th scope="col" className="px-4 py-2 font-medium">{t("enrollment.col.scope")}</th>
                <th scope="col" className="px-4 py-2 font-medium">{t("enrollment.col.target")}</th>
                <th scope="col" className="px-4 py-2 font-medium">{t("enrollment.col.status")}</th>
                <th scope="col" className="px-4 py-2 font-medium">{t("enrollment.col.expires")}</th>
                <th scope="col" className="px-4 py-2 font-medium"><span className="sr-only">{t("enrollment.col.actions")}</span></th>
              </tr>
            </thead>
            <tbody>
              {tokens.map((tok) => {
                const st = tokenState(tok);
                return (
                  <tr key={tok.id} className="border-b border-border/60 last:border-0">
                    <td className="px-4 py-2.5 text-ink">{t(`enrollment.scope.${tok.scope_type}`)}</td>
                    <td className="px-4 py-2.5">
                      <code className="text-[11px] text-ink-soft">
                        {(tok.node_id ?? tok.group_id ?? "").slice(0, 12)}
                      </code>
                    </td>
                    <td className="px-4 py-2.5">
                      <StatusBadge variant={stateVariant[st]}>{t(`enrollment.state.${st}`)}</StatusBadge>
                    </td>
                    <td className="px-4 py-2.5 text-xs text-ink-soft">
                      {new Date(tok.expires_at).toLocaleString()}
                    </td>
                    <td className="px-4 py-2.5 text-right">
                      {st === "pending" && (
                        <button
                          type="button"
                          onClick={() => void revokeToken(tok.id)}
                          className="rounded-lg border border-border px-2 py-1 text-xs font-medium text-ink hover:bg-neutral-soft focus-visible:outline-2 focus-visible:outline-accent"
                        >
                          {t("enrollment.revoke")}
                        </button>
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </section>

      <p className="text-[11px] text-ink-soft">{t("enrollment.securityNote")}</p>
    </div>
  );
}

const stateVariant: Record<"pending" | "consumed" | "revoked" | "expired", BadgeVariant> = {
  pending: "info",
  consumed: "ok",
  revoked: "neutral",
  expired: "warn",
};

function TokenIssueForm({
  nodes,
  groups,
  onIssue,
}: {
  nodes: { id: string; display_name: string }[];
  groups: GroupRecord[];
  onIssue: (payload: {
    scope_type: "node" | "group";
    node_id?: string;
    group_id?: string;
    ttl_minutes: number;
  }) => Promise<boolean>;
}) {
  const { t } = useI18n();
  const [scope, setScope] = useState<"node" | "group">("group");
  const [nodeId, setNodeId] = useState("");
  const [groupId, setGroupId] = useState("");
  const [ttl, setTtl] = useState(15);
  const [pending, setPending] = useState(false);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (pending) return;
    const payload: {
      scope_type: "node" | "group";
      node_id?: string;
      group_id?: string;
      ttl_minutes: number;
    } = { scope_type: scope, ttl_minutes: ttl };
    if (scope === "node") {
      if (!nodeId) return;
      payload.node_id = nodeId;
    } else {
      if (!groupId) return;
      payload.group_id = groupId;
    }
    setPending(true);
    const ok = await onIssue(payload);
    setPending(false);
    if (ok) {
      setNodeId("");
      setGroupId("");
    }
  }

  return (
    <form onSubmit={onSubmit} className="flex flex-col gap-3 rounded-xl border border-border bg-panel p-4 shadow-sm">
      <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
        <label className="flex flex-col gap-1">
          <span className="text-xs font-medium text-ink">{t("enrollment.form.scope")}</span>
          <select
            value={scope}
            onChange={(e) => setScope(e.target.value as "node" | "group")}
            className="rounded-lg border border-border bg-surface px-3 py-2 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
          >
            <option value="group">{t("enrollment.scope.group")}</option>
            <option value="node">{t("enrollment.scope.node")}</option>
          </select>
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-xs font-medium text-ink">
            {scope === "node" ? t("enrollment.form.node") : t("enrollment.form.group")}
          </span>
          {scope === "node" ? (
            <select
              value={nodeId}
              onChange={(e) => setNodeId(e.target.value)}
              className="rounded-lg border border-border bg-surface px-3 py-2 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
            >
              <option value="">{t("enrollment.form.pick")}</option>
              {nodes.map((n) => (
                <option key={n.id} value={n.id}>{n.display_name}</option>
              ))}
            </select>
          ) : (
            <select
              value={groupId}
              onChange={(e) => setGroupId(e.target.value)}
              className="rounded-lg border border-border bg-surface px-3 py-2 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
            >
              <option value="">{t("enrollment.form.pick")}</option>
              {groups.map((g) => (
                <option key={g.id} value={g.id}>{g.name}</option>
              ))}
            </select>
          )}
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-xs font-medium text-ink">{t("enrollment.form.ttl")}</span>
          <select
            value={ttl}
            onChange={(e) => setTtl(Number(e.target.value))}
            className="rounded-lg border border-border bg-surface px-3 py-2 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
          >
            <option value={5}>5 {t("enrollment.form.minutes")}</option>
            <option value={15}>15 {t("enrollment.form.minutes")}</option>
            <option value={60}>60 {t("enrollment.form.minutes")}</option>
          </select>
        </label>
      </div>
      <div>
        <button
          type="submit"
          disabled={pending || (scope === "node" ? !nodeId : !groupId)}
          className="rounded-lg bg-accent px-4 py-2 text-sm font-medium text-white hover:bg-accent/90 focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-50"
        >
          {pending ? t("common.saving") : t("enrollment.form.submit")}
        </button>
      </div>
    </form>
  );
}
