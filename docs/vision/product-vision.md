# Product Vision

## Product statement

Server Control is a self-hosted, extensible infrastructure control plane. It gives an administrator one coherent web interface to observe and operate supported servers, hosting services, container workloads, virtual machines and infrastructure integrations.

## Product principles

1. **One control plane, many nodes:** A central UI and API can manage the local node and remote nodes.
2. **Agent-first connectivity:** Nodes initiate outbound authenticated connections; no broad inbound remote-shell access is required.
3. **Capability-aware UI:** Show actions only when a node declares and passes capability/compatibility checks.
4. **Typed operations over arbitrary shell:** Common actions are explicit domain operations with schemas, validation, audit and bounded execution.
5. **Modular, not fragmented:** Begin with a modular monolith for the Control Plane and separate node agents. Split services only with evidence.
6. **Safe by default:** Least privilege, deny-by-default authorization, reversible operations where possible, explicit confirmation for destructive operations.
7. **Open standards:** REST/OpenAPI for browser-facing API; gRPC/Protobuf for Agent protocol; OCI and standard operating-system interfaces where practical.
8. **Operational transparency:** Every change has status, actor, target, result and correlated logs.

## Primary users

- Owner / Super Administrator: full infrastructure administration.
- Infrastructure Administrator: manage nodes and infrastructure modules within assigned scopes.
- Operator: restart approved services, inspect metrics and deploy approved workloads.
- Read-only Auditor: view configuration, status, logs and audit history.

## Product boundary

The initial product is for the owner's own infrastructure. It is not initially a hosting reseller billing system, domain registrar, public cloud provider, or commercial multi-tenant SaaS. The architecture should not block future extensions, but those features are not MVP acceptance requirements.

## Success criteria

- One administrator can enroll and manage more than one node.
- A lost/revoked Agent certificate cannot continue issuing privileged operations.
- The UI never offers unsupported node actions.
- A routine action has a traceable task lifecycle and audit record.
- Failed/partial actions are visible and do not silently claim success.
- A documented restore procedure is tested before production readiness.
