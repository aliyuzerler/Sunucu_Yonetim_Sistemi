# Official Sources Checked for Baseline

The following sources were consulted when drafting the technology and compatibility baseline (2026-11-01). Recheck before pinning versions or integrating a dependency.

## Language / frontend / runtime
- Go releases: https://go.dev/dl/ and https://go.dev/doc/devel/release (Go 1.27.3 shown as current stable on baseline date).
- React versions: https://react.dev/versions (React 19.3 shown as latest on baseline date).
- Node.js release schedule: https://github.com/nodejs/Release (Node 26 LTS beginning October 28, 2026; Node 24 is Maintenance LTS).
- Vite supported releases: https://vite.dev/releases (Vite 8.3 supported line on baseline date).

## Operating systems / databases / containers
- Debian releases: https://www.debian.org/releases/ (Debian 13 Trixie current stable; 13.7 update announced September 12, 2026).
- Ubuntu release cycle: https://ubuntu.com/about/release-cycle and https://wiki.ubuntu.com/Releases (24.04 LTS remains supported; 26.04 LTS is available in 2026).
- PostgreSQL versioning: https://www.postgresql.org/support/versioning/ (PostgreSQL 18.x supported on baseline date).
- Docker Engine API: https://docs.docker.com/reference/api/engine/ (API version negotiation documented).

## RPC / queue / storage / metrics
- gRPC-Go: https://github.com/grpc/grpc-go (Apache-2.0).
- Valkey: https://github.com/valkey-io/valkey (BSD-3-Clause; optional, not mandatory).
- NATS Server: https://github.com/nats-io/nats-server (Apache-2.0; optional).
- Prometheus licensing: https://github.com/prometheus/docs/blob/main/docs/introduction/faq.md (Apache-2.0).
- Grafana licensing: https://github.com/grafana/grafana/blob/main/LICENSING.md (AGPL-3.0-only default with directory-level exceptions).
- Apache ECharts: https://github.com/apache/echarts/blob/master/LICENSE (Apache-2.0).
- Restic: https://github.com/restic/restic (BSD-2-Clause).

## Architecture references
- 1Panel: https://github.com/1Panel-dev/1Panel (GPL-3.0; study reference only).
- Cockpit: https://github.com/cockpit-project/cockpit (mixed licenses; file-level review required).
- HestiaCP: https://github.com/hestiacp/hestiacp (GPL-3.0; name/logo restrictions also documented).
- libvirt: https://gitlab.com/libvirt/libvirt (API LGPL-2.1+; other parts may have GPL terms).
- QEMU: https://www.qemu.org/ (code license varies; GPL-2.0-or-later is common default).

## Notes

Search engine summaries are not a substitute for reviewing repository LICENSE/NOTICE files, official release notes, advisories and exact version sources. No external source code was downloaded or bundled in this ZIP.
