# Platform Feature and Support Matrix

Support labels: **Planned** = architecture/future phase; **Experimental** = implemented but not production-certified; **Supported** = automated compatibility and security tests pass for declared versions. Never expose Planned/Experimental as Supported.

| Target / capability | Initial target | Planned phase | Notes |
|---|---|---:|---|
| Ubuntu Server 24.04 LTS | Primary Linux test target | 03–04 | Exact package versions validated in CI |
| Debian 13 (Trixie) | Secondary Linux test target | 03–04 | Current stable release at baseline date |
| Ubuntu 26.04 LTS | Compatibility extension | 06 | Do not assume package parity before tests |
| Rocky Linux / AlmaLinux | Later Linux adapters | 06 | Version-specific systemd, package, firewall tests |
| Windows Server | Separate Agent | 05 | Windows-native adapter; control plane remains Linux-first |
| Docker Engine | Linux node module | 07 | API version negotiation and permission boundary |
| Docker Compose | Linux node module | 07 | Compose plugin compatibility matrix |
| Nginx + PHP-FPM | Hosting | 08 | Per-site isolation and safe configuration reload |
| Apache | Optional hosting driver | 08 | Avoid assuming Nginx and Apache can both bind same ports |
| Domain / DNS | Provider adapters | 09 | DNS provider support is per provider |
| ACME / Let's Encrypt | SSL automation | 09 | Rate limits, renewal, challenge and rollback tests |
| MariaDB / MySQL | DB management | 10 | Version-specific grants and backup/restore |
| PostgreSQL | DB management | 10 | Separate managed instance vs panel's own DB |
| Postfix / Dovecot / Rspamd | Optional mail stack | 11 | Deliverability and DNS are external dependencies |
| KVM / QEMU / libvirt | Virtualization host | 12 | Requires hardware virtualization and supported kernel |
| LXC | Optional containers | 12 | Distinct from Docker; separate security model |
| Cloud provider APIs | Provider connectors | 14 | Each provider separately tested |

A node may report many OS facts, but this does not automatically qualify it for management. Supported operations are an intersection of OS version, Agent version, adapter version, host permissions and test status.
