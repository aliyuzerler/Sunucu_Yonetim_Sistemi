# Version and Update Policy

- Treat versions in `technology-stack.md` as baseline families. Before a new implementation or upgrade, check official release/support pages.
- Pin exact patch versions in `go.mod`, `package-lock.json`/`pnpm-lock.yaml`, container image digests and deployment manifests.
- Use supported LTS/stable release lines for production; do not use alpha, beta or nightly in production.
- Update security patches promptly through a tested patch release process.
- Maintain a compatibility matrix: Control Plane version × Agent version × OS release × Docker API × libvirt API × module adapter version.
- Agent auto-update must be staged, signature-verified, health-checked and rollback-capable. Never self-update a privileged component from an unauthenticated URL.
- Docker Engine API clients must negotiate supported API version and gracefully report unsupported operations.
- Database migrations must have backup/restore instructions and explicit downgrade limitations.
- Record the date and source used when a version is selected.
