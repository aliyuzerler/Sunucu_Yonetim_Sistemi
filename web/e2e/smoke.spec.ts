// Faz 02 E2E smoke: giriş → genel bakış → sunucular → görevler → denetim
// kaydı → dil değişimi. Adım adım ekran görüntüsü üretir; başarılı görüntüler
// docs/reviews/screenshots/phase-02 altına yazılır (repoya commit edilir).
import { test, expect } from "@playwright/test";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const shotDir = path.resolve(here, "../../docs/reviews/screenshots/phase-02");

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


test("giriş sayfası ve oturum açma akışı", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByLabel("Oturum açma")).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "01-login.png"), fullPage: true });

  await page.getByLabel("Kullanıcı adı").fill(ADMIN);
  await page.getByLabel("Parola").fill(PASSWORD);
  await page.getByRole("button", { name: "Giriş yap" }).click();

  await expect(page.getByRole("navigation", { name: "Ana gezinme" })).toBeVisible();
  await expect(page.getByText("admin", { exact: true })).toBeVisible();
  await expect(page.getByText("İzleme modülü etkin değil")).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "02-overview.png"), fullPage: true });
});

test("sunucu oluşturma ve listeleme", async ({ page }) => {
  await login(page);
  await page.getByRole("button", { name: /Sunucular/ }).click();

  await expect(page.getByText("Henüz kayıtlı sunucu yok")).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "03-nodes-empty.png"), fullPage: true });

  await page.getByRole("button", { name: "Sunucu Ekle" }).click();
  await page.getByLabel("Görünen ad").fill("web-01");
  await page.getByLabel("Hostname (isteğe bağlı)").fill("web01.example.internal");
  await page.getByLabel("Etiketler (virgülle ayırın)").fill("prod, web");
  await page.getByRole("button", { name: "Kaydet" }).click();

  await expect(page.getByText("web-01")).toBeVisible();
  await expect(page.getByText("web01.example.internal")).toBeVisible();
  await expect(page.getByText("Beklemede")).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "04-node-created.png"), fullPage: true });
});

test("kalıcı görev oluşturma ve tamamlanma", async ({ page }) => {
  await login(page);
  await page.getByRole("button", { name: /Görevler ve Hareketler/ }).click();

  await expect(page.getByText("Görev geçmişi henüz yok")).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "05-tasks-empty.png"), fullPage: true });

  await page.getByLabel("Operasyon (tipli kayıt)").selectOption("noop.echo");
  await page.getByLabel("Payload (JSON)").fill('{"mesaj":"merhaba e2e"}');
  await page.getByRole("button", { name: "Görev Oluştur" }).click();

  await expect(page.getByText("Görev kuyruğa alındı.")).toBeVisible();
  // Mock worker 400ms içinde succeeded yapar; sayfa yeniden yüklenerek doğrulanır.
  await expect(async () => {
    await page.reload();
    await page.getByRole("button", { name: /Görevler ve Hareketler/ }).click();
    await expect(page.getByText("Başarılı").first()).toBeVisible({ timeout: 3_000 });
  }).toPass({ timeout: 30_000 });
  await page.screenshot({ path: path.join(shotDir, "06-task-succeeded.png"), fullPage: true });
});

test("denetim kaydı görünür", async ({ page }) => {
  await login(page);

  // Bağımsızlık: bu test kendi task.create olayını üretir.
  await page.getByRole("button", { name: /Görevler ve Hareketler/ }).click();
  await page.getByLabel("Operasyon (tipli kayıt)").selectOption("noop.echo");
  await page.getByRole("button", { name: "Görev Oluştur" }).click();
  await expect(page.getByText("Görev kuyruğa alındı.")).toBeVisible();

  await page.getByRole("button", { name: /Denetim Kaydı/ }).click();
  await expect(page.getByText("auth.login").first()).toBeVisible();
  await expect(page.getByText("task.create").first()).toBeVisible();
  await expect(page.getByText("salt-eklenebilir", { exact: false })).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "07-audit.png"), fullPage: true });
});

test("İngilizce yerel ayar", async ({ page }) => {
  await login(page);
  await page.getByRole("button", { name: "EN", exact: true }).click();
  await expect(page.getByText("Infrastructure").first()).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "08-locale-en.png"), fullPage: true });
});
