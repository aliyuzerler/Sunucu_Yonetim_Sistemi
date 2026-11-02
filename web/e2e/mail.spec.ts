// Faz 11 E2E: E-posta servisi node bölümü.
// Mock modda zengin içerik doğrulanır: preflight raporu, deliverability
// açıklaması (kriter 3), domain/mailbox/alias yaşam döngüsü, tek-seferlik
// şifre reveal'ı, içeriksiz kuyruk (kriter 5) ve limitler.
// Gerçek backend modunda agent olmadığından dürüst boş durumlar doğrulanır.
// Ekran görüntüleri docs/reviews/screenshots/phase-11 altına yazılır ve
// repoya commit edilir (sahip talimatı). IT-01 dersi: liste yerleşmeden
// (yükleniyor göstergesi kaybolmadan) satır sayımı YAPILMAZ.
import { expect, test } from "@playwright/test";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const shotDir = path.resolve(here, "../../docs/reviews/screenshots/phase-11");
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

// mail-01 UI'dan oluşturulur; MOCK modda "mail-" öneki e-posta şablonunu
// tetikler (mail.* ailesi), gerçek modda agent olmadığından dürüst boş
// durumlar doğrulanır.
async function openMail01(page) {
  await page.getByRole("button", { name: /Sunucular/ }).click();
  await expect(page.getByText("Yükleniyor…")).toBeHidden();
  const row = page.getByRole("row", { name: /mail-01/ });
  if (!(await row.count())) {
    await page.getByRole("button", { name: "Sunucu Ekle" }).click();
    await page.getByLabel("Görünen ad").fill("mail-01");
    await page.getByLabel("Hostname (isteğe bağlı)").fill("mail01.example.internal");
    await page.getByRole("button", { name: "Kaydet" }).click();
    await expect(page.getByText("mail-01")).toBeVisible();
  }
  await row.getByRole("button", { name: "Detay" }).click();
}

test("mail node detay: e-posta bölümü görünür, deliverability açıklaması yerinde (kriter 3)", async ({ page }) => {
  // Gerçek modda agent yok → mail capability'si yok → panel DÜRÜST olarak
  // görünmez; ayrıca node oluşturmak smoke'ın boş-liste ön koşulunu bozar.
  test.skip(realBackend, "Gerçek modda agent yok; akış mock modda doğrulanır.");
  await login(page);
  await openMail01(page);

  const mail = page.getByRole("region", { name: /E-posta servisi/ });
  await expect(mail).toBeVisible();
  // Preflight yalnız RAPOR verir — kurulum butonu ASLA yoktur (kriter 1).
  await expect(mail.getByText("Ön uç taraması (preflight) — kurulum yapmaz, yalnız raporlar")).toBeVisible();
  await expect(mail.getByText("Preflight çalıştır")).toBeVisible();
  await expect(mail.getByRole("button", { name: /Kur/i })).toHaveCount(0);
  // Deliverability: SPF/DKIM/DMARC/PTR açıklamaları (kriter 3).
  await expect(mail.getByText("Teslim edilebilirlik (deliverability) gereksinimleri")).toBeVisible();
  for (const rec of ["SPF", "DKIM", "DMARC", "PTR / rDNS"]) {
    await expect(mail.getByText(rec, { exact: true })).toBeVisible();
  }
  // Dış DNS dürüstlük notu.
  await expect(mail.getByText(/dış DNS'te tutulur|DNS sağlayıcınızda tanımlanmalıdır/)).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "01-mail-panel.png"), fullPage: true });
});

test("mail domain + mailbox yaşam döngüsü ve tek seferlik şifre reveal", async ({ page }) => {
  test.skip(realBackend, "Gerçek modda agent yok; akış mock modda doğrulanır.");
  await login(page);
  await openMail01(page);

  const mail = page.getByRole("region", { name: /E-posta servisi/ });

  // Domain listesi: demo domain mock'ta hazır.
  await mail.getByRole("button", { name: "Listele" }).first().click();
  await expect(mail.getByTestId("mail-domains")).toContainText("mail.example.com");

  // Yeni mailbox oluştur → scrub edilmiş sonuç: credential_id + fingerprint.
  await mail.getByLabel("Domain").last().fill("mail.example.com");
  await mail.getByLabel("Yerel kısım").fill("ayse");
  await mail.getByRole("button", { name: "Oluştur", exact: true }).click();
  await expect(mail.getByTestId("mail-credential-note")).toBeVisible({ timeout: 15000 });
  await expect(mail.getByTestId("mail-credential-note")).toContainText("node'da üretildi");
  await page.screenshot({ path: path.join(shotDir, "02-mailbox-created.png"), fullPage: true });

  // Mailbox listesinde iki kutu: ali + ayse.
  await mail.getByRole("button", { name: "Listele" }).nth(1).click();
  await expect(mail.getByTestId("mail-mailboxes")).toContainText("ayse@mail.example.com", { timeout: 15000 });
  await expect(mail.getByTestId("mail-mailboxes")).toContainText("ali@mail.example.com");

  // Tek seferlik reveal.
  await mail.getByRole("button", { name: /Şifreyi göster/ }).first().click();
  await expect(mail.getByTestId("mail-revealed-password")).toBeVisible();
  await expect(mail.getByText("Bu şifre bir daha gösterilemez.")).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "03-password-revealed.png"), fullPage: true });

  // Takma ad: listele → hedefli satır.
  await mail.getByRole("button", { name: "Listele" }).nth(2).click();
  await expect(mail.getByTestId("mail-aliases")).toContainText("info@mail.example.com → ali@mail.example.com", { timeout: 15000 });
});

