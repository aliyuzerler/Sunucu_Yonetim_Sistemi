// UX denetimi P1 (m.4.1–m.4.5) E2E: hash derin bağlantı + geri/ileri,
// dil kalıcılığı, node detay adresleme, tek-gösterim token kopyalama,
// ConfirmDialog + toast akışı. Ekran görüntüleri
// docs/reviews/screenshots/ux-p1 altına yazılır.
import { expect, test } from "@playwright/test";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const shotDir = path.resolve(here, "../../docs/reviews/screenshots/ux-p1");
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

test("hash derin bağlantı: yenileme ve modül adresi korunur (m.4.1)", async ({ page }) => {
  test.skip(realBackend, "Derin bağlantı mock modda doğrulanır.");
  await login(page);

  await page.goto("/#/dns");
  await expect(page.getByRole("heading", { name: "Alan Adları ve DNS" })).toBeVisible();
  await page.reload();
  await expect(page.getByRole("heading", { name: "Alan Adları ve DNS" })).toBeVisible();

  await page.goto("/#/docker");
  await expect(page.getByText("Bu modül henüz uygulanmadı")).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "01-deep-link.png"), fullPage: true });
});

test("geri/ileri tuşları hash ile senkron (m.4.1)", async ({ page }) => {
  test.skip(realBackend, "Gezinme mock modda doğrulanır.");
  await login(page);

  await page.getByRole("button", { name: /Görevler ve Hareketler/ }).click();
  await expect(page).toHaveURL(/#\/tasks/);
  await page.getByRole("button", { name: /Denetim Kaydı/ }).click();
  await expect(page).toHaveURL(/#\/audit/);

  await page.goBack();
  await expect(page).toHaveURL(/#\/tasks/);
  await page.goForward();
  await expect(page).toHaveURL(/#\/audit/);
  await page.screenshot({ path: path.join(shotDir, "02-history.png"), fullPage: true });
});

test("dil tercihi yenileme sonrası da kalıcı (m.4.5)", async ({ page }) => {
  test.skip(realBackend, "Tercih kalıcılığı mock modda doğrulanır.");
  await login(page);

  await page.getByRole("button", { name: "EN", exact: true }).click();
  await expect(page.getByRole("button", { name: /^Nodes/ })).toBeVisible();

  await page.reload();
  // Kalıcılık: yenileme sonrası hâlâ İngilizce.
  await expect(page.getByRole("button", { name: /^Nodes/ })).toBeVisible();

  await page.getByRole("button", { name: "TR", exact: true }).click();
  await expect(page.getByRole("button", { name: /Sunucular/ })).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "03-locale.png"), fullPage: true });
});

test("node detayı adreslenir: Detay → #/nodes/<id> → geri → #/nodes (m.4.2)", async ({ page }) => {
  test.skip(realBackend, "Node akışı mock modda doğrulanır.");
  await login(page);

  await page.getByRole("button", { name: /Sunucular/ }).click();
  await page.getByRole("button", { name: "Sunucu Ekle" }).click();
  await page.getByLabel("Görünen ad").fill("ux-p1-node");
  await page.getByRole("button", { name: "Kaydet" }).click();
  const nodeRow = page.getByRole("row", { name: /ux-p1-node/ });
  await expect(nodeRow).toBeVisible();

  // Liste başka spec'lerden kalan node'lar içerebilir; satıra göre daralt.
  await nodeRow.getByRole("button", { name: "Detay" }).click();
  await expect(page).toHaveURL(/#\/nodes\/[0-9a-f-]{36}/);
  await expect(page.getByRole("heading", { name: "ux-p1-node" })).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "04-node-deep-link.png"), fullPage: true });

  await page.getByRole("button", { name: /Node listesine dön/ }).click();
  await expect(page).toHaveURL(/#\/nodes$/);
  await expect(page.getByRole("button", { name: "Sunucu Ekle" })).toBeVisible();
});

test("tek gösterim token tek tıkla kopyalanır (m.4.4)", async ({ page, context }) => {
  test.skip(realBackend, "Token reveal mock modda doğrulanır.");
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  await login(page);

  // Grup oluştur (kapsam=group için).
  await page.getByRole("button", { name: /Sunucular/ }).click();
  await page.getByRole("button", { name: "Sunucu Ekle" }).click();
  await page.getByPlaceholder("yeni-grup-adi").fill("ux-p1-grp");
  await page.getByRole("button", { name: "Grup Oluştur" }).click();
  await expect(page.getByText("ux-p1-grp").first()).toBeVisible();

  await page.getByRole("button", { name: /Agent Kaydı/ }).click();
  await page.getByLabel("Kapsam").selectOption("group");
  await page.getByLabel("Hedef grup").selectOption({ label: "ux-p1-grp" });
  await page.getByRole("button", { name: "Token Üret" }).click();

  await expect(page.getByText("Token bir kez gösterilir")).toBeVisible();
  const reveal = page.getByLabel("Token bir kez gösterilir");
  const tokenText = (await reveal.locator("code").innerText()).trim();
  expect(tokenText.length).toBeGreaterThan(0);

  const copyBtn = reveal.getByTestId("copy-button");
  await copyBtn.click();
  await expect(copyBtn).toHaveText("Kopyalandı");
  const clip = await page.evaluate(() => navigator.clipboard.readText());
  expect(clip).toBe(tokenText);
  await page.screenshot({ path: path.join(shotDir, "05-copy-token.png"), fullPage: true });
});

test("sağlayıcı silme ConfirmDialog + toast ile bildirilir (m.4.3)", async ({ page }) => {
  test.skip(realBackend, "Sağlayıcı CRUD mock modda doğrulanır.");
  await login(page);

  await page.getByRole("button", { name: /Alan Adları ve DNS/ }).click();
  await page.getByLabel("Sağlayıcı adı").fill("ux-p1-sil");
  await page.getByLabel("API anahtarı (yalnız yazılır)").fill("gizli-ux-p1-anahtar");
  await page.getByRole("button", { name: "Sağlayıcı Ekle" }).click();

  const row = page.getByRole("row", { name: /ux-p1-sil/ });
  await expect(row).toBeVisible();

  await row.getByRole("button", { name: "Sil", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Onay bekleniyor" });
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText("Sağlayıcı silinsin mi?");

  await dialog.getByRole("button", { name: "Onayla" }).click();
  await expect(page.getByTestId("toast")).toContainText("DNS sağlayıcısı silindi.");
  await expect(page.getByRole("row", { name: /ux-p1-sil/ })).toHaveCount(0);
  await page.screenshot({ path: path.join(shotDir, "06-confirm-toast.png"), fullPage: true });
});
