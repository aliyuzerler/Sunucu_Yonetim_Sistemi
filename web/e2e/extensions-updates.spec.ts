// Faz 15 E2E: eklenti sözleşmesi (imzalı manifest listesi, istenen izinler,
// onay akışı — kriter 1) ve imzalı güncelleme kayıt defteri (yayınlama/
// iptal + node update akışı — kriter 2). Mock modda zengin içerik doğrulanır;
// gerçek backend modunda agent olmadığı için mock modda koşar (backup-cloud
// kalıbı). Ekran görüntüleri docs/reviews/screenshots/phase-15 altına yazılır
// ve repoya commit edilir.
import { expect, test } from "@playwright/test";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const shotDir = path.resolve(here, "../../docs/reviews/screenshots/phase-15");
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

test("eklentiler: imzalı manifest listesi + istenen izinler + onay rozetleri (kriter 1)", async ({ page }) => {
  test.skip(realBackend, "Gerçek modda agent yok; akış mock modda doğrulanır.");
  await login(page);

  await page.getByRole("button", { name: "Modüller" }).click();
  const list = page.getByRole("region", { name: /Kayıtlı eklentiler/ });
  await expect(list).toBeVisible();
  await expect(page.getByText("Site Audit Helper")).toBeVisible({ timeout: 15000 });
  await expect(page.getByText("backup:read")).toBeVisible();
  await expect(page.getByText(/Yedek listesini okuyup rapor üretir/)).toBeVisible();
  // Varsayılan izin yok: onay bekleyen rozet + kural notu.
  await expect(page.getByText("Onay bekliyor").first()).toBeVisible();
  await expect(page.getByText(/VARSAYILAN İZİN YOKTUR/)).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "01-extensions-list.png"), fullPage: true });
});

test("eklentiler: izin onayı → Onaylı rozeti + audit", async ({ page }) => {
  test.skip(realBackend, "Gerçek modda agent yok; akış mock modda doğrulanır.");
  await login(page);

  await page.getByRole("button", { name: "Modüller" }).click();
  await expect(page.getByText("Site Audit Helper")).toBeVisible({ timeout: 15000 });

  // UX denetimi m.4.3: onay artık uygulama içi dialog ile yapılır
  // (window.confirm kaldırıldı — dialog listener'ı gereksiz).
  const row = page.getByRole("listitem").filter({ hasText: "Site Audit Helper" }).last();
  await row.getByRole("button", { name: "Onayla" }).first().click();
  const dialog = page.getByRole("dialog", { name: "Onay bekleniyor" });
  await expect(dialog).toBeVisible();
  await dialog.getByRole("button", { name: "Onayla" }).click();
  await expect(page.getByText("İşlem tamamlandı.")).toBeVisible({ timeout: 15000 });
  await page.screenshot({ path: path.join(shotDir, "02-extension-approve.png"), fullPage: true });
});

test("güncellemeler: artifact kayıt defteri + sürüm/üzeri üst veri (kriter 2/3)", async ({ page }) => {
  test.skip(realBackend, "Gerçek modda agent yok; akış mock modda doğrulanır.");
  await login(page);

  await page.getByRole("button", { name: "Güncellemeler" }).click();
  const list = page.getByRole("region", { name: /Sürüm artifact'ları/ });
  await expect(list).toBeVisible();
  await expect(page.getByText("agent_linux_amd64")).toBeVisible({ timeout: 15000 });
  await expect(page.getByText("v0.16.0-rc1")).toBeVisible();
  await expect(page.getByText(/server-control-agent_0.16.0-rc1_amd64.deb/)).toBeVisible();
  await expect(page.getByText("Yayımlı", { exact: true })).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "03-updates-artifacts.png"), fullPage: true });
});

test("güncellemeler: node update akışı — status sorgusu task sonucuyla döner (kriter 2)", async ({ page }) => {
  test.skip(realBackend, "Gerçek modda agent yok; akış mock modda doğrulanır.");
  await login(page);

  // Node UI'dan oluşturulur (monitoring-logs kalıbı — mock node listesi
  // UI oluşturmayla dolar).
  await page.getByRole("button", { name: /Sunucular/ }).click();
  await expect(page.getByText("Yükleniyor…")).toBeHidden();
  const row = page.getByRole("row", { name: /upd-01/ });
  if (!(await row.count())) {
    await page.getByRole("button", { name: "Sunucu Ekle" }).click();
    await page.getByLabel("Görünen ad").fill("upd-01");
    await page.getByRole("button", { name: "Kaydet" }).click();
    await expect(page.getByText("upd-01")).toBeVisible();
  }

  await page.getByRole("button", { name: "Güncellemeler" }).click();
  const nodeSection = page.getByRole("region", { name: /Node güncelleme durumu/ });
  await expect(nodeSection).toBeVisible();

  await page.getByLabel("Node seç").selectOption({ label: "upd-01" });
  await nodeSection.getByRole("button", { name: /Durumu sorgula/ }).click();
  await expect(page.getByText(/update_enabled/)).toBeVisible({ timeout: 20000 });
  await page.screenshot({ path: path.join(shotDir, "04-updates-node-status.png"), fullPage: true });
});
