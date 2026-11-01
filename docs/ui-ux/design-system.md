# UI Design System

## Direction

Use cPanel's information architecture pattern—persistent left navigation, grouped modules, top bar, breadcrumbs, searchable actions and card-based overview—without copying cPanel branding, exact layout assets or trade dress. The visual direction is modern, restrained, professional and operationally clear.

## Layout

- Left sidebar: Overview; Infrastructure (Nodes, Linux, Windows, Docker, VMs); Hosting (Sites, Domains/DNS, SSL, Databases, Email); Operations (Monitoring, Logs, Tasks & Activity, Backups, Security); Settings.
- Top bar: node/global scope selector, global search, task notifications, help, user/profile.
- Main content: breadcrumb, page title, one primary action, filters, status summaries, data table/cards.
- Node detail uses tabs: Summary, Metrics, Services, Storage, Network, Logs, Capabilities, Tasks, Settings.

## Design rules

- Status color must be paired with text and icon; never communicate status by color only.
- Destructive action buttons are visually separated and require confirmation.
- Long-running operations show task ID, current stage, cancel availability and final state.
- Empty states explain prerequisite/next action; unsupported capabilities show a reason.
- Tables support search, sorting, pagination, filters and responsive fallback.
- Provide keyboard navigation, visible focus, semantic labels, contrast and reduced-motion support.
- Build reusable tokens for spacing, typography, surfaces, borders, shadows, status and density.
- Use Apache ECharts for charts; avoid decorative graphs that imply uncollected metrics.
- Turkish and English locales should be supported from the beginning; user-facing copy must be translation-key based.
