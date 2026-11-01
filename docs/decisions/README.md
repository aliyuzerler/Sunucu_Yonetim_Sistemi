# Architecture Decision Records (ADR)

ADR'ler, mimari olarak önemli kararları, bağlamlarını ve sonuçlarını kaydeder. Faz 00 denetiminde zorunlu kılınan ADR sürecinin dizinidir.

## Süreç

1. Karar, mimari/teknoloji/güvenlik üzerinde kalıcı etkisi varsa ADR olarak yazılır (yeni dosya: `ADR-NNNN-kisa-ad.md`).
2. Numaralandırma sıralıdır; hiçbir ADR numarası yeniden kullanılmaz veya silinmez.
3. Durum değerleri: `Proposed` → `Accepted` | `Rejected` | `Superseded by ADR-XXXX`.
4. Çelişki çözüm hiyerarşisi `agents.md` bölüm 1'dedir: güvenlik kuralları → ADR → mimari doküman → teknoloji standardı → faz promptu → geçici kod varsayımı.
5. Yeni bir ADR eklendiğinde bu dizin ve `CHANGELOG.md` güncellenir.

## Şablon

```markdown
# ADR-NNNN: <başlık>
- Status: Proposed | Accepted | Rejected | Superseded by ADR-XXXX
- Context: <kararı zorlayan durum, kısıtlar, alternatifler>
- Decision: <alınan karar ve gerekçesi>
- Consequences: <olumlu/olumsuz sonuçlar, takip işleri>
```

## Dizin

| ADR | Başlık | Durum |
|---|---|---|
| [ADR-0001](ADR-0001-modular-monolith.md) | Modular Monolith Control Plane | Accepted (baseline) |
| [ADR-0002](ADR-0002-agent-first-mtls.md) | Outbound Agent Connections with mTLS | Accepted (baseline) |
| [ADR-0003](ADR-0003-typed-operations.md) | Typed Allowlisted Operations, No Arbitrary Remote Shell | Accepted (security invariant) |
| [ADR-0004](ADR-0004-postgres-source-of-truth.md) | PostgreSQL Source of Truth and Durable Job Queue | Accepted (baseline) |
| [ADR-0005](ADR-0005-linux-first-cross-platform-agent.md) | Linux-first Control Plane, Cross-platform Agent Contracts | Accepted (baseline) |
| [ADR-0006](ADR-0006-multi-node-strategy.md) | Multi-node Strategy — Single Control Plane First, Multi-node-ready Contracts | Accepted (baseline, Phase 00) |
| [ADR-0007](ADR-0007-identity-sessions-bootstrap.md) | Identity, Sessions, First-Admin Bootstrap and CSRF Model | Accepted (Phase 02) |
| [ADR-0008](ADR-0008-agent-protocol-mtls.md) | Agent Protocol, mTLS Identity and Enrollment | Accepted (Phase 03) |
| [ADR-0009](ADR-0009-windows-agent.md) | Windows Server Agent and Native Adapters | Accepted (Phase 05) |
| [ADR-0010](ADR-0010-firewall-preview-only.md) | Firewall Rule Changes — Preview-Only (No Mutation Surface) | Accepted (Phase 06) |
| [ADR-0011](ADR-0011-docker-broker-only-preview-remove.md) | Docker Engine Access — Broker-Only + Preview-Only Remove/Deploy | Accepted (Phase 07) |
| [ADR-0012](ADR-0012-web-hosting.md) | Web Hosting — Broker-Only Execution, File-Preserving Delete, Marker Config Discipline | Accepted (Phase 08) |
| [ADR-0013](ADR-0013-dns-acme-tls-keys.md) | DNS Provider Adapter on Control Plane, ACME DNS-01 Orchestration, TLS Keys Never Leave the Node | Accepted (Phase 09) |
