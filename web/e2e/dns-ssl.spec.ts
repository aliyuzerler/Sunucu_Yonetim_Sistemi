// Faz 09 E2E: Domain/DNS + SSL sertifika sayfaları. Mock modda sağlayıcı
// CRUD (kimlik bilgisi ASLA geri gösterilmez — kriter 5), bölge/kayıt
// listesi, diff önizleme + apply (kriter 2), sertifika üretim sihirbazının
// üç görev zinciri (kriter 3/4) ve sınıflandırılmış rate_limit hatası
// doğrulanır. IT-01 dersi: liste yerleşmeden satır sayımı YAPILMAZ.
// Ekran görüntüleri docs/reviews/screenshots/phase-09 altına yazılır.
import { expect, test } from "@playwright/test";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const shotDir = path.resolve(here, "../../docs/reviews/screenshots/phase-09");
const realBackend = process.env.E2E_REAL === "1";

const ADMIN = "admin";
const PASSWORD = "guclu-parola-12345";

test.describe.configure({ mode: "serial" });

async function login(page) {
  await page.goto("/");
  await expect(page.getByLabel("Kullanıcı adı")).toBeVisible();
  await page.getByLabel("Kullanıcı adı").fill(ADMIN);
  await page.getByLabel("Parola").fill(PASSWORD);
  await page.getByRole("button", { name: "Giriş yap" }).click();
  await expect(page.getByRole("navigation", { name: "Ana gezinme" })).toBeVisible();
}

test("DNS: sağlayıcı ekleme kimlik bilgisini geri göstermez, bölge/kayıt listesi ve diff→apply akışı", async ({ page }) => {
  test.skip(realBackend, "Gerçek modda mock sağlayıcı yok; akış mock modda doğrulanır.");
  await login(page);

  await page.getByRole("button", { name: /Alan Adları ve DNS/ }).click();
  await expect(page.getByText("Henüz sağlayıcı eklenmedi.")).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "01-dns-empty.png"), fullPage: true });

  // Sağlayıcı ekle (kimlik bilgisi yalnız yazılır).
  await page.getByLabel("Sağlayıcı adı").fill("mock-e2e");
  await page.getByLabel("API anahtarı (yalnız yazılır)").fill("gizli-e2e-anahtar-9876");
  await page.getByRole("button", { name: "Sağlayıcı Ekle" }).click();

  // Liste yerleşir; kimlik bilgisi DEĞERİ hiçbir yerde görünmez (kriter 5).
  await expect(page.getByText("mock-e2e")).toBeVisible();
  await expect(page.getByText(/şifreli saklandı/)).toBeVisible();
  await expect(page.getByText("gizli-e2e-anahtar-9876")).toHaveCount(0);
  // Kabiliyet bildirimi (kriter 1).
  await expect(page.getByText(/DNS-01 ✓/)).toBeVisible();

  // Bölge bölümünü aç + listele (görev poll edilir).
  await page.getByRole("button", { name: "Bölgeler", exact: true }).click();
  await page.getByRole("button", { name: "Bölgeleri listele" }).click();
  const zoneSelect = page.getByRole("combobox", { name: "Bölgeler" });
  await expect(zoneSelect).toBeVisible({ timeout: 15_000 });
  await zoneSelect.selectOption("example.com");
  await page.screenshot({ path: path.join(shotDir, "02-dns-provider-zones.png"), fullPage: true });

  // Kayıtlar.
  await page.getByRole("button", { name: "Kayıtları listele" }).click();
  await expect(page.getByRole("cell", { name: "_acme-challenge", exact: true })).toBeVisible({ timeout: 15_000 });

  // Kayıt formu: www A 203.0.113.99 → diff → apply (kriter 2).
  // "Tür" etiketi sağlayıcı formunda da olduğundan kayıt bölümüne daralt.
  const zoneSection = page.locator('section[aria-label="Bölgeler"]');
  await zoneSection.getByLabel("Kayıt adı").fill("www");
  await zoneSection.getByLabel("Tür").selectOption("A"); // exact YOK: label textContent option metnini içerir
  await zoneSection.getByLabel("Değer").fill("203.0.113.99");
  await zoneSection.getByLabel("TTL (60-86400)").fill("600");
  await zoneSection.getByRole("button", { name: "Diff önizle" }).click();
  await expect(page.getByTestId("dns-diff")).toBeVisible({ timeout: 15_000 });
  await expect(page.getByTestId("dns-diff")).toContainText("203.0.113.10"); // before
  await expect(page.getByTestId("dns-diff")).toContainText("203.0.113.99"); // after
  await page.screenshot({ path: path.join(shotDir, "03-dns-diff-preview.png"), fullPage: true });

  await zoneSection.getByRole("button", { name: "Değişikliği uygula" }).click();
  await expect(page.getByRole("status").filter({ hasText: /Güncelleme: www\/A/ })).toBeVisible({ timeout: 15_000 });
  await page.screenshot({ path: path.join(shotDir, "04-dns-applied.png"), fullPage: true });
});

