# Module Catalogue

Each module exposes a UI route, backend application service, permission namespace, capability requirement, API contract, Agent operation set, audit event types, health check and test suite.

| Module | Responsibilities | Key dependencies |
|---|---|---|
| Overview | Fleet status, health, alerts, quick links | Inventory, metrics |
| Nodes | Enroll, group, tag, inspect and revoke nodes | Identity, Agent protocol |
| System | OS facts, CPU/RAM/disk/network, uptime | System adapter |
| Services | Inspect/restart/reload allowed system services | Service manager adapter |
| Docker | Images, containers, networks, volumes, Compose | Docker Engine API |
| Hosting | Site lifecycle, web server, PHP-FPM pools | Nginx/Apache adapters |
| Domains & DNS | DNS zones/records and provider links | DNS providers |
| SSL | ACME challenge, issue, renew, revoke | ACME library / proxy adapter |
| Databases | DB instances, databases, users, grants, backups | Database drivers |
| Files | Scoped file browsing/upload/download/permissions | Filesystem broker |
| Email | Mail domains, mailboxes, aliases, status | Postfix/Dovecot/Rspamd adapters |
| Virtualization | VM/guest lifecycle, storage, virtual network | libvirt/KVM |
| Monitoring | Metrics, alerts, health history | Prometheus/exporters |
| Logs | Host/service/Agent logs with filters | Journal/Event Log adapters |
| Security | Firewall, updates, SSH posture, audit | OS security adapters |
| Backup | Plans, snapshots, verification and restore | Restic / storage adapter |
| Cloud | Provider resources and lifecycle | Provider-specific API clients |
| Settings | Users, roles, certificates, integrations | Core platform |

Modules must be independently enableable. A module can be installed but disabled; unavailable integrations must report a clear reason instead of failing silently.
