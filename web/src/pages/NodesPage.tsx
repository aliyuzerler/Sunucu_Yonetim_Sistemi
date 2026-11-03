import { useCallback, useEffect, useState } from "react";
import type { FormEvent } from "react";
import { apiFetch, isApiError } from "../api/client";
import type { GroupRecord, NodePage, NodeRecord } from "../api/client";
import { NodeDetail } from "./NodeDetail";
import { EmptyState } from "../components/EmptyState";
import { StatusBadge } from "../components/StatusBadge";
import type { BadgeVariant } from "../components/StatusBadge";
import { useI18n } from "../i18n/LocaleProvider";

const statusVariant: Record<NodeRecord["status"], BadgeVariant> = {
  pending: "info",
  active: "ok",
  offline: "warn",
  retired: "neutral",
};

export function NodesPage({
  routeNodeId,
  onOpenNode,
  onNodeClosed,
}: {
  /** UX denetimi m.4.2: #/nodes/<id> derin bağlantısından gelen node kimliği. */
  routeNodeId?: string | null;
  /** Satır detail tıklamasında URL'i güncelleyen geri çağrı (App → hash). */
  onOpenNode?: (id: string) => void;
  /** Detay kapatıldığında URL'i #/nodes'a döndüren geri çağrı. */
  onNodeClosed?: () => void;
}) {
  const { t } = useI18n();
  const [page, setPage] = useState<NodePage | null>(null);
  const [groups, setGroups] = useState<GroupRecord[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [showForm, setShowForm] = useState(false);
  const [fetchedNode, setFetchedNode] = useState<NodeRecord | null>(null);
  const [reloadKey, setReloadKey] = useState(0);

  // m.4.2: URL tek doğruluk kaynağıdır — seçim, derin bağlantıdan
  // türetilir; geri/ileri ve elle hash düzenlemesi otomatik çalışır.
  const selectedNodeID = routeNodeId ?? null;

  const reload = useCallback(() => setReloadKey((k) => k + 1), []);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const [nodes, groupList] = await Promise.all([
          apiFetch<NodePage>("/api/v1/nodes?limit=100"),
          apiFetch<{ groups: GroupRecord[] }>("/api/v1/node-groups"),
        ]);
        if (cancelled) return;
        setPage(nodes);
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

  const loading = page === null && error === null;

  // m.4.2: Derin bağlantıdaki node ilk sayfada (limit=100) yoksa tekil
  // uç noktadan çekilir. Bulunamazsa dürüstçe liste görünümünde kalınır.
  // setFetchedNode yalnız ASYNC geri çağrıda çağrılır (cascading render yok);
  // eski sonuç, id eşleşmesiyle türetilerek doğal olarak geçersizleşir.
  useEffect(() => {
    if (!selectedNodeID || !page) return;
    if (page.nodes.some((n) => n.id === selectedNodeID)) return;
    let cancelled = false;
    (async () => {
      try {
        const n = await apiFetch<NodeRecord>(
          `/api/v1/nodes/${encodeURIComponent(selectedNodeID)}`,
        );
        if (!cancelled) setFetchedNode(n);
      } catch {
        if (!cancelled) setFetchedNode(null);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [selectedNodeID, page]);

  async function createGroup(name: string): Promise<boolean> {
    try {
      await apiFetch("/api/v1/node-groups", {
        method: "POST",
        body: JSON.stringify({ name, description: "" }),
      });
      reload();
      return true;
    } catch (err) {
      setError(isApiError(err) ? err.message : t("common.error.generic"));
      return false;
    }
  }

  async function createNode(payload: {
    display_name: string;
    hostname: string;
    os_family: NodeRecord["os_family"];
    tags: string[];
    group_ids: string[];
  }): Promise<boolean> {
    try {
      await apiFetch("/api/v1/nodes", { method: "POST", body: JSON.stringify(payload) });
      reload();
      return true;
    } catch (err) {
      setError(isApiError(err) ? err.message : t("common.error.generic"));
      return false;
    }
  }

  const selectedNode =
    page?.nodes.find((n) => n.id === selectedNodeID) ??
    (fetchedNode && fetchedNode.id === selectedNodeID ? fetchedNode : null);

  return (
    <div className="flex flex-col gap-4 p-6">
      <div className="flex items-center justify-between">
        <div>
          <p className="text-[11px] text-ink-soft">{t("nav.group.infrastructure")}</p>
          <h1 className="text-xl font-semibold text-ink">{t("nav.item.nodes")}</h1>
        </div>
        <button
          type="button"
          onClick={() => setShowForm((v) => !v)}
          aria-expanded={showForm}
          className="rounded-lg bg-accent px-3 py-1.5 text-xs font-medium text-white hover:bg-accent/90 focus-visible:outline-2 focus-visible:outline-accent"
        >
          {showForm ? t("common.cancel") : t("nodes.create")}
        </button>
      </div>

      {error && (
        <p role="alert" className="rounded-lg bg-err-soft px-3 py-2 text-xs text-err">
          {error}
        </p>
      )}

      {selectedNode && (
        <NodeDetail
          node={selectedNode}
          onBack={() => {
            onNodeClosed?.();
          }}
        />
      )}

      {!selectedNode && showForm && (
        <NodeCreateForm
          groups={groups}
          onCreateNode={createNode}
          onCreateGroup={createGroup}
          onDone={() => {
            setShowForm(false);
            reload();
          }}
        />
      )}

      <section aria-label={t("nav.item.nodes")} className="rounded-xl border border-border bg-panel shadow-sm" hidden={selectedNode !== null}>
        {loading ? (
          <p className="p-6 text-sm text-ink-soft">{t("common.loading")}</p>
        ) : !page || page.nodes.length === 0 ? (
          <EmptyState
            title={t("nodes.empty.title")}
            description={t("nodes.empty.description")}
          />
        ) : (
          <table className="w-full text-left text-sm">
            <caption className="sr-only">{t("nodes.table.caption")}</caption>
            <thead>
              <tr className="border-b border-border text-xs uppercase tracking-wide text-ink-soft">
                <th scope="col" className="px-4 py-2 font-medium">{t("nodes.col.name")}</th>
                <th scope="col" className="px-4 py-2 font-medium">{t("nodes.col.os")}</th>
                <th scope="col" className="px-4 py-2 font-medium">{t("nodes.col.status")}</th>
                <th scope="col" className="px-4 py-2 font-medium">{t("nodes.col.tags")}</th>
                <th scope="col" className="px-4 py-2 font-medium"><span className="sr-only">{t("nodes.detail")}</span></th>
              </tr>
            </thead>
            <tbody>
              {page.nodes.map((n) => (
                <tr key={n.id} className="border-b border-border/60 last:border-0">
                  <td className="px-4 py-2.5">
                    <p className="font-medium text-ink">{n.display_name}</p>
                    {n.hostname && <p className="text-[11px] text-ink-soft">{n.hostname}</p>}
                  </td>
                  <td className="px-4 py-2.5 text-ink-soft">
                    {n.os_family}
                    {n.os_version ? ` · ${n.os_version}` : ""}
                  </td>
                  <td className="px-4 py-2.5">
                    <StatusBadge variant={statusVariant[n.status]}>{t(`nodes.status.${n.status}` as TranslationNodeStatus)}</StatusBadge>
                  </td>
                  <td className="px-4 py-2.5">
                    <div className="flex flex-wrap gap-1">
                      {n.tags.length === 0 ? (
                        <span className="text-xs text-ink-soft">—</span>
                      ) : (
                        n.tags.map((tag) => (
                          <span key={tag} className="rounded-full bg-neutral-soft px-2 py-0.5 text-[11px] text-ink-soft">
                            {tag}
                          </span>
                        ))
                      )}
                    </div>
                  </td>
                  <td className="px-4 py-2.5">
                    <button
                      type="button"
                      onClick={() => onOpenNode?.(n.id)}
                      className="rounded-lg border border-border px-2.5 py-1 text-[11px] text-ink hover:bg-neutral-soft focus-visible:outline-2 focus-visible:outline-accent"
                    >
                      {t("nodes.detail")}
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </section>

      <p className="text-[11px] text-ink-soft">{t("nodes.agentNote")}</p>
    </div>
  );
}

type TranslationNodeStatus = `nodes.status.${NodeRecord["status"]}`;

function NodeCreateForm({
  groups,
  onCreateNode,
  onCreateGroup,
  onDone,
}: {
  groups: GroupRecord[];
  onCreateNode: (payload: {
    display_name: string;
    hostname: string;
    os_family: NodeRecord["os_family"];
    tags: string[];
    group_ids: string[];
  }) => Promise<boolean>;
  onCreateGroup: (name: string) => Promise<boolean>;
  onDone: () => void;
}) {
  const { t } = useI18n();
  const [displayName, setDisplayName] = useState("");
  const [hostname, setHostname] = useState("");
  const [osFamily, setOsFamily] = useState<NodeRecord["os_family"]>("linux");
  const [tags, setTags] = useState("");
  const [selectedGroups, setSelectedGroups] = useState<string[]>([]);
  const [newGroup, setNewGroup] = useState("");
  const [pending, setPending] = useState(false);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    if (pending || !displayName.trim()) return;
    setPending(true);
    const ok = await onCreateNode({
      display_name: displayName.trim(),
      hostname: hostname.trim(),
      os_family: osFamily,
      tags: tags.split(",").map((s) => s.trim().toLowerCase()).filter(Boolean),
      group_ids: selectedGroups,
    });
    setPending(false);
    if (ok) {
      setDisplayName("");
      setHostname("");
      setTags("");
      setSelectedGroups([]);
      onDone();
    }
  }

  async function submitGroup() {
    const name = newGroup.trim().toLowerCase();
    if (!name || pending) return;
    setPending(true);
    const ok = await onCreateGroup(name);
    setPending(false);
    if (ok) setNewGroup("");
  }

  return (
    <form onSubmit={onSubmit} className="flex flex-col gap-3 rounded-xl border border-border bg-panel p-4 shadow-sm">
      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        <label className="flex flex-col gap-1">
          <span className="text-xs font-medium text-ink">{t("nodes.form.name")}</span>
          <input
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
            required
            maxLength={128}
            className="rounded-lg border border-border bg-surface px-3 py-2 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
          />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-xs font-medium text-ink">{t("nodes.form.hostname")}</span>
          <input
            value={hostname}
            onChange={(e) => setHostname(e.target.value)}
            maxLength={253}
            className="rounded-lg border border-border bg-surface px-3 py-2 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
          />
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-xs font-medium text-ink">{t("nodes.form.os")}</span>
          <select
            value={osFamily}
            onChange={(e) => setOsFamily(e.target.value as NodeRecord["os_family"])}
            className="rounded-lg border border-border bg-surface px-3 py-2 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
          >
            <option value="linux">linux</option>
            <option value="windows">windows</option>
            <option value="unknown">{t("nodes.form.osUnknown")}</option>
          </select>
        </label>
        <label className="flex flex-col gap-1">
          <span className="text-xs font-medium text-ink">{t("nodes.form.tags")}</span>
          <input
            value={tags}
            onChange={(e) => setTags(e.target.value)}
            placeholder="prod, web"
            className="rounded-lg border border-border bg-surface px-3 py-2 text-sm text-ink focus-visible:outline-2 focus-visible:outline-accent"
          />
        </label>
      </div>

      <fieldset className="rounded-lg border border-border p-3">
        <legend className="px-1 text-xs font-medium text-ink">{t("nodes.form.groups")}</legend>
        {groups.length === 0 ? (
          <p className="text-xs text-ink-soft">{t("nodes.form.noGroups")}</p>
        ) : (
          <div className="flex flex-wrap gap-2">
            {groups.map((g) => (
              <label key={g.id} className="flex items-center gap-1.5 text-xs text-ink">
                <input
                  type="checkbox"
                  checked={selectedGroups.includes(g.id)}
                  onChange={(e) =>
                    setSelectedGroups((prev) =>
                      e.target.checked ? [...prev, g.id] : prev.filter((id) => id !== g.id),
                    )
                  }
                />
                {g.name}
              </label>
            ))}
          </div>
        )}
        <div className="mt-2 flex gap-2">
          <input
            value={newGroup}
            onChange={(e) => setNewGroup(e.target.value)}
            placeholder={t("nodes.form.newGroupPlaceholder")}
            maxLength={64}
            className="flex-1 rounded-lg border border-border bg-surface px-3 py-1.5 text-xs text-ink focus-visible:outline-2 focus-visible:outline-accent"
          />
          <button
            type="button"
            onClick={submitGroup}
            disabled={!newGroup.trim()}
            className="rounded-lg border border-border px-3 py-1.5 text-xs font-medium text-ink hover:bg-neutral-soft focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-50"
          >
            {t("nodes.form.createGroup")}
          </button>
        </div>
      </fieldset>

      <div>
        <button
          type="submit"
          disabled={pending || !displayName.trim()}
          className="rounded-lg bg-accent px-4 py-2 text-sm font-medium text-white hover:bg-accent/90 focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-50"
        >
          {pending ? t("common.saving") : t("nodes.form.submit")}
        </button>
      </div>
    </form>
  );
}
