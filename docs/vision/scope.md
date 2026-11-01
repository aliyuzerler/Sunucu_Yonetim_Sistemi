# Scope and Non-Goals

## In scope — eventual platform

- Linux node management: supported distro detection, system facts, service status/control, users/groups (carefully scoped), storage, network, firewall integration, package updates and logs.
- Windows Server node management through a dedicated Windows Agent: services, event logs, host facts, firewall and update status using Windows-native APIs.
- Docker Engine and Compose management, images, containers, networks, volumes, resource limits and logs.
- Web hosting: sites, virtual hosts, Nginx, PHP-FPM, app runtimes and deployment directories.
- Domain/DNS/SSL: DNS provider adapters, reverse proxy routes, ACME issuance/renewal.
- Database instances: MariaDB/MySQL and PostgreSQL management; credentials and grants.
- Email stack management as an optional advanced module.
- KVM/QEMU/libvirt virtual machine lifecycle and virtual networks.
- Monitoring, logs, notifications, backup and restore.
- Cloud provider integrations through provider APIs.
- Multi-node inventory and capability-aware operations.

## MVP scope

MVP is intentionally smaller: Control Plane, account/RBAC, node registry, Linux Agent, heartbeat/capability, system facts and metrics, service management, task/audit history, core UI, Docker read-only/limited lifecycle after security review, and development/test deployment. Hosting is added in a later phase, not presumed available from the first install.

Alignment note (Phase 00 review): in the 16-phase roadmap this MVP corresponds to Phases 00–04. Docker read-only/limited lifecycle is delivered in Phase 07 after the dedicated security review; it is listed here as an MVP target, not as part of the initial skeleton.

## Explicit non-goals for initial releases

- Public SaaS tenant billing, subscription plans or payment gateway.
- Domain registration / registrar reseller service.
- Arbitrary root shell execution through REST API.
- Automatic installation of every optional service on a production host without a preview and confirmation.
- Managing unsupported operating systems based only on a successful SSH connection.
- Treating a VM as a full managed OS node unless a guest Agent is enrolled.
- Claiming a cloud provider resource can be fully managed without checking the provider API's permissions and feature coverage.
