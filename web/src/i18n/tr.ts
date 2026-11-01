// Türkçe varsayılan sözlük. Anahtar kümesi kanoniktir: diğer diller bu
// Record tipini karşılamak zorundadır (eksik anahtar derleme hatası olur).
export const tr = {
  "app.title": "Server Control",
  "app.subtitle": "Altyapı kontrol düzlemi",

  "nav.group.overview": "Genel Bakış",
  "nav.group.infrastructure": "Altyapı",
  "nav.group.hosting": "Hosting",
  "nav.group.operations": "Operasyonlar",
  "nav.group.settings": "Ayarlar",

  "nav.item.overview": "Genel Bakış",
  "nav.item.nodes": "Sunucular",
  "nav.item.linux": "Linux",
  "nav.item.windows": "Windows",
  "nav.item.docker": "Docker",
  "nav.item.vms": "Sanal Makineler",
  "nav.item.sites": "Web Siteleri",
  "nav.item.dns": "Alan Adları ve DNS",
  "nav.item.ssl": "SSL Sertifikaları",
  "nav.item.databases": "Veritabanları",
  "nav.item.email": "E-posta",
  "nav.item.monitoring": "İzleme",
  "nav.item.logs": "Loglar",
  "nav.item.tasks": "Görevler ve Hareketler",
  "nav.item.backups": "Yedekler",
  "nav.item.security": "Güvenlik",
  "nav.item.users": "Kullanıcılar ve Roller",
  "nav.item.enrollment": "Agent Kaydı",
  "nav.item.integrations": "Entegrasyonlar",
  "nav.item.modules": "Modüller",
  "nav.item.updates": "Güncellemeler",
  "nav.item.system": "Sistem Ayarları",

  "nav.phaseChip": "Faz {phase}",
  "topbar.scope": "Kapsam: Tüm altyapı",
  "topbar.user": "yönetici",
  "topbar.locale.tr": "TR",
  "topbar.locale.en": "EN",

  "overview.title": "Genel Bakış",
  "overview.breadcrumb": "Genel Bakış",
  "overview.card.nodes": "Sunucular",
  "overview.card.tasks": "Görevler",
  "overview.card.status": "Sistem Durumu",
  "overview.nodes.empty.title": "Henüz kayıtlı sunucu yok",
  "overview.nodes.empty.description":
    "Node envanteri Faz 02'de, Agent kaydı ise Faz 03'te etkinleşir. Sunucular kaydedildiğinde burada listelenir.",
  "overview.tasks.empty.title": "Görev geçmişi henüz yok",
  "overview.tasks.empty.description":
    "Kalıcı görev altyapısı Faz 02'de kurulur; burada görev durumu ve denetim kaydı görülecek.",
  "overview.status.badge": "İzleme modülü etkin değil",
  "overview.status.description": "Metrik toplama Faz 13'te başlar. Bu kart henüz veri göstermez.",

  "coming.title": "Bu modül henüz uygulanmadı",
  "coming.description":
    "{module} modülü yol haritasında Faz {phase} planında. Erken uygulama, kapsam kayması olarak reddedilir.",
} as const;

export type TranslationKey = keyof typeof tr;
export type Locale = "tr" | "en";
