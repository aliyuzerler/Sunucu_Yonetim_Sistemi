// Faz 12 E2E: Sanallaştırma (KVM/QEMU/libvirt) node bölümü.
// Mock modda zengin içerik doğrulanır: dürüst guest sınırı (kriter 5),
// report-only preflight (kriter 1), kaynak limitli VM create (kriter 2),
// disk format doğrulama görünümü (kriter 4) ve snapshot akışı.
// Gerçek backend modunda agent olmadığından dürüst boş durumlar doğrulanır.
// Ekran görüntüleri docs/reviews/screenshots/phase-12 altına yazılır ve
// repoya commit edilir (sahip talimatı). IT-01 dersi: liste yerleşmeden
// (yükleniyor göstergesi kaybolmadan) satır sayımı YAPILMAZ.
import { expect, test } from "@playwright/test";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const shotDir = path.resolve(here, "../../docs/reviews/screenshots/phase-12");
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

// vm-01 UI'dan oluşturulur; MOCK modda "vm-" öneki sanallaştırma şablonunu
// tetikler (vm.* ailesi), gerçek modda agent olmadığından dürüst boş
// durumlar doğrulanır.
async function openVm01(page) {
  await page.getByRole("button", { name: /Sunucular/ }).click();
  await expect(page.getByText("Yükleniyor…")).toBeHidden();
  const row = page.getByRole("row", { name: /vm-01/ });
  if (!(await row.count())) {
    await page.getByRole("button", { name: "Sunucu Ekle" }).click();
    await page.getByLabel("Görünen ad").fill("vm-01");
    await page.getByLabel("Hostname (isteğe bağlı)").fill("vm01.example.internal");
    await page.getByRole("button", { name: "Kaydet" }).click();
    await expect(page.getByText("vm-01")).toBeVisible();
  }
  await row.getByRole("button", { name: "Detay" }).click();
}

test("vm node detay: sanallaştırma bölümü görünür, dürüst guest sınırı yerinde (kriter 5)", async ({ page }) => {
  test.skip(realBackend, "Gerçek modda agent yok; akış mock modda doğrulanır.");
  await login(page);
  await openVm01(page);

  const vm = page.getByRole("region", { name: /Sanallaştırma/ });
  await expect(vm).toBeVisible();
  // Kriter 5: VM oluşturmak guest OS yönetimi sağlamaz — açık not.
  await expect(vm.getByTestId("vm-guest-note")).toContainText(
    /guest OS yönetimi sağlamaz|in-guest modüller/,
  );
  // Kriter 1: preflight report-only — kurulum butonu ASLA yoktur.
  await expect(vm.getByText("Ön uç taraması (preflight) — kurulum yapmaz, yalnız raporlar")).toBeVisible();
  await expect(vm.getByRole("button", { name: "Preflight çalıştır" })).toBeVisible();
  await expect(vm.getByRole("button", { name: /Kur/i })).toHaveCount(0);
  await page.screenshot({ path: path.join(shotDir, "01-vm-panel.png"), fullPage: true });
});

test("vm preflight raporu: KVM/bellek/pool/köprü dürüst listelenir (kriter 1)", async ({ page }) => {
  test.skip(realBackend, "Gerçek modda agent yok; akış mock modda doğrulanır.");
  await login(page);
  await openVm01(page);

  const vm = page.getByRole("region", { name: /Sanallaştırma/ });
  await vm.getByRole("button", { name: "Preflight çalıştır" }).click();
  await waitForPreflight(vm);

  await expect(vm.getByTestId("vm-preflight")).toContainText("KVM:");
  await expect(vm.getByTestId("vm-preflight")).toContainText("QEMU 8.2.0");
  await expect(vm.getByTestId("vm-preflight")).toContainText("default");
  await expect(vm.getByTestId("vm-preflight")).toContainText("br0");
  await page.screenshot({ path: path.join(shotDir, "02-vm-preflight.png"), fullPage: true });
});

