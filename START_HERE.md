# İlk oturumda ZCode'a verilecek talimat

Bu depoyu dikkatle oku. Bu proje, cPanel benzeri görünüme sahip ancak kendi kod tabanımızdan geliştirilen çoklu işletim sistemi ve çoklu sunucu yönetim platformudur.

## Güncel durum (önce bunu oku)

16 fazlık yol haritasının tamamı uygulandı ve bağımsız testlerle kabul edildi: **Faz 00–15 (16/16)**, güncel sürüm **v0.16.0-rc1**. Sürümlü deb/rpm/zip paketleme, imzalı güncelleme hattı (SCP-SIG-V1) ve üretim sertleştirmesi Faz 15 ile tamamlandı.

İlk oturumda sırayla:

1. `README.md` — proje özeti, depo yapısı, güncel durum.
2. `docs/project-status.md` — faz faz kanıtlar, bilinen riskler, sıradaki adımlar.
3. `CHANGELOG.md` — sürüm geçmişi.
4. İlgili iş varsa: `docs/release/RELEASE-v0.16.0-rc1.md` (GA kapatıcıları) ve `agents.md` §1'deki kaynak önceliği.

## Çalışma kuralları

- `prompts/phase-start/phase-NN.md` ve `prompts/phase-test/phase-NN-test.md` dosyaları **tamamlanmış fazların tarihsel kaydıdır**; bu fazları yeniden uygulamaya çalışma. Yeni bir faz/görev yalnızca kullanıcının açık talimatıyla başlar.
- Yeni değişikliklerde aynı disiplin geçerlidir: önce plan + etkilenecek dosyalar + riskler; sonra küçük, test edilebilir adımlar; `prompts/workflow/` altındaki akışlar (fix-failed-tests, integration-regression, phase-gate, final-release-audit) yol göstericidir.
- Güvenlik kuralları (`agents.md` §4) ve kural setleri (`rules/`) her değişiklikte bağlayıcıdır; serbest metin shell yok, fail-closed varsayılan, secret loglanmaz.
- Çelişki durumunda öncelik sırası `agents.md` §1'dedir; çelişki sessizce çözülmez, ADR önerisi yazılır (`docs/decisions/`).
- Her anlamlı değişiklik sonunda `docs/project-status.md` ve `CHANGELOG.md` güncellenir; testler geçmeden iş tamamlanmış sayılmaz.
