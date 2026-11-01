# Deployment Model

## First supported topology

- One Linux Control Plane host.
- PostgreSQL on the same host for initial install or a separate private DB host.
- One local Agent and zero or more remote Agents.
- Browser access through HTTPS reverse proxy.
- Agent connections outbound to Control Plane over an authenticated TLS endpoint.

## Installation principles

- Installer must be idempotent and preflight OS, architecture, ports, storage, existing services and package conflicts.
- Never overwrite existing Nginx/Apache/Docker/PostgreSQL configuration without backup and diff preview.
- Prefer package manager + signed repository or signed release artifacts over piping an unverified remote script to shell.
- Installation has `check`, `plan`, `apply`, `verify` stages.
- Support uninstall/rollback instructions and preserve user data by default.
- Keep management listener separate from public hosting listener.
- Firewall default is deny; explicitly list required ports in deployment docs.

## Port policy

Do not hardcode a public exposure requirement into product behavior. Choose ports through config; document purpose, bind address and firewall rule. Management UI and gRPC endpoint should bind to private interfaces or be protected by a reverse proxy/VPN wherever possible. Never expose Docker Engine TCP without strong authenticated transport and network restrictions.
