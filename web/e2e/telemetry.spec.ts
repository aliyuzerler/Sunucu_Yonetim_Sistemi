// Faz 04 E2E: node telemetri (sistem bilgileri + metrikler), kabiliyet
// beyanları ve tipli servis işlemleri. Mock modda zengin içerik doğrulanır;
// gerçek backend modunda (E2E_REAL=1) agent olmadığından dürüst boş durumlar
// ve görev oluşturma doğrulanır. Ekran görüntüleri
// docs/reviews/screenshots/phase-04 altına yazılır ve repoya commit edilir.
import { test, expect } from "@playwright/test";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const shotDir = path.resolve(here, "../../docs/reviews/screenshots/phase-04");
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

test("node detay: telemetri, kabiliyetler ve servis işlemleri", async ({ page }) => {
  await login(page);
  await page.getByRole("button", { name: /Sunucular/ }).click();

  // Node oluştur (mock ve gerçek modda da çalışır).
  await page.getByRole("button", { name: "Sunucu Ekle" }).click();
  await page.getByLabel("Görünen ad").fill("telemetry-01");
  await page.getByLabel("Hostname (isteğe bağlı)").fill("telemetry01.example.internal");
  await page.getByRole("button", { name: "Kaydet" }).click();
  await expect(page.getByText("telemetry-01")).toBeVisible();

  // Detay görünümü (hedef node'un satırından).
  await page.getByRole("row", { name: /telemetry-01/ }).getByRole("button", { name: "Detay" }).click();
  await expect(page.getByText("Sistem bilgileri (agent)")).toBeVisible();
  await expect(page.getByText("Metrikler")).toBeVisible();
  await expect(page.getByText("Kabiliyetler")).toBeVisible();
  await expect(page.getByText("Servis işlemleri")).toBeVisible();

  if (realBackend) {
    // Gerçek backend: agent bağlanmadı → dürüst boş durumlar.
    await expect(page.getByText("Agent henüz sistem bilgisi bildirmedi.")).toBeVisible();
    await expect(page.getByText("Henüz metrik anlık görüntüsü yok.")).toBeVisible();
    await expect(page.getByText("Agent henüz kabiliyet bildirmedi.")).toBeVisible();
  } else {
    // Mock: agent telemetrisi dolu.
    await expect(page.getByText("Ubuntu 24.04.3 LTS")).toBeVisible();
    await expect(page.getByText("6.8.0-49-generic")).toBeVisible();
    await expect(page.getByText("apt").first()).toBeVisible();
    await expect(page.getByText(/procfs\+statfs/).first()).toBeVisible();
    await expect(page.getByText(/\/dev\/vda1/)).toBeVisible();
    await expect(page.getByText("agent.echo")).toBeVisible();
  }
  await page.screenshot({ path: path.join(shotDir, "01-node-telemetry.png"), fullPage: true });
});

test("tipli servis görevi oluşturma (allowlist dışı birim istemcide reddedilir)", async ({ page }) => {
  await login(page);
  await page.getByRole("button", { name: /Sunucular/ }).click();
  await page.getByRole("row", { name: /telemetry-01/ }).getByRole("button", { name: "Detay" }).click();
  await expect(page.getByText("Servis işlemleri")).toBeVisible();

  // Geçersiz birim adı: istemci tarafı allowlist reddeder, istek gitmez.
  await page.getByLabel(/Servis birimi/).fill("../etc/passwd");
  await page.getByRole("button", { name: "Başlat", exact: true }).click();
  await expect(page.getByText(/Geçersiz birim adı/)).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "02-unit-rejected.png"), fullPage: true });

  // Geçerli birim: görev kuyruğa alınır.
  await page.getByLabel(/Servis birimi/).fill("nginx.service");
  await page.getByRole("button", { name: "Yeniden başlat" }).click();
  await expect(page.getByText(/Görev kuyruğa alındı/)).toBeVisible({ timeout: 15_000 });
  await page.screenshot({ path: path.join(shotDir, "03-service-task-queued.png"), fullPage: true });
});
