# Technology Stack Baseline

Version status checked for the documentation baseline date **2026-11-01**. These are starting ranges, not a promise to use an unpatched version. Before implementation, ZCode must verify the latest supported patch release, runtime compatibility and official release notes, then pin exact versions in lockfiles and build manifests.

| Layer | Selected technology | Baseline / policy | License / notes |
|---|---|---|---|
| Backend | Go | Go 1.27.x stable line; use latest security patch | BSD-3-Clause |
| HTTP router | `net/http` + chi | Current supported chi v5 | MIT; standard library first |
| Browser UI | React + TypeScript | React 19.3 line; strict TypeScript | MIT / Apache-2.0 for TypeScript |
| Frontend build | Node.js + Vite | Node 26 LTS line; Vite 8.3 supported line | Node MIT; Vite MIT |
| Styling/components | Tailwind CSS + shadcn/ui | Current stable compatible versions | MIT; generated component code must retain upstream notices if applicable |
| Charts | Apache ECharts | Current stable | Apache-2.0 |
| API | REST + OpenAPI | `/api/v1` | Specification tooling license per tool |
| Agent RPC | gRPC + Protocol Buffers | Versioned proto contract | gRPC-Go Apache-2.0; protobuf BSD-3-Clause |
| Main database | PostgreSQL | 18.x; 17.x compatibility only if tested | PostgreSQL License |
| Durable job queue | PostgreSQL outbox + `SKIP LOCKED` | First release | No additional broker required |
| Optional messaging | NATS JetStream | Introduce only when measured scale requires it | Apache-2.0 |
| Optional cache | Valkey | Optional; avoid mandatory Redis dependency | BSD-3-Clause |
| Containers | Docker Engine API / Moby | Negotiate API version; test supported daemon matrix | Moby Apache-2.0; daemon/package terms must be checked |
| Virtualization | KVM/QEMU + libvirt | Host kernel and libvirt version-specific | libvirt LGPL-2.1+ API; QEMU GPL-2.0-or-later; review component boundaries |
| Web server | Nginx | Nginx stable/mainline choice by tested matrix | BSD-2-Clause |
| Container ingress | Traefik | Optional driver | MIT |
| Certificates | ACME via lego or Certbot adapter | Start with ACME abstraction; choose implementation after compatibility review | lego MIT; Certbot Apache-2.0 |
| Monitoring | Prometheus + node_exporter | Current supported versions | Apache-2.0 |
| Logs / telemetry | OpenTelemetry Collector (optional) | Add when required | Apache-2.0 |
| Backup | Restic | Current stable | BSD-2-Clause |
| Local development | Docker Compose | Pinned plugin version | Apache-2.0 for Compose; Engine terms separately |
| CI | GitHub Actions or compatible runner | Lint, test, SAST, dependency/license scan | Hosted runner terms separate |

## Platform baseline

- Initial managed Linux validation: Ubuntu Server 24.04 LTS and Debian 13 (Trixie).
- Ubuntu 26.04 LTS is a planned compatibility target; do not declare supported until end-to-end CI and package operations pass.
- Control Plane initially runs on supported Linux only.
- Windows Server is managed by a Windows Agent introduced in Phase 05; Windows Control Plane hosting is not promised.
- `amd64` first; `arm64` support requires CI artifacts and package tests.

## Implementation guidance

- Prefer Go standard library for process, HTTP, crypto, logging (`slog`) and concurrency unless a dependency clearly improves maintainability.
- Use `pgx` for PostgreSQL access; migrations are explicit and reversible when safe.
- Avoid ORM magic for privilege-sensitive control-plane state. SQL migrations and reviewed queries are preferred.
- Use generated gRPC/Proto code from checked-in `.proto` files.
- Use OpenAPI generation/validation only after schema ownership and generated-code workflow are documented.
- Do not introduce Redis by habit. If cache is needed, Valkey is an option; if event streaming is needed, NATS is an option. Both are optional and must be justified.
