// Faz 13 E2E: İzleme (metrik + alarm yaşam döngüsü), log arama (redaksiyon
// görünür) ve güvenlik merkezi (dürüst durumlar — kriter 5).
// Mock modda zengin içerik doğrulanır: freshness rozetleri (kriter 1),
// alert dedupe/ack/silence/resolve durum makinesi (kriter 4), logs.search
// görevi ve node'da redakte edilmiş mesaj (kriter 3), unknown asla ok
// görünmez (kriter 5). Gerçek backend modunda agent olmadığından (ve task
// akışı node'a bağlı olduğundan) mock modda koşar — vm.spec kalıbı.
// Ekran görüntüleri docs/reviews/screenshots/phase-13 altına yazılır ve
// repoya commit edilir. IT-01 dersi: liste yerleşmeden işlem YAPILMAZ.
import { expect, test } from "@playwright/test";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const shotDir = path.resolve(here, "../../docs/reviews/screenshots/phase-13");
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

// mon-01 UI'dan oluşturulur; MOCK modda varsayılan telemetri şablonu TAZE
// metrics/latest döner (freshness kriterinin pozitif yolu).
async function openMon01(page) {
  await page.getByRole("button", { name: /Sunucular/ }).click();
  await expect(page.getByText("Yükleniyor…")).toBeHidden();
  const row = page.getByRole("row", { name: /mon-01/ });
  if (!(await row.count())) {
    await page.getByRole("button", { name: "Sunucu Ekle" }).click();
    await page.getByLabel("Görünen ad").fill("mon-01");
    await page.getByRole("button", { name: "Kaydet" }).click();
    await expect(page.getByText("mon-01")).toBeVisible();
  }
}

test("izleme sayfası: freshness rozetleri ve telemetri özeti görünür (kriter 1)", async ({ page }) => {
  test.skip(realBackend, "Gerçek modda agent yok; akış mock modda doğrulanır.");
  await login(page);
  await openMon01(page);

  await page.getByRole("button", { name: "İzleme" }).click();
  const fresh = page.getByRole("region", { name: /Düğüm telemetri güncelliği/ });
  await expect(fresh).toBeVisible();
  // Kriter 1: timestamp/freshness — taze snapshot "dk önce" + kaynak CPU/Bellek.
  // Tam süitte başka spec'lerin node'ları da listededir — satır mon-01'e daraltılır.
  const monRow = fresh.getByRole("listitem").filter({ hasText: "mon-01" });
  await expect(monRow).toBeVisible();
  await expect(monRow.getByText(/dk önce/)).toBeVisible();
  await expect(monRow.getByText(/CPU \d+% · Bellek \d+%/)).toBeVisible();
  // Dürüstlük notu: veri olmayan düğüm asla güncel gösterilmez.
  await expect(page.getByText(/asla güncel gösterilmez/)).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "01-monitoring-freshness.png"), fullPage: true });
});

test("alarm yaşam döngüsü: acknowledge → silence → resolve durum makinesi (kriter 4)", async ({ page }) => {
  test.skip(realBackend, "Gerçek modda agent yok; akış mock modda doğrulanır.");
  await login(page);
  await openMon01(page);

  await page.getByRole("button", { name: "İzleme" }).click();
  const alerts = page.getByRole("region", { name: /^Alarmlar/ });
  await expect(alerts.getByText("CPU yüksek (düğüm cpu)")).toBeVisible({ timeout: 15000 });
  await expect(alerts.getByText("Açık")).toBeVisible();
  await expect(alerts.getByText(/Tekrar: 3/)).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "02-alert-open.png"), fullPage: true });

  // open → acknowledged.
  await alerts.getByRole("button", { name: "Onayla" }).click();
  await expect(page.getByText("İşlem tamamlandı.")).toBeVisible();
  await expect(alerts.getByText("Onaylandı")).toBeVisible({ timeout: 15000 });

  // acknowledged → silenced.
  await alerts.getByRole("button", { name: "1 saat sessize al" }).click();
  await expect(alerts.getByText("Sessizde")).toBeVisible({ timeout: 15000 });

  // silenced → resolved (aktif listeden düşer).
  await alerts.getByRole("button", { name: "Çözüldü işaretle" }).click();
  await expect(alerts.getByText("Aktif alarm yok.")).toBeVisible({ timeout: 15000 });
});

