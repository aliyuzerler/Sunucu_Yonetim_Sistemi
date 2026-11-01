// Faz 03 E2E: Agent kaydı — token üretimi (tek gösterim) ve listeleme.
import { test, expect } from "@playwright/test";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const shotDir = path.resolve(here, "../../docs/reviews/screenshots/phase-03");

const ADMIN = "admin";
const PASSWORD = "guclu-parola-12345";

async function login(page) {
  await page.goto("/");
  await expect(page.getByLabel("Kullanıcı adı")).toBeVisible();
  await page.getByLabel("Kullanıcı adı").fill(ADMIN);
  await page.getByLabel("Parola").fill(PASSWORD);
  await page.getByRole("button", { name: "Giriş yap" }).click();
  await expect(page.getByRole("navigation", { name: "Ana gezinme" })).toBeVisible();
}

test("agent kayıt tokenı üretimi tek gösterimle", async ({ page }) => {
  await login(page);

  // Önce bir grup oluştur (Nodes sayfası üzerinden, gerçek kullanıcı akışı).
  await page.getByRole("button", { name: /Sunucular/ }).click();
  await page.getByRole("button", { name: "Sunucu Ekle" }).click();
  await page.getByPlaceholder("yeni-grup-adi").fill("prod-tr");
  await page.getByRole("button", { name: "Grup Oluştur" }).click();
  await expect(page.getByText("prod-tr").first()).toBeVisible();

  await page.getByRole("button", { name: /Agent Kaydı/ }).click();
  await expect(page.getByText("Kayıt tokenı yok")).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "09-enrollment-empty.png"), fullPage: true });

  await page.getByLabel("Kapsam").selectOption("group");
  await page.getByLabel("Hedef grup").selectOption({ label: "prod-tr" });
  await page.getByRole("button", { name: "Token Üret" }).click();

  // Tek gösterim paneli.
  await expect(page.getByText("Token bir kez gösterilir")).toBeVisible();
  await expect(page.getByText(/e2e-.*-mock-token/)).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "10-enrollment-reveal.png"), fullPage: true });

  await page.getByRole("button", { name: "Anladım, gizle" }).click();
  await expect(page.getByText("Bekliyor").first()).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "11-enrollment-list.png"), fullPage: true });

  // Token bir daha görüntülenemez; listede durum Bekliyor.
  await expect(page.getByText("Tek kullanımlıktır")).toHaveCount(0);
});
