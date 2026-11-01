# Glossary

- **Control Plane:** Merkezi web API, UI, veri, kullanıcı, görev ve envanter katmanı.
- **Node:** Yönetilen fiziksel/virtual makine veya desteklenen altyapı hedefi.
- **Agent:** Node üzerinde çalışan; Control Plane'e outbound bağlanan servis.
- **Privileged Broker:** Agent adına yalnızca sınırlı ayrıcalıklı işlemleri yapan lokal IPC servisi.
- **Adapter:** Belirli OS, runtime veya sağlayıcı API'sini domain operasyonlarına bağlayan sürücü.
- **Capability:** Bir node/adapter kombinasyonunun gerçekten desteklediği işlem.
- **Task:** Bir node üzerinde çalıştırılacak typed, versioned ve auditli operasyon.
- **Outbox:** Veritabanı değişikliğiyle aynı transaction'da yazılan, güvenilir dağıtım bekleyen olay kaydı.
- **mTLS:** İstemci ve sunucunun karşılıklı sertifika doğrulaması.
- **ACME:** Sertifika otoritesi ile otomatik sertifika yaşam döngüsü protokolü.
- **Control Plane host vs managed node:** Panelin kurulduğu makine ile panelin yönettiği hedef makine aynı olmak zorunda değildir.
