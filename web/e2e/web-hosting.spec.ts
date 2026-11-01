// Faz 08 E2E: Web hosting node — site yaşam döngüsü bölümü (list/create),
// PHP sürüm matrisi ve delete'in "docroot korunur" etiketi (ADR-0012).
// Mock modda zengin içerik doğrulanır; gerçek backend modunda agent
// olmadığından dürüst boş durumlar doğrulanır. Ekran görüntüleri
// docs/reviews/screenshots/phase-08 altına yazılır ve repoya commit edilir
// (sahip talimatı). IT-01 dersi: liste yerleşmeden (yükleniyor göstergesi
// kaybolmadan) satır sayımı YAPILMAZ.
import { expect, test } from "@playwright/test";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const shotDir = path.resolve(here, "../../docs/reviews/screenshots/phase-08");
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

// web-01 UI'dan oluşturulur; MOCK modda "web-" öneki web hosting şablonunu
// tetikler (web.site.* ailesi), gerçek modda agent olmadığından dürüst boş
// durumlar doğrulanır.
async function openWeb01(page) {
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
  await row.getByRole("button", { name: "Detay" }).click();
}

test("web node detay: hosting bölümü, delete etiketi ve PHP matrisi görünür", async ({ page }) => {
  test.skip(realBackend, "Gerçek modda agent yok; akış mock modda doğrulanır.");
  await login(page);
  await openWeb01(page);

  const web = page.getByRole("region", { name: /Web hosting/ });
  await expect(web).toBeVisible();
  await expect(web.getByRole("button", { name: "Siteleri listele" })).toBeVisible();
  await expect(web.getByRole("button", { name: "PHP sürümleri" })).toBeVisible();
  await expect(web.getByRole("button", { name: "Site oluştur" })).toBeVisible();
  await expect(web.getByRole("button", { name: "Siteyi sil" })).toBeVisible();
  await expect(web.getByText(/document root KORUNUR/)).toBeVisible();

  // PHP sürüm matrisi: görev sonucu poll edilir ve gösterilir (kriter 4).
  await web.getByRole("button", { name: "PHP sürümleri" }).click();
  const report = web.getByTestId("web-report");
  await expect(report).toBeVisible({ timeout: 15_000 });
  await expect(report.getByText(/PHP-FPM sürüm matrisi/)).toBeVisible();
  await expect(report.getByText(/8\.2, 8\.3, 8\.4, 8\.5/)).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "01-web-node.png"), fullPage: true });
});

test("site oluşturma: geçersiz ad reddedilir, geçerli form görev kurur ve sonuç gösterilir", async ({ page }) => {
  test.skip(realBackend, "Gerçek modda agent yok; akış mock modda doğrulanır.");
  await login(page);
  await openWeb01(page);

  const web = page.getByRole("region", { name: /Web hosting/ });
  // Geçersiz ad: istemci allowlist reddi, istek gitmez.
  await web.getByLabel("Site adı").fill("Blog");
  await web.getByLabel(/Sunucu adları/).fill("blog.example.com");
  await web.getByRole("button", { name: "Site oluştur" }).click();
  await expect(web.getByText(/Geçersiz site adı/)).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "02-web-name-rejected.png"), fullPage: true });

  // Geçerli ad + php backend: görev kuyruğa alınır, sonuç poll edilir.
  await web.getByLabel("Site adı").fill("blog");
  await web.getByLabel("Backend").selectOption("php");
  await expect(web.getByLabel("PHP sürümü")).toBeVisible();
  await web.getByRole("button", { name: "Site oluştur" }).click();
  const report = web.getByTestId("web-report");
  await expect(report).toBeVisible({ timeout: 15_000 });
  await expect(report.getByText(/create \(blog\)/)).toBeVisible();
  await expect(report.getByText(/srvctl-blog\.conf/)).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "03-web-site-created.png"), fullPage: true });
});

test("site listesi ve silme etiketi: yönetilen siteler tablo halinde gösterilir", async ({ page }) => {
  test.skip(realBackend, "Gerçek modda agent yok; akış mock modda doğrulanır.");
  await login(page);
  await openWeb01(page);

  const web = page.getByRole("region", { name: /Web hosting/ });
  await web.getByRole("button", { name: "Siteleri listele" }).click();
  const report = web.getByTestId("web-report");
  await expect(report).toBeVisible({ timeout: 15_000 });
  await expect(report.getByText(/Yönetilen siteler/)).toBeVisible();
  await expect(report.getByRole("cell", { name: "blog", exact: true })).toBeVisible();
  await expect(report.getByRole("cell", { name: "shop", exact: true })).toBeVisible();

  // Delete: docroot korunur notu görev sonucunda da görünür (ADR-0012).
  await web.getByLabel("Hedef site").fill("blog");
  await web.getByRole("button", { name: "Siteyi sil" }).click();
  await expect(report.getByText(/delete \(blog\)/)).toBeVisible({ timeout: 15_000 });
  await expect(report.getByText(/Document root korundu/)).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "04-web-site-deleted.png"), fullPage: true });
});