test("alarm kuralı: kapalı şemalı form kural oluşturur ve listede görünür", async ({ page }) => {
  test.skip(realBackend, "Gerçek modda agent yok; akış mock modda doğrulanır.");
  await login(page);
  await openMon01(page);

  await page.getByRole("button", { name: "İzleme" }).click();
  const rules = page.getByRole("region", { name: /Alarm kuralları/ });
  await expect(rules).toBeVisible();

  await page.getByLabel("Kural adı").fill("cpu-yuksek");
  // Kapsam "Tüm düğümler" kalır; metrik/koşul/eşik kapalı enum'dan.
  await page.getByLabel("Metrik").selectOption("cpu");
  await page.getByLabel("Koşul").selectOption("gt");
  await page.getByLabel("Eşik").fill("90");
  await page.getByRole("button", { name: "Kural oluştur" }).click();
  await expect(rules.getByText("cpu-yuksek")).toBeVisible({ timeout: 15000 });
  await expect(rules.getByText("Aktif")).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "03-alert-rules.png"), fullPage: true });
});

test("log arama: görev sonuçları tabloda, mesaj node'da redakte edilmiş (kriter 3)", async ({ page }) => {
  test.skip(realBackend, "Gerçek modda agent yok; akış mock modda doğrulanır.");
  await login(page);
  await openMon01(page);

  await page.getByRole("button", { name: "Loglar" }).click();
  // Liste yerleşmeden select YAPILMAZ (IT-01 dersi).
  await page.getByLabel("Düğüm").selectOption({ label: "mon-01" });
  await page.getByLabel("Birim (örn. nginx)").fill("nginx");
  await page.getByRole("button", { name: "Ara" }).click();

  await expect(page.getByText(/kayıt bulundu/)).toBeVisible({ timeout: 20000 });
  // Redaksiyon kanıtı: gerçek parola DEĞİL, [REDACTED] etiketi görünür.
  await expect(page.getByText(/password=\[REDACTED\]/)).toBeVisible();
  // Kaynak rozeti (başlık satırındaki span) — select option'ıyla karışmasın.
  await expect(page.getByText("journald", { exact: true })).toBeVisible();
  await expect(page.getByText("err", { exact: true })).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "04-logs-redacted.png"), fullPage: true });
});

test("güvenlik merkezi: bilinmeyen kanıt asla Tamam görünmez (kriter 5)", async ({ page }) => {
  test.skip(realBackend, "Gerçek modda agent yok; akış mock modda doğrulanır.");
  await login(page);

  await page.getByRole("button", { name: "Güvenlik" }).click();
  const controls = page.getByRole("region", { name: /Kontrol durumu/ });
  await expect(controls).toBeVisible();
  // Dürüstlük notu görünür (sözleşme: kanıt yok → Bilinmiyor, asla güvenli sayılmaz).
  await expect(page.getByText(/asla güvenli\/temiz sayılmaz/)).toBeVisible();
  // Dört dürüst durum da mevcut — unknown birinci sınıf durum. Mock raporu:
  // 4×Tamam (ok), 1×Uyarı (warn), 1×Alarm (alert), 2×Bilinmiyor (unknown).
  await expect(controls.getByText("Tamam", { exact: true })).toHaveCount(4);
  await expect(controls.getByText("Uyarı", { exact: true })).toHaveCount(1);
  await expect(controls.getByText("Alarm", { exact: true })).toHaveCount(1);
  const unknownBadge = controls.getByText("Bilinmiyor", { exact: true });
  await expect(unknownBadge).toHaveCount(2);
  // "unknown" kanıtı olan kontroller bilinmiyor rozetiyle durur, ok'a çekilmez
  // (iki unknown kanıt: node telemetri yok + denetim kaydı yok).
  await expect(controls.getByText(/kanıt yok/)).toHaveCount(2);
  await page.screenshot({ path: path.join(shotDir, "05-security-honest.png"), fullPage: true });
});
