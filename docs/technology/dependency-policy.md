# Dependency and License Policy

1. Every direct and transitive dependency must have a recorded name, exact version/commit, source URL, SPDX license identifier, purpose and maintainer status.
2. Verify license from the actual source distribution, not only a package website label.
3. Generate an SBOM for backend, frontend, Agent and release artifacts.
4. Block new dependencies with unknown, custom, SSPL, Commons Clause or incompatible copyleft terms until reviewed.
5. AGPL/GPL dependencies must be explicitly approved and reviewed for distribution/network-use implications. Never copy GPL/AGPL code into our proprietary product by accident.
6. Preserve LICENSE, NOTICE and attribution files as required.
7. Review security advisories and update policy at each phase and release.
8. Pin exact dependency versions in lockfiles and Go module files; use reproducible CI builds where practical.
9. No unmaintained dependency may be introduced without ADR and mitigation.
10. Re-run license scan when dependency tree or build artifact changes.

Suggested tooling: `govulncheck`, `osv-scanner`, `trivy`, `syft` (SBOM), package manager audit and license scanner. Verify the current license of the scanner tool itself before bundling it.
