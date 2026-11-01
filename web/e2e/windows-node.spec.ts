// Faz 05 E2E: Windows node telemetrisi — SCM servis ailesi (windows.scm.*)
// ve Event Log okuma formu. Mock modda zengin içerik doğrulanır; gerçek
// backend modunda (E2E_REAL=1) agent olmadığından dürüst boş durumlar
// doğrulanır. Ekran görüntüleri docs/reviews/screenshots/phase-05 altına
// yazılır ve repoya commit edilir (sahip talimatı: testler + screenshot).
import { test, expect } from "@playwright/test";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const shotDir = path.resolve(here, "../../docs/reviews/screenshots/phase-05");
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

// win-01 UI'dan oluşturulur; MOCK modda "win-" öneki Windows telemetri
// şablonunu tetikler, gerçek modda agent olmadığından dürüst boş durumlar
// doğrulanır.
async function openWin01(page) {
  await page.getByRole("button", { name: /Sunucular/ }).click();
  const row = page.getByRole("row", { name: /win-01/ });
  if (!(await row.count())) {
    await page.getByRole("button", { name: "Sunucu Ekle" }).click();
    await page.getByLabel("Görünen ad").fill("win-01");
    await page.getByLabel("Hostname (isteğe bağlı)").fill("win01.example.internal");
    await page.getByRole("button", { name: "Kaydet" }).click();
    await expect(page.getByText("win-01")).toBeVisible();
  }
  await row.getByRole("button", { name: "Detay" }).click();
}

test("windows node detay: SCM ailesi ve event log formu görünür", async ({ page }) => {
  await login(page);
  await openWin01(page);

  if (realBackend) {
    // Gerçek backend: agent bağlanmadı → linux varsayılan ailesi + boş durumlar.
    await expect(page.getByText("Agent henüz sistem bilgisi bildirmedi.")).toBeVisible();
  } else {
    // Mock: Windows telemetrisi dolu.
    await expect(page.getByText("Windows Server 2022 Standard")).toBeVisible();
    await expect(page.getByText(/build 20348/)).toBeVisible();
    await expect(page.getByText("NTFS")).toBeVisible();
    await expect(page.getByText("agent.echo")).toBeVisible();
    await expect(page.getByText("windows.eventlog.read")).toBeVisible();
  }
  await page.screenshot({ path: path.join(shotDir, "01-windows-node.png"), fullPage: true });
});

test("windows SCM servis görevi: geçersiz ad istemcide reddedilir, geçerli ad kuyruğa girer", async ({ page }) => {
  test.skip(realBackend, "Gerçek modda agent yok; akış mock modda doğrulanır.");
  await login(page);
  await openWin01(page);
  await expect(page.getByText(/Servis işlemleri \(Windows SCM\)/)).toBeVisible();

  // Geçersiz SCM adı: istemci allowlist reddi, istek gitmez.
  await page.getByLabel(/Servis adı/).fill("-flag /evil");
  await page.getByRole("button", { name: "Başlat", exact: true }).click();
  await expect(page.getByText(/Geçersiz servis adı/)).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "02-scm-name-rejected.png"), fullPage: true });

  // Geçerli SCM adı: görev kuyruğa alınır.
  await page.getByLabel(/Servis adı/).fill("Dhcp");
  await page.getByRole("button", { name: "Yeniden başlat" }).click();
  await expect(page.getByText(/Görev kuyruğa alındı/)).toBeVisible({ timeout: 15_000 });
  await page.screenshot({ path: path.join(shotDir, "03-scm-task-queued.png"), fullPage: true });
});

test("event log okuma görevi oluşturma", async ({ page }) => {
  test.skip(realBackend, "Gerçek modda agent yok; akış mock modda doğrulanır.");
  await login(page);
  await openWin01(page);
  await expect(page.getByText("Olay günlüğü (Windows Event Log)")).toBeVisible();

  await page.getByLabel("Kanal").selectOption("Application");
  await page.getByLabel("Seviye").selectOption("2");
  await page.getByLabel(/Olay sayısı/).fill("25");
  await page.getByRole("button", { name: "Olayları oku" }).click();
  await expect(page.getByText(/Görev kuyruğa alındı/)).toBeVisible({ timeout: 15_000 });
  await page.screenshot({ path: path.join(shotDir, "04-eventlog-task-queued.png"), fullPage: true });
});
