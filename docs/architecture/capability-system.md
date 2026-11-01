# Capability and Adapter System

## Purpose

The platform must not assume every node can do every action. Capability is a runtime contract, not a hard-coded OS label.

## Capability record

Suggested fields:
- `capability_id` (stable namespaced key, e.g. `linux.systemd.service.restart`)
- `version` (semantic contract version)
- `available` and `reason_unavailable`
- `adapter_id` and adapter version
- supported OS family/version range
- required local privilege profile
- supported operation schema versions
- `observed_at` and expiration/freshness

## Adapter interfaces

Define Go interfaces by domain, not a single giant interface:
- `SystemFactsProvider`
- `MetricsProvider`
- `ServiceManager`
- `PackageManager`
- `FirewallManager`
- `FilesystemManager`
- `ContainerRuntime`
- `VirtualizationProvider`
- `WebServerProvider`
- `DNSProvider`
- `CertificateProvider`
- `DatabaseProvider`
- `BackupProvider`
- `CloudProvider`

Each adapter declares capabilities and performs preflight/validation. Adapters return typed domain errors (`Unsupported`, `PermissionDenied`, `InvalidInput`, `Conflict`, `Unavailable`, `Timeout`, `PartialFailure`).

## OS implementations

- Linux systemd adapter, with distro-specific package/firewall/filesystem adapters.
- Windows SCM, Event Log, firewall and storage adapters.
- Docker Engine adapter; do not confuse container capability with VM capability.
- libvirt adapter for KVM/QEMU/LXC providers.
- Cloud providers are remote API adapters and do not require node Agent unless provider design does.

## UI behavior

The UI queries capability snapshots and shows a clear unavailable explanation. Capability state must be refreshed after OS/Agent updates. The server remains authoritative: hiding a button is not authorization. Every operation repeats capability and permission checks on the backend and Agent.
