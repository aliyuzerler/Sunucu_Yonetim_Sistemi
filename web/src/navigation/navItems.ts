import type { TranslationKey } from "../i18n/tr";

export interface NavItem {
  id: string;
  key: TranslationKey;
}

export interface NavGroup {
  id: string;
  key: TranslationKey;
  items: NavItem[];
}

// Navigation structure mirrors docs/ui-ux/navigation.md exactly.
export const navGroups: NavGroup[] = [
  {
    id: "overview",
    key: "nav.group.overview",
    items: [{ id: "overview", key: "nav.item.overview" }],
  },
  {
    id: "infrastructure",
    key: "nav.group.infrastructure",
    items: [
      { id: "nodes", key: "nav.item.nodes" },
      { id: "linux", key: "nav.item.linux" },
      { id: "windows", key: "nav.item.windows" },
      { id: "docker", key: "nav.item.docker" },
      { id: "vms", key: "nav.item.vms" },
    ],
  },
  {
    id: "hosting",
    key: "nav.group.hosting",
    items: [
      { id: "sites", key: "nav.item.sites" },
      { id: "dns", key: "nav.item.dns" },
      { id: "ssl", key: "nav.item.ssl" },
      { id: "databases", key: "nav.item.databases" },
      { id: "email", key: "nav.item.email" },
    ],
  },
  {
    id: "operations",
    key: "nav.group.operations",
    items: [
      { id: "monitoring", key: "nav.item.monitoring" },
      { id: "logs", key: "nav.item.logs" },
      { id: "tasks", key: "nav.item.tasks" },
      { id: "audit", key: "nav.item.audit" },
      { id: "backups", key: "nav.item.backups" },
      { id: "security", key: "nav.item.security" },
    ],
  },
  {
    id: "settings",
    key: "nav.group.settings",
    items: [
      { id: "users", key: "nav.item.users" },
      { id: "enrollment", key: "nav.item.enrollment" },
      { id: "integrations", key: "nav.item.integrations" },
      { id: "modules", key: "nav.item.modules" },
      { id: "updates", key: "nav.item.updates" },
      { id: "system", key: "nav.item.system" },
    ],
  },
];

// Roadmap phase per module id — drives the honest "not implemented yet" chips
// and the ComingSoon page. 0 means available in the Faz 02 application.
export const modulePhase: Record<string, number> = {
  overview: 0,
  nodes: 0,
  linux: 4,
  windows: 5,
  docker: 7,
  vms: 12,
  sites: 8,
  dns: 9,
  ssl: 9,
  databases: 10,
  email: 11,
  monitoring: 13,
  logs: 13,
  tasks: 0,
  audit: 0,
  backups: 14,
  security: 13,
  users: 0,
  enrollment: 0,
  integrations: 14,
  modules: 15,
  updates: 15,
  system: 1,
};
