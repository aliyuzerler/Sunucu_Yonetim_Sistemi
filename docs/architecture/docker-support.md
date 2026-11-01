# Docker Desteği ve Sınır Matrisi (Faz 07)

Bu belge Faz 07 Docker modülünün destek matrisini, güvenlik sınırlarını ve
dürüst boşluklarını tanımlar. Windows/Linux dağıtım matrisleri için
`windows-support.md` ve `linux-support.md` geçerlidir.

## 1. Destek matrisi

| Bileşen | Destek | Kanıt |
| --- | --- | --- |
| Docker Engine (Linux, unix socket) | **API 1.24–1.47** (pazarlıklı) | CI `agent-docker`: runner daemon'ı + disposable busybox lifecycle |
| Engine TCP socket / TLS-uzak daemon | **Desteklenmez** — tasarım reddi (ADR-0011) | kod: yalnız unix dial (`dockerx.Client.socketPath`) |
| container list/inspect/logs/start/stop/restart | Desteklenir (docker:read / docker:control) | birim + gerçek lifecycle testi |
| container/image/volume/network **remove** | **PREVIEW-ONLY** — Engine `DELETE` çağrısı metin olarak döner, yürütülmez | `dockerx.RenderRemovePreview`; `*Client`'ın remove metodu yok (derleyici invariant) |
| image/network/volume listeleri | Desteklenir (docker:read; istemci-side clamp) | birim testler |
| compose **validate** | Desteklenir — şema + 14+ policy kuralı (agent-local saf) | `composeops` test seti |
| compose **deploy** | **PLAN-ONLY** — aksiyon listesi metin, yürütülmez | `composeops.BuildDeployPlan` (`Executed=false`) |
| Windows node'larda docker ailesi | Yok (windows pipe broker'ında üye değil; dispatch default-deny) | `auth_windows.go` |

- Engine API sürümü `/_ping` üzerinden pazarlanır; daemon <1.24 ise tipli
  `ErrUnsupportedAPI` döner (tahmin yok). >1.47 daemon'lar 1.47'ye clamp'lenir.
- compose ayrıştırması `gopkg.in/yaml.v3 v3.0.1` (MIT) — girdi ≤64 KiB,
  services ≤64, findings ≤100; alias/genişleme koruması yaml.v3 içindir.

## 2. Compose policy kuralları (docker.compose.validate)

Önizleme raporunun bulgu kuralları (kapalı küme; severity high/medium/low):

- **high:** `privileged`, tehlikeli `cap_add` (ALL/SYS_ADMIN/NET_ADMIN/
  SYS_PTRACE), `network_mode: host`, `pid: host`, `docker.sock` bağlama,
  hassas host dizini bağlama (`/`, `/etc`, `/proc`, `/sys`, `/boot`, `/dev`)
- **medium:** diğer `cap_add`, `network_mode: container:*`, `ipc: host`,
  root kullanıcı (user yok/0), public port (host IP yok veya 0.0.0.0),
  gizli-anahtar görünümlü ortam ANAHTARI (değer asla kopyalanmaz), `devices`
- **low:** ayrıcalıklı host portu (<1024), bind-mount bilgi, `env_file`,
  restart politikası yok, yazılabilir kök dosya sistemi

Şema hataları: yaml ayrıştırma, `services` yok/boş, image+build ikisi de yok,
geçersiz servis adı, services >64, `depends_on` bilinmeyen servis veya döngü.

## 3. Güvenlik sınırları

1. Daemon soketine erişim YALNIZ `server-privd`'de (root; unix socket; TCP
   yok). Broker JSONL audit'i her istek için (ret dahil) yazılır.
2. Üç katmanlı doğrulama: control-plane validator → agent executor → broker
   `validateRequest` (strict JSON, kapalı alan kümesi, ref allowlist
   `^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`, image ref `+:@/` , `..` reddi).
3. Silme/deploy yürütme yüzeyi kodda YOKTUR (ADR-0011) — yalnız metin
   önizleme döner; onay akışlı yürütme ayrı faz + yeni ADR.
4. Secret redaksiyonu: inspect'te `Config.Env` dışlanır; compose bulguları
   yalnız anahtar adları; log çıktısı 64 KiB sınırı + truncation dürüst
   raporlanır.

## 4. Bilinen sınırlar / dürüst boşluklar

1. compose `build:` bölümleri analizde yalnız varlık olarak bilinir; build
   bağlamı derin analizi kapsam dışıdır.
2. compose `configs`/`secrets` bölümleri Faz 07'de ayrıştırılmaz (genişletme
   sonraki fazlara).
3. Engine olay akışı (event stream) ve istatistik API'si kapsam dışıdır.
4. CI kanıtı runner'ın kendi daemon'ı ile üretilir (disposable); sahibin
   kalıcı VM doğrulaması ek değerdir.
