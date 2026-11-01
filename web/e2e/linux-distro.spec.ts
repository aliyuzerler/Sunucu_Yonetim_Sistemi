// Faz 06 E2E: Rocky Linux node telemetrisi — distro sınıflandırması
// (destek durumu + aile) ve salt-okuma dağıtım işlemleri (paket envanteri +
// firewall sörveyi + kural önizlemesi; önizleme asla çalıştırmaz — ADR-0010).
// Mock modda zengin içerik doğrulanır; gerçek backend modunda (E2E_REAL=1)
// agent olmadığından dürüst boş durumlar doğrulanır. Ekran görüntüleri
// docs/reviews/screenshots/phase-06 altına yazılır ve repoya commit edilir
// (sahip talimatı: testler + screenshot).
import { test, expect } from "@playwright/test";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const shotDir = path.resolve(here, "../../docs/reviews/screenshots/phase-06");
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

// rocky-01 UI'dan oluşturulur; MOCK modda "rocky-" öneki Rocky telemetri
// şablonunu tetikler, gerçek modda agent olmadığından dürüst boş durumlar
// doğrulanır.
async function openRocky01(page) {
  await page.getByRole("button", { name: /Sunucular/ }).click();
  const row = page.getByRole("row", { name: /rocky-01/ });
  if (!(await row.count())) {
    await page.getByRole("button", { name: "Sunucu Ekle" }).click();
    await page.getByLabel("Görünen ad").fill("rocky-01");
    await page.getByLabel("Hostname (isteğe bağlı)").fill("rocky01.example.internal");
    await page.getByRole("button", { name: "Kaydet" }).click();
    await expect(page.getByText("rocky-01")).toBeVisible();
  }
  await row.getByRole("button", { name: "Detay" }).click();
}

test("rocky node detay: distro sınıflandırması ve dağıtım işlemleri bölümü görünür", async ({ page }) => {
  await login(page);
  await openRocky01(page);

  if (realBackend) {
    // Gerçek backend: agent bağlanmadı → dürüst boş durumlar.
    await expect(page.getByText("Agent henüz sistem bilgisi bildirmedi.")).toBeVisible();
  } else {
    // Mock: Rocky telemetrisi dolu — destek rozeti + aile + survey bölümü.
    await expect(page.getByText("Rocky Linux 9.5 (Blue Onyx)")).toBeVisible();
    await expect(page.getByText(/5\.14\.0-503\.22\.1\.el9_5/)).toBeVisible();
    await expect(page.getByText("Destekleniyor")).toBeVisible();
    await expect(page.getByText(/desteklenen majör sürüm 9/)).toBeVisible();
    await expect(page.getByText("rhel")).toBeVisible();
    await expect(page.getByText(/Dağıtım işlemleri/)).toBeVisible();
    await expect(page.getByText("linux.firewall.rule.preview")).toBeVisible();
  }
  await page.screenshot({ path: path.join(shotDir, "01-rocky-node.png"), fullPage: true });
});

test("paket listesi görevi: limit doğrulaması ve kuyruğa alma", async ({ page }) => {
  test.skip(realBackend, "Gerçek modda agent yok; akış mock modda doğrulanır.");
  await login(page);
  await openRocky01(page);
  await expect(page.getByText(/Dağıtım işlemleri/)).toBeVisible();

  // Geçersiz limit (501): istemci reddi, istek gitmez.
  await page.getByLabel(/Paket limiti/).fill("501");
  await page.getByRole("button", { name: "Paket listesi görevi" }).click();
  await expect(page.getByText(/Limit 1-500 arasında olmalı/)).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "02-pkg-limit-rejected.png"), fullPage: true });

  // Geçerli limit: görev kuyruğa alınır.
  await page.getByLabel(/Paket limiti/).fill("100");
  await page.getByRole("button", { name: "Paket listesi görevi" }).click();
  await expect(page.getByText(/Görev kuyruğa alındı/)).toBeVisible({ timeout: 15_000 });
  await page.screenshot({ path: path.join(shotDir, "03-pkg-task-queued.png"), fullPage: true });
});

test("firewall kural önizlemesi: geçersiz bölge reddedilir, geçerli önizleme kuyruğa girer", async ({ page }) => {
  test.skip(realBackend, "Gerçek modda agent yok; akış mock modda doğrulanır.");
  await login(page);
  await openRocky01(page);
  await expect(page.getByText(/Dağıtım işlemleri/)).toBeVisible();

  // Geçersiz bölge: istemci allowlist reddi, istek gitmez.
  await page.getByPlaceholder("public").fill("bad;zone");
  await page.getByRole("button", { name: "Kural önizlemesi (çalıştırmaz)" }).click();
  await expect(page.getByText(/Bölge adı/)).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "04-fw-zone-rejected.png"), fullPage: true });

  // Geçerli bölge: önizleme görevi kuyruğa alınır (hiçbir komut çalışmaz).
  await page.getByPlaceholder("public").fill("public");
  await page.getByLabel(/Port/).fill("8443");
  await page.getByLabel(/Protokol/).selectOption("tcp");
  await page.getByRole("button", { name: "Kural önizlemesi (çalıştırmaz)" }).click();
  await expect(page.getByText(/Görev kuyruğa alındı/)).toBeVisible({ timeout: 15_000 });
  await page.screenshot({ path: path.join(shotDir, "05-fw-preview-queued.png"), fullPage: true });
});