test("SSL: sertifika listesi sınıflandırılmış rate_limit hatasını görünür kılar", async ({ page }) => {
  test.skip(realBackend, "Gerçek modda demo sertifika yok; akış mock modda doğrulanır.");
  await login(page);

  await page.getByRole("button", { name: /SSL Sertifikaları/ }).click();
  await expect(page.getByText("demo.example.internal")).toBeVisible();
  // Kriter 3: rate limit + Retry-After görünür.
  await expect(page.getByText("yenileme başarısız")).toBeVisible();
  await expect(page.getByText("rate_limit")).toBeVisible();
  await expect(page.getByText(/Retry-After: 3600s/)).toBeVisible();
  // Expiry state yönetimi: yakında bitiyor rozeti.
  await expect(page.getByText(/yakında bitiyor/)).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "05-ssl-cert-list.png"), fullPage: true });
});

test("SSL: üretim sihirbazı üç görevi zincirler ve sertifikayı kurar", async ({ page }) => {
  test.skip(realBackend, "Gerçek modda agent yok; sihirbaz mock modda doğrulanır.");
  await login(page);

  // Node: web-01 (mock web şablonu; web.cert.* kabiliyetli).
  await page.getByRole("button", { name: /Sunucular/ }).click();
  await expect(page.getByText("Yükleniyor…")).toBeHidden();
  const row = page.getByRole("row", { name: /web-01/ });
  if (!(await row.count())) {
    await page.getByRole("button", { name: "Sunucu Ekle" }).click();
    await page.getByLabel("Görünen ad").fill("web-01");
    await page.getByLabel("Hostname (isteğe bağlı)").fill("web01.example.internal");
    await page.getByRole("button", { name: "Kaydet" }).click();
    await expect(page.getByText("web-01")).toBeVisible();
  }

  await page.getByRole("button", { name: /SSL Sertifikaları/ }).click();
  // getByLabel("Sunucu") bölüm aria-label'iyle de eşleşebildiğinden
  // select öğesine doğrudan in (label textContent option metnini taşır).
  const wizard = page.locator('section[aria-label^="Sertifika üret"]');
  await wizard.locator("select").first().selectOption({ label: "web-01" });

  // Sağlayıcı: ilk testte eklenen mock-e2e yoksa oluştur.
  const providerSelect = wizard.locator("select").nth(1);
  const providerOptions = await providerSelect.locator("option").allTextContents();
  if (!providerOptions.some((o) => o.includes("mock-e2e"))) {
    await page.getByRole("button", { name: /Alan Adları ve DNS/ }).click();
    await page.getByLabel("Sağlayıcı adı").fill("mock-e2e");
    await page.getByLabel("API anahtarı (yalnız yazılır)").fill("gizli-e2e-anahtar-9876");
    await page.getByRole("button", { name: "Sağlayıcı Ekle" }).click();
    await expect(page.getByText("mock-e2e").first()).toBeVisible();
    await page.getByRole("button", { name: /SSL Sertifikaları/ }).click();
  }
  await providerSelect.selectOption({ index: 1 });

  await wizard.getByLabel("Birincil domain").fill("wizard.example.internal");
  await wizard.getByLabel("SAN'lar (virgülle)").fill("www.wizard.example.internal");
  await page.getByRole("button", { name: "CSR üret ve sertifikayı al" }).click();

  // Üç adım tamamlanır: request → issue → install (poll edilir).
  await expect(page.getByText("Sertifika kuruldu.")).toBeVisible({ timeout: 30_000 });
  // Liste yenilenir: yeni sertifika issued görünür.
  await expect(page.getByText("wizard.example.internal")).toBeVisible({ timeout: 15_000 });
  await page.screenshot({ path: path.join(shotDir, "06-ssl-wizard-done.png"), fullPage: true });
});
