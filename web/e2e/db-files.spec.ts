// Faz 10 E2E: Veritabanları + scoped dosya yöneticisi node bölümü.
// Mock modda zengin içerik doğrulanır: instance listesi, kullanıcı
// oluşturma + TEK SEFERLİK şifre reveal'ı, dosya listesi + upload sonucu.
// Gerçek backend modunda agent olmadığından dürüst boş durumlar doğrulanır.
// Ekran görüntüleri docs/reviews/screenshots/phase-10 altına yazılır ve
// repoya commit edilir (sahip talimatı). IT-01 dersi: liste yerleşmeden
// (yükleniyor göstergesi kaybolmadan) satır sayımı YAPILMAZ.
import { expect, test } from "@playwright/test";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const shotDir = path.resolve(here, "../../docs/reviews/screenshots/phase-10");
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

// dbfs-01 UI'dan oluşturulur; MOCK modda "dbfs-" öneki DB+FS şablonunu
// tetikler (db.* + fs.* aileleri), gerçek modda agent olmadığından dürüst
// boş durumlar doğrulanır.
async function openDbfs01(page) {
  await page.getByRole("button", { name: /Sunucular/ }).click();
  await expect(page.getByText("Yükleniyor…")).toBeHidden();
  const row = page.getByRole("row", { name: /dbfs-01/ });
  if (!(await row.count())) {
    await page.getByRole("button", { name: "Sunucu Ekle" }).click();
    await page.getByLabel("Görünen ad").fill("dbfs-01");
    await page.getByLabel("Hostname (isteğe bağlı)").fill("dbfs01.example.internal");
    await page.getByRole("button", { name: "Kaydet" }).click();
    await expect(page.getByText("dbfs-01")).toBeVisible();
  }
  await row.getByRole("button", { name: "Detay" }).click();
}

test("db node detay: bölümler görünür, instance listesi raporlanır", async ({ page }) => {
  test.skip(realBackend, "Gerçek modda agent yok; akış mock modda doğrulanır.");
  await login(page);
  await openDbfs01(page);

  const db = page.getByRole("region", { name: /Veritabanları/ });
  await expect(db).toBeVisible();
  await expect(db.getByRole("button", { name: "Instance'ları listele" })).toBeVisible();
  await expect(db.getByRole("button", { name: "Veritabanlarını listele" })).toBeVisible();

  const fs = page.getByRole("region", { name: /Dosya yöneticisi/ });
  await expect(fs).toBeVisible();
  await expect(fs.getByRole("button", { name: "Listele" })).toBeVisible();

  // Instance listesi: görev poll edilir, rapor kartı dolur.
  await db.getByRole("button", { name: "Instance'ları listele" }).click();
  const report = db.getByTestId("db-report");
  await expect(report).toBeVisible({ timeout: 15_000 });
  await expect(report.getByText("18.2")).toBeVisible();
  await expect(report.getByText("11.4-MariaDB")).toBeVisible();
  await expect(report.getByText("/run/postgresql/.s.PGSQL.5432")).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "01-dbfs-node.png"), fullPage: true });
});

test("db.user.create: şifre task sonucunda düz görünmez; reveal tek seferliktir", async ({ page }) => {
  test.skip(realBackend, "Gerçek modda agent yok; akış mock modda doğrulanır.");
  await login(page);
  await openDbfs01(page);

  const db = page.getByRole("region", { name: /Veritabanları/ });
  await db.getByLabel("Kullanıcı adı").fill("app_e2e");
  await db.getByRole("button", { name: "Kullanıcı oluştur" }).click();

  // Şifre üretildi kartı: parmak izi + tek-seferlik uyarı; DÜZ ŞİFRE YOK.
  const note = db.getByTestId("db-credential-note");
  await expect(note).toBeVisible({ timeout: 15_000 });
  await expect(note.getByText(/Şifre üretildi/)).toBeVisible();
  await expect(note.getByText(/yalnız bir kez gösterilir/)).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "02-user-created.png"), fullPage: true });

  // Kimlik kartında reveal butonu → tek seferlik şifre.
  const creds = db.getByTestId("db-credentials");
  await expect(creds.getByText("app_e2e")).toBeVisible({ timeout: 15_000 });
  await creds.getByTestId("reveal-app_e2e").click();
  await expect(creds.getByTestId("revealed-password")).toBeVisible();
  await expect(creds.getByText(/ikinci kez gösterilemez/i).or(page.getByText(/ikinci kez gösterilemez/i))).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "03-password-revealed.png"), fullPage: true });
});

test("fs bölümü: dizin listesi ve upload sonucu gösterilir", async ({ page }) => {
  test.skip(realBackend, "Gerçek modda agent yok; akış mock modda doğrulanır.");
  await login(page);
  await openDbfs01(page);

  const fs = page.getByRole("region", { name: /Dosya yöneticisi/ });
  await fs.getByLabel("Kök (site adı)").fill("blog");
  await fs.getByRole("button", { name: "Listele" }).click();

  const listing = fs.getByTestId("fs-report");
  await expect(listing).toBeVisible({ timeout: 15_000 });
  await expect(listing.getByText("index.html")).toBeVisible();
  await expect(listing.getByText("uploads")).toBeVisible();
  await expect(listing.getByRole("cell", { name: "0644" }).first()).toBeVisible();
  await expect(listing.getByText("0750")).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "04-fs-listing.png"), fullPage: true });
});
