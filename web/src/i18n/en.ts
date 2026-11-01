import type { TranslationKey } from "./tr";

// English dictionary. Record<TranslationKey, string> enforces key parity with
// the canonical Turkish dictionary at compile time.
export const en: Record<TranslationKey, string> = {
  "app.title": "Server Control",
  "app.subtitle": "Infrastructure control plane",

  "nav.group.overview": "Overview",
  "nav.group.infrastructure": "Infrastructure",
  "nav.group.hosting": "Hosting",
  "nav.group.operations": "Operations",
  "nav.group.settings": "Settings",

  "nav.item.overview": "Overview",
  "nav.item.nodes": "Nodes",
  "nav.item.linux": "Linux",
  "nav.item.windows": "Windows",
  "nav.item.docker": "Docker",
  "nav.item.vms": "Virtual Machines",
  "nav.item.sites": "Websites",
  "nav.item.dns": "Domains & DNS",
  "nav.item.ssl": "SSL Certificates",
  "nav.item.databases": "Databases",
  "nav.item.email": "Email",
  "nav.item.monitoring": "Monitoring",
  "nav.item.logs": "Logs",
  "nav.item.tasks": "Tasks & Activity",
  "nav.item.backups": "Backups",
  "nav.item.security": "Security",
  "nav.item.users": "Users & Roles",
  "nav.item.enrollment": "Agent Enrollment",
  "nav.item.integrations": "Integrations",
  "nav.item.modules": "Modules",
  "nav.item.updates": "Updates",
  "nav.item.system": "System Settings",

  "nav.phaseChip": "Phase {phase}",
  "topbar.scope": "Scope: All infrastructure",
  "topbar.user": "administrator",
  "topbar.locale.tr": "TR",
  "topbar.locale.en": "EN",

  "overview.title": "Overview",
  "overview.breadcrumb": "Overview",
  "overview.card.nodes": "Nodes",
  "overview.card.tasks": "Tasks",
  "overview.card.status": "System Status",
  "overview.nodes.empty.title": "No registered nodes yet",
  "overview.nodes.empty.description":
    "Node inventory activates in Phase 02 and Agent enrollment in Phase 03. Registered nodes will be listed here.",
  "overview.tasks.empty.title": "No task history yet",
  "overview.tasks.empty.description":
    "The durable task layer is built in Phase 02; task state and audit records will appear here.",
  "overview.status.badge": "Monitoring module not active",
  "overview.status.description": "Metric collection starts in Phase 13. This card shows no data yet.",

  "coming.title": "This module is not implemented yet",
  "coming.description":
    "The {module} module is planned for Phase {phase} of the roadmap. Early implementation is rejected as scope creep.",
};
