# agent/

Node Agent (ayrı Go ikilisi) — **Faz 03'te** geliştirilmeye başlanır.

Bu klasör şu an yalnızca monorepo yerleşimini rezerve eder (Faz 01 kabul
kriteri: monorepo klasörleri `backend/control-plane, agent, proto, web, deploy,
docs` olarak ayrılmış olmalı). Erken uygulama kapsam kaymasıdır.

Planlanan içerik (Faz 03/04):

- `server-agent`: outbound mTLS gRPC istemcisi, heartbeat, capability raporu
- `server-privd`: dar kapsamlı lokal privileged broker (Unix domain socket)
- Kayıt: tek kullanımlık token → istemci sertifikası; özel anahtar node'dan çıkmaz

Kurallar: `rules/agent-rules.md`, `skills/linux-agent/SKILL.md`,
`docs/architecture/agent-architecture.md`.
