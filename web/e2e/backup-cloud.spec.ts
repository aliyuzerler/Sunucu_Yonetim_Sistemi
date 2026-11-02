// Faz 14 E2E: yedekleme (CP + node ayrı kapsamlar, doğrulama durumu,
// izole restore tatbikatı — kriter 1/2) ve bulut sağlayıcı bağdaştırıcıları
// (izin matrisi + onay kapılı kaynak silme — kriter 5).
// Mock modda zengin içerik doğrulanır; gerçek backend modunda agent olmadığı
// için mock modda koşar (monitoring-logs kalıbı). Ekran görüntüleri
// docs/reviews/screenshots/phase-14 altına yazılır ve repoya commit edilir.
// IT-01 dersi: liste yerleşmeden işlem YAPILMAZ.
import { expect, test } from "@playwright/test";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const shotDir = path.resolve(here, "../../docs/reviews/screenshots/phase-14");
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

test("yedekler: CP ve node kapsamları ayrı bölümlerde, doğrulama rozetleri dürüst (kriter 1/2)", async ({ page }) => {
  test.skip(realBackend, "Gerçek modda agent yok; akış mock modda doğrulanır.");
  await login(page);

  await page.getByRole("button", { name: "Yedekler" }).click();
  const cpSection = page.getByRole("region", { name: /Kontrol Paneli yedeği/ });
  await expect(cpSection).toBeVisible();
  // CP yedeği: doğrulanmış rozet + özet + izole tatbikat butonu.
  await expect(cpSection.getByText("Başarılı", { exact: true })).toBeVisible();
  await expect(cpSection.getByText("Doğrulandı", { exact: true })).toBeVisible();
  await expect(cpSection.getByText(/tablo=27 satır=142/)).toBeVisible();

  const nodeSection = page.getByRole("region", { name: /Node veri yedeği/ });
  await expect(nodeSection).toBeVisible();
  // Node yedeği: restic snapshot kimliği + doğrulama detayı.
  await expect(nodeSection.getByText(/abcd1234ef567890/)).toBeVisible();

  // Tatbikat bölümü: izole hedef adı görünür (scp_drill_ öneki — kriter 2).
  const drills = page.getByRole("region", { name: /Restore tatbikatları/ });
  await expect(drills.getByText(/scp_drill_deadbeef/).first()).toBeVisible();
  await expect(page.getByText(/Canlı veriye restore bu sürümde yoktur/)).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "01-backups-overview.png"), fullPage: true });
});

test("yedekler: CP yedeği al → görev tamamlanır → yeni kayıt listeye düşer", async ({ page }) => {
  test.skip(realBackend, "Gerçek modda agent yok; akış mock modda doğrulanır.");
  await login(page);

  await page.getByRole("button", { name: "Yedekler" }).click();
  const cpSection = page.getByRole("region", { name: /Kontrol Paneli yedeği/ });
  await expect(cpSection.getByText("Başarılı", { exact: true })).toBeVisible({ timeout: 15000 });

  await page.getByRole("button", { name: "Kontrol Paneli Yedeği Al" }).click();
  await expect(page.getByText("İşlem tamamlandı.")).toBeVisible({ timeout: 20000 });
  // Yeni kayıt (mock worker üretimi) listede görünür — iki Başarılı satırı.
  await expect(cpSection.getByText("Başarılı", { exact: true })).toHaveCount(2);
  await page.screenshot({ path: path.join(shotDir, "02-backup-cp-run.png"), fullPage: true });
});

test("bulut: sağlayıcı listesi + izin matrisi + kimlik denemesi (kriter 5)", async ({ page }) => {
  test.skip(realBackend, "Gerçek modda agent yok; akış mock modda doğrulanır.");
  await login(page);

  await page.getByRole("button", { name: "Entegrasyonlar" }).click();
  await expect(page.getByText("demo-object-storage")).toBeVisible({ timeout: 15000 });
  await expect(page.getByText("s3", { exact: true })).toBeVisible();
  await expect(page.getByText(/Her sağlayıcı kendi kapsam\/kimlik\/izin matrisiyle/)).toBeVisible();

  // Verify → izin matrisi tablosu + probe sonucu.
  await page.getByRole("button", { name: "Kimliği doğrula" }).click();
  await expect(page.getByText("s3:ListAllMyBuckets").first()).toBeVisible({ timeout: 15000 });
  await expect(page.getByText("s3:DeleteBucket").first()).toBeVisible();
  await expect(page.getByText("Kanıtlandı", { exact: true })).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "03-cloud-provider-verify.png"), fullPage: true });
});

test("bulut: kaynak listesi, dolu kaynak red ve onaylı silme (kriter 5)", async ({ page }) => {
  test.skip(realBackend, "Gerçek modda agent yok; akış mock modda doğrulanır.");
  await login(page);

  await page.getByRole("button", { name: "Entegrasyonlar" }).click();
  await expect(page.getByText("demo-object-storage")).toBeVisible({ timeout: 15000 });

  await page.getByRole("button", { name: "Kaynakları listele" }).click();
  await expect(page.getByText("site-backups")).toBeVisible({ timeout: 15000 });
  await expect(page.getByText("db-dumps-full")).toBeVisible();

  // Dolu kaynak: onay verilse bile sağlayıcı reddedilir → dürüst hata.
  const fullRow = page.getByRole("listitem").filter({ hasText: "db-dumps-full" }).last();
  await fullRow.getByRole("button", { name: "Sil", exact: true }).click();
  await fullRow.getByRole("button", { name: "Kalıcı sil" }).click();
  await expect(page.getByText(/panel zorla silmez|boş değil/)).toBeVisible({ timeout: 15000 });

  // Boş kaynak: iki adımlı onay → silinir, listeden düşer.
  const emptyRow = page.getByRole("listitem").filter({ hasText: "site-backups" }).last();
  await emptyRow.getByRole("button", { name: "Sil", exact: true }).click();
  await expect(page.getByText(/kalıcı siler/)).toBeVisible();
  await emptyRow.getByRole("button", { name: "Kalıcı sil" }).click();
  await expect(page.getByText("Kaynak silindi.")).toBeVisible({ timeout: 15000 });
  await expect(page.getByText("site-backups")).toHaveCount(0);
  await page.screenshot({ path: path.join(shotDir, "04-cloud-delete-confirm.png"), fullPage: true });
});