test("mail kuyruk içeriksiz görünür ve limitler kapalı enum'dur (kriter 5)", async ({ page }) => {
  test.skip(realBackend, "Gerçek modda agent yok; akış mock modda doğrulanır.");
  await login(page);
  await openMail01(page);

  const mail = page.getByRole("region", { name: /E-posta servisi/ });

  // Kuyruk: yalnız id/boyut/yaş/gönderici/neden — alıcı adresi YOK.
  await mail.getByRole("button", { name: "Kuyruğu getir" }).click();
  await expect(mail.getByTestId("mail-queue")).toBeVisible({ timeout: 15000 });
  const table = await mail.getByTestId("mail-queue").textContent();
  expect(table).toContain("3F1A2B4C5D");
  expect(table).toContain("bounce@mail.example.com");
  expect(table).not.toContain("secret-rcpt");
  await expect(mail.getByText(/Mesaj gövdesi ve alıcı adresleri gizlilik gereği hiçbir zaman gösterilmez/)).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "04-queue-and-limits.png"), fullPage: true });

  // Limitler: kapalı enum etiketleri görünür.
  await mail.getByRole("button", { name: "Mevcut değerler" }).click();
  await expect(mail.getByText("message_size_limit")).toBeVisible({ timeout: 15000 });
  await expect(mail.getByText("smtpd_client_message_rate_limit")).toBeVisible();
});

test("mail preflight raporu port rozetleri ve dürüst notlarla görünür (kriter 1)", async ({ page }) => {
  test.skip(realBackend, "Gerçek modda agent yok; akış mock modda doğrulanır.");
  await login(page);
  await openMail01(page);

  const mail = page.getByRole("region", { name: /E-posta servisi/ });
  await mail.getByRole("button", { name: "Preflight çalıştır" }).click();
  await expect(mail.getByTestId("mail-preflight")).toBeVisible({ timeout: 15000 });
  const pf = await mail.getByTestId("mail-preflight").textContent();
  expect(pf).toContain("Postfix");
  expect(pf).toContain("Dovecot");
  expect(pf).toContain("Rspamd");
  // Dürüstlük: dış DNS bağımlılığı notu raporun içinde geçer.
  expect(pf).toContain("dış DNS bağımlılığıdır");
});
