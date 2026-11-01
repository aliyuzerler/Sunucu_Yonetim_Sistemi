# ZCode Agent Instructions — Server Control

## 1. Kaynak önceliği

Çalışmaya başlamadan önce aşağıdaki dosyaları oku:
1. `README.md` ve `START_HERE.md`
2. `docs/vision/product-vision.md`, `docs/vision/scope.md`, `docs/vision/feature-matrix.md`
3. `docs/architecture/system-architecture.md`, `agent-architecture.md`, `security-boundaries.md`, `capability-system.md`
4. `docs/technology/technology-stack.md`, `dependency-policy.md`, `open-source-references.md`
5. `docs/decisions/` altındaki ADR dosyaları
6. İlgili fazın dokümanı, uygulama promptu, test promptu ve ilgili skill/rule dosyaları

Çelişki varsa sırasıyla: güvenlik kuralları → ADR → mimari doküman → teknoloji standardı → faz promptu → geçici kod varsayımı. Çelişkiyi sessizce çözme; `docs/decisions/` içine ADR önerisi yaz ve kullanıcıya raporla.

## 2. Ürün sınırları

- Ürün, kullanıcının kendi altyapısını yönetmesi için self-hosted kontrol panelidir. İlk sürümde SaaS faturalandırma, hosting satışı, müşteri komisyonu veya herkese açık multi-tenant ticari hesap sistemi geliştirme.
- Nihai hedef; Linux dağıtımları, Windows Server, Docker, klasik web hosting, DNS, SSL, veritabanları, e-posta, KVM sanallaştırma, yedekleme, izleme ve bulut sağlayıcılarını tek arayüzden yönetmektir.
- İlk doğrulanan sistemler Ubuntu Server 24.04 LTS ve Debian 13'tür. Windows ve diğer dağıtımlar için kod mimarisi baştan ayrılır; ilgili faz ve testler geçmeden destek beyan edilmez.
- İlk kurulum tek Control Plane örneği olabilir; merkezi veri modeli ve Agent protokolü çoklu sunucuya hazır olmalıdır.
- cPanel bilgi mimarisinden ilham al; cPanel markasını, logosunu, ikonlarını, metinlerini veya ekran tasarımını kopyalama.

## 3. Mimari kurallar

- İlk aşamada dağıtık mikroservis yığını kurma. Modüler monolit Control Plane + ayrı Agent ikilileri kullan.
- Backend ve Agent Go; UI React + TypeScript.
- API sözleşmesi OpenAPI; Agent protokolü Protocol Buffers/gRPC.
- Agent bağlantısı dışarı doğru, mTLS ile kurulmalı. Yönetilen sunucuya inbound yönetim portu açmak varsayılan olmamalı.
- Linux/Windows özel işlemleri adapter arayüzlerine konmalı. Domain/business katmanı işletim sistemi komutlarını bilmemeli.
- Capability registry, her düğümün gerçekten desteklediği işlemleri ilan etmesini sağlamalı.
- PostgreSQL kalıcı kaynak doğrusu (source of truth). İlk sürümde kalıcı job/outbox yapısı PostgreSQL üzerinde tasarlanır. Harici broker zorunlu değildir.
- Her API ve protokol değişikliği versioned olmalı; geriye uyumluluk düşünülmeli.
- İşlemler idempotency key, timeout, cancellation, retry sınırı ve audit event kullanmalı.
- Bir modül başka modülün private iç koduna bağlanmamalı; yalnızca tanımlı port/interface ve event sözleşmelerini kullanmalı.

## 4. Güvenlikte taviz yok

- Serbest metin shell komutunu uzaktan çalıştıran API oluşturma.
- Kullanıcı girdisini shell string interpolation ile birleştirme. Mümkünse işletim sistemi API'si; zorunluysa sabit binary + argüman dizisi + allowlist kullan.
- Docker socket'i web tarayıcısına veya genel ağa açma. Docker daemon erişimini ayrıcalıklı ve yüksek riskli kabul et.
- Agent kimliği sertifika tabanlı olmalı; bootstrap token tek kullanımlık, kısa ömürlü ve node'a bağlı olmalı.
- mTLS, sertifika rotasyonu/iptali, replay koruması ve protokol sürüm kontrolü tasarla.
- Secret değerlerini loglama, UI'ya tekrar düz metin döndürme veya Git'e yazma.
- Varsayılan deny; dar RBAC izinleri; kritik işlemlerde yeniden doğrulama ve açık kullanıcı onayı.
- File manager path traversal, symlink escape, race condition ve tenant/node izolasyonuna karşı test edilmeli.
- Terminal, SSH key veya root erişimini genel özellik gibi açma. İleride ayrı izin, MFA, kısa ömürlü oturum, audit ve session recording gerektirir.
- Gerçek sistemde destructive işlemi varsayılan olarak çalıştırma. Geliştirmede mock/sandbox kullan.
- Üretim sunucusunda test çalıştırmadan önce hedef host, etki, rollback ve onay gereksinimini yazılı olarak belirt.

## 5. Kod kalitesi

- Tipli modeller, açık hata türleri ve bağlamlı loglar kullan.
- Go'da context propagation, graceful shutdown, race testleri, `go test ./...`, `go vet ./...` ve mümkün olduğunda static analysis zorunlu.
- TypeScript strict mode açık olmalı; `any` yalnızca gerekçeli ve sınırlı kullanılmalı.
- UI erişilebilir, responsive ve Türkçe/İngilizce yerelleştirmeye hazır olmalı.
- Secret, token, private key ve gerçek sunucu verisi test fixture'larına konmamalı.
- Mock/unit test, integration test, contract test ve en az bir E2E smoke test katmanı kullan.
- Her faz sonunda `CHANGELOG.md`, `docs/project-status.md`, test raporu ve ilgili ADR'leri güncelle.
- Testleri geçmeyen veya kritik güvenlik bulgusu olan fazı tamamlandı olarak işaretleme.

## 6. Çalışma biçimi

- Sadece kullanıcının belirttiği fazı uygula. Sonraki faza kendiliğinden geçme.
- Başlamadan önce mevcut repo durumunu ve son faz raporunu incele.
- Önce plan + etkilenecek dosyalar + riskler; sonra küçük ve test edilebilir değişiklikler.
- Var olan çalışan işlevi gerekçesiz silme veya yeniden yazma.
- Belirsiz bağımlılık/API/lisans varsa doğrula; varsayım olarak gizleme.
- Gerçek indirme/kurulum gerekiyorsa resmi kaynaktan, sabitlenmiş sürüm ve checksum/signature doğrulamasıyla yap. İndirme yapılmadıysa yapılmış gibi raporlama.
- Faz sonunda değişen dosyaları, uygulanan işlevleri, komutları, test sonuçlarını, açık riskleri ve kalan işleri raporla.
