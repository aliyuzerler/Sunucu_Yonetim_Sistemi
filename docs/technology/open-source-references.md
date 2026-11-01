# Open-source Projects to Study / Integrate

This is a reference list, not a direction to copy their source code. Prefer their documented APIs, standard protocols, or independent architectural lessons. Re-check each repository's current LICENSE file, submodules, bundled assets and dependencies before importing code.

| Project | Repository / docs | License snapshot | Intended use |
|---|---|---|---|
| 1Panel | https://github.com/1Panel-dev/1Panel | GPL-3.0 | UI/feature and module architecture study only; do not copy code into a permissively licensed product without a deliberate GPL decision |
| Cockpit | https://github.com/cockpit-project/cockpit | Mixed: LGPL-2.1+, GPL-3.0+, BSD-3, MIT, CC-BY-SA | Linux admin workflow reference; inspect file-level license before reuse |
| Coolify | https://github.com/coollabsio/coolify | Verify current repository LICENSE before reuse | Docker deployment and app platform workflow reference |
| HestiaCP | https://github.com/hestiacp/hestiacp | GPL-3.0; name/logo have separate trademark restrictions in repo docs | Hosting feature/API reference; avoid using its brand |
| Proxmox VE | https://www.proxmox.com/en/proxmox-virtual-environment/overview and https://git.proxmox.com/ | Component/repository-specific; verify | VM lifecycle and operator UX reference; integrate through libvirt/KVM or supported APIs, do not assume a single license |
| Docker / Moby | https://github.com/moby/moby and https://docs.docker.com/reference/api/engine/ | Apache-2.0 for Moby source; daemon packaging and Docker product terms may differ | Docker Engine API integration |
| Prometheus | https://github.com/prometheus/prometheus | Apache-2.0 | Metrics collection and query |
| Grafana | https://github.com/grafana/grafana | Default AGPL-3.0-only; some directories have exceptions | Prefer external optional installation, not embedding/redistributing without license review |
| Traefik | https://github.com/traefik/traefik | MIT | Docker ingress / reverse proxy adapter |
| Restic | https://github.com/restic/restic | BSD-2-Clause | Backup engine integration |
| libvirt | https://gitlab.com/libvirt/libvirt | LGPL-2.1+ for C API; some components GPL-2.0+ | Virtualization API |
| QEMU | https://www.qemu.org/ | GPL-2.0-or-later except components with different notices | VM execution layer; use system package rather than copying code |
| Valkey | https://github.com/valkey-io/valkey | BSD-3-Clause | Optional cache / in-memory data store |
| NATS Server | https://github.com/nats-io/nats-server | Apache-2.0 | Optional messaging / event stream |
| Apache ECharts | https://github.com/apache/echarts | Apache-2.0 | Dashboard visualization |
| gRPC-Go | https://github.com/grpc/grpc-go | Apache-2.0 | Agent transport |
| chi | https://github.com/go-chi/chi | MIT | Go HTTP router |
| PostgreSQL | https://www.postgresql.org/ | PostgreSQL License | Platform database |

## Important licensing caveats

- License metadata on GitHub is a starting point, not legal clearance. Verify exact commit/tag and all distributed files.
- GPL/AGPL obligations depend on how code is modified, combined, conveyed and offered over a network. Obtain legal review before shipping a product with copyleft code.
- A permissive license does not waive trademark rules, third-party asset terms, patent clauses or dependency notices.
- Do not embed the full source of 1Panel, HestiaCP or Cockpit in this codebase. Treat them as study references unless a separately approved license strategy says otherwise.
- Create an SBOM (SPDX or CycloneDX) and retain third-party notices for every release.