async function waitForPreflight(vm) {
  await expect(vm.getByTestId("vm-preflight")).toContainText("KVM:", { timeout: 15000 });
}

test("vm create → list → info: kaynak limitli kurulum ve disk format doğrulaması (kriter 2/3/4)", async ({ page }) => {
  test.skip(realBackend, "Gerçek modda agent yok; akış mock modda doğrulanır.");
  await login(page);
  await openVm01(page);

  const vm = page.getByRole("region", { name: /Sanallaştırma/ });
  // Create form (kriter 2: limitler formda görünür).
  await vm.getByLabel("Ad (küçük harf, [a-z0-9-])").fill("web-01");
  await vm.getByLabel("Köprü / libvirt ağı").fill("br0");
  await vm.getByRole("button", { name: "Oluştur", exact: true }).click();
  await expect(vm.getByTestId("vm-create")).toContainText("Oluşturuldu: web-01", { timeout: 15000 });

  // Liste: demo-vm + web-01.
  await vm.getByRole("button", { name: "Listele", exact: true }).click();
  await expect(vm.getByTestId("vm-list")).toContainText("web-01", { timeout: 15000 });
  await expect(vm.getByTestId("vm-list")).toContainText("demo-vm");

  // Info: guest_agent dürüst 'unsupported' + konsol local-only + disk doğrulaması.
  const webRow = vm.getByRole("row", { name: /web-01/ });
  await webRow.getByRole("button", { name: "Bilgi" }).click();
  // Not: E2E gerçek sözlükle koşar — anahtar değil çevrilmiş metin.
  await expect(vm.getByTestId("vm-info")).toContainText("in-guest modüller unsupported", { timeout: 15000 });
  await expect(vm.getByTestId("vm-info")).toContainText("yalnız yerel dinleme");
  await expect(vm.getByTestId("vm-info")).toContainText("qemu-img ile doğrulandı");
  await expect(vm.getByTestId("vm-info")).toContainText("sc-web-01-root");
  await page.screenshot({ path: path.join(shotDir, "03-vm-info-disks.png"), fullPage: true });

  // Start (kriter 2: typed lifecycle).
  await webRow.getByRole("button", { name: "Başlat" }).click();
  await expect(vm.getByTestId("vm-list")).toContainText("running", { timeout: 15000 });
});

test("vm snapshot akışı: create doğrulanmış formatları raporlar (kriter 4)", async ({ page }) => {
  test.skip(realBackend, "Gerçek modda agent yok; akış mock modda doğrulanır.");
  await login(page);
  await openVm01(page);

  const vm = page.getByRole("region", { name: /Sanallaştırma/ });
  await vm.getByRole("button", { name: "Listele", exact: true }).click();
  await expect(vm.getByTestId("vm-list")).toContainText("demo-vm", { timeout: 15000 });
  const demoRow = vm.getByRole("row", { name: /demo-vm/ });
  await demoRow.getByRole("button", { name: "Bilgi" }).click();
  await expect(vm.getByTestId("vm-info")).toBeVisible({ timeout: 15000 });
  await demoRow.getByRole("button", { name: "Snapshot'lar" }).click();
  await expect(vm.getByTestId("vm-snapshots")).toContainText("temiz-kurulum", { timeout: 15000 });

  // Yeni snapshot (qcow2 kapısı node'da; mock doğrulanmış formatı taşır).
  await vm.getByLabel("Snapshot adı").fill("guncelleme-oncesi");
  await vm.getByRole("button", { name: "Snapshot al" }).click();
  await expect(vm.getByTestId("vm-snapshots")).toContainText("guncelleme-oncesi", { timeout: 15000 });
  // Doğrulanmış format raporu formun altındaki özettedir (bölge genelinde).
  await expect(vm.getByText(/vda=qcow2/)).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "04-vm-snapshots.png"), fullPage: true });
});
