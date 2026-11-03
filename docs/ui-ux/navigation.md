# Navigation and Information Architecture

```text
Overview
Infrastructure
  Nodes
  Linux
  Windows
  Docker
  Virtual Machines
Hosting
  Websites
  Domains & DNS
  SSL Certificates
  Databases
  Email
Operations
  Monitoring
  Logs
  Tasks & Activity
  Audit Log
  Backups
  Security
Settings
  Users & Roles
  Agent Enrollment
  Integrations
  Modules
  Updates
  System Settings
```

Navigation items are capability/module-aware. A user without permission must not see an action as available; API still enforces permission. Global scope and current node must always be visible. Do not use a destructive action as a normal one-click quick action.

## Hash Adres Şeması (UX denetimi m.4.1/m.4.2 — ADR-0022)

Gezinme URL hash'inden türetilir (`web/src/navigation/router.ts`); her görünüm
paylaşılabilir bir adrese sahiptir. Geri/ileri tuşları ve yenileme korunur.

```text
#/                    → Overview (boş/bilinmeyen hash'in varsayılanı)
#/nodes               → Sunucu listesi
#/nodes/<node-id>     → Sunucu detayı (NodeDetail — derin bağlantı; node ilk
                        sayfada yoksa GET /api/v1/nodes/{id} ile çekilir)
#/tasks  #/audit  #/users  #/enrollment
#/dns    #/ssl   #/monitoring  #/logs  #/security
#/backups  #/integrations  #/modules  #/updates
#/<bilinmeyen>        → ComingSoon (faz bilgisiyle dürüst açıklama)
```

Uygulama içi gezinme görünümü senkron günceller ve hash'i yazar; tarayıcı
geri/ileri ve elle hash düzenlemesi `hashchange` üzerinden akar.
