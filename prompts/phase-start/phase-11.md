# ZCode Uygulama Promptu — Faz 11: E-posta servisi yönetimi

Bu depodaki `agents.md`, `rules/`, `skills/`, `docs/architecture/`, `docs/technology/`, `docs/security/` ve `docs/phases/phase-11.md` dosyalarını oku. Sonra yalnızca bu fazı uygula.

## Görev

Postfix, Dovecot ve Rspamd tabanlı isteğe bağlı mail modülünü tasarla ve test ortamında uygula; mail deliverability dış DNS bağımlılıklarını açıkla.

## Uygulama talimatları

1. Başlamadan önce repo durumunu, önceki faz raporunu ve ilgili ADR'leri incele. Önce kısa uygulama planı, dosya listesi, bağımlılıklar ve riskleri çıkar.
2. İlgili fazın kabul kriterlerini tek tek izlenebilir görevler haline getir. Başka fazlara ait özellikleri kapsam kayması olarak ertele.
3. Mimari sınırları koru: Control Plane modüler monolith; Agent ayrı ikili; ayrı privileged broker; OS işlemleri adapter arayüzleri; capability ile feature gating; typed operation; PostgreSQL kalıcı kayıt.
4. Gerçek sunucuya bağlanma veya host konfigürasyonu değiştirme. Önce unit/mock/integration testleri ve disposable ortam kullan. Gerçek host testi gerekiyorsa açıkça ayrı test planı ve risk listesi üret; kullanıcı onayı olmadan destructive işlem çalıştırma.
5. Her işlem için RBAC, node scope, validation, timeout, idempotency, audit ve secret redaction gereksinimini kontrol et.
6. Her dosyayı küçük, anlaşılır ve test edilebilir tut. Kod üretirken mevcut dosyaları sebepsiz silme veya büyük ölçekte yeniden yazma.
7. İhtiyaç duyulan dış bağımlılığın güncel sürümünü, resmi kaynağını ve lisansını doğrula; pinle; `docs/technology/` ve SBOM kayıtlarını güncelle. Lisans belirsizse bağımlılığı ekleme.
8. Kod, test, dokümantasyon ve UI durumlarını birlikte tamamla. Sadece backend endpointi yazıp UI'yı veya testini eksik bırakma.
9. Testleri çalıştır; geçmeyen testleri saklama veya silme. Hata varsa düzelt ve yeniden çalıştır.
10. `docs/project-status.md`, `CHANGELOG.md` ve varsa ADR'leri güncelle.

## Kabul kriterleri

- [ ] Mail modülü varsayılan kurulumda zorunlu olmamalı; preflight ve port kontrolü yapmalı.
- [ ] Domain/mailbox/alias/quota işlemleri typed ve auditli olmalı.
- [ ] SPF, DKIM, DMARC, PTR/reverse DNS gereksinimleri UI'da açıklanmalı.
- [ ] Mail servis config değişiklikleri syntax check ve rollback olmadan uygulanmamalı.
- [ ] Spam/abuse limitleri ve mail queue görüntüleme bulunmalı; hassas içerik loglanmamalı.

## Son rapor formatı

- Yapılanlar
- Değişen/eklenen dosyalar
- Kullanılan yeni bağımlılıklar ve lisansları
- Çalıştırılan komutlar
- Test sonuçları (PASS / FAIL / NOT RUN / BLOCKED)
- Güvenlik ve operasyon riskleri
- Açık kalan işler
- Faz tamamlandı mı? (Evet/Hayır; gerekçesi)

**Sonraki faza geçme.** Bu fazın test promptu ayrıca çalıştırılacaktır.
