// Faz 07 E2E: Docker node telemetrisi — Engine bölümü (list/lifecycle/log),
// remove önizlemesi (çalıştırmaz — ADR-0011) ve compose policy analizi
// (görev sonucu poll edilir; bulgular UI'da gösterilir). Mock modda zengin
// içerik doğrulanır; gerçek backend modunda agent olmadığından dürüst boş
// durumlar doğrulanır. Ekran görüntüleri docs/reviews/screenshots/phase-07
// altına yazılır ve repoya commit edilir (sahip talimatı).
import { expect, test } from "@playwright/test";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const shotDir = path.resolve(here, "../../docs/reviews/screenshots/phase-07");
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

// docker-01 UI'dan oluşturulur; MOCK modda "docker-" öneki Docker telemetri
// şablonunu tetikler (Engine ailesi kabiliyetleri), gerçek modda agent
// olmadığından dürüst boş durumlar doğrulanır. IT-01 dersi: liste
// yerleşmeden (yükleniyor göstergesi kaybolmadan) satır sayımı YAPILMAZ.
async function openDocker01(page) {
  await page.getByRole("button", { name: /Sunucular/ }).click();
  await expect(page.getByText("Yükleniyor…")).toBeHidden();
  const row = page.getByRole("row", { name: /docker-01/ });
  if (!(await row.count())) {
    await page.getByRole("button", { name: "Sunucu Ekle" }).click();
    await page.getByLabel("Görünen ad").fill("docker-01");
    await page.getByLabel("Hostname (isteğe bağlı)").fill("docker01.example.internal");
    await page.getByRole("button", { name: "Kaydet" }).click();
    await expect(page.getByText("docker-01")).toBeVisible();
  }
  await row.getByRole("button", { name: "Detay" }).click();
}

test("docker node detay: Engine bölümü ve önizleme etiketleri görünür", async ({ page }) => {
  test.skip(realBackend, "Gerçek modda agent yok; akış mock modda doğrulanır.");
  await login(page);
  await openDocker01(page);

  const docker = page.getByRole("region", { name: /Docker/ });
  await expect(docker).toBeVisible();
  await expect(docker.getByRole("button", { name: "Containerları listele" })).toBeVisible();
  await expect(docker.getByRole("button", { name: "Imageleri listele" })).toBeVisible();
  await expect(docker.getByRole("button", { name: /Silme önizlemesi/ })).toBeVisible();
  await expect(docker.getByText(/hiçbir şey silinmez/)).toBeVisible();
  await expect(docker.getByRole("button", { name: /Deploy planı/ })).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "01-docker-node.png"), fullPage: true });
});

test("container yaşam döngüsü: geçersiz ref reddedilir, geçerli görev kuyruğa girer", async ({ page }) => {
  test.skip(realBackend, "Gerçek modda agent yok; akış mock modda doğrulanır.");
  await login(page);
  await openDocker01(page);

  const docker = page.getByRole("region", { name: /Docker/ });
  // Geçersiz ref: istemci allowlist reddi, istek gitmez.
  await docker.getByLabel(/Container kimliği\/adı/).fill("-flag");
  await docker.getByRole("button", { name: "Başlat", exact: true }).click();
  await expect(docker.getByText("Geçersiz kaynak referansı")).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "02-docker-ref-rejected.png"), fullPage: true });

  // Geçerli ref: görev kuyruğa alınır.
  await docker.getByLabel(/Container kimliği\/adı/).fill("web-01");
  await docker.getByRole("button", { name: "Yeniden başlat" }).click();
  await expect(page.getByText(/Görev kuyruğa alındı/)).toBeVisible({ timeout: 15_000 });
  await page.screenshot({ path: path.join(shotDir, "03-docker-task-queued.png"), fullPage: true });
});

test("compose analizi: bulgular ve deploy planı sonuç olarak gösterilir (yürütmez)", async ({ page }) => {
  test.skip(realBackend, "Gerçek modda agent yok; akış mock modda doğrulanır.");
  await login(page);
  await openDocker01(page);

  const docker = page.getByRole("region", { name: /Docker/ });
  await docker.getByLabel(/Compose dosyası/).fill(
    "services:\n  web:\n    image: nginx:1.25\n    ports:\n      - \"8080:80\"\n  worker:\n    image: worker:2\n    privileged: true\n",
  );
  await docker.getByRole("button", { name: "Şema/policy doğrula" }).click();
  // Görev sonucu poll edilir ve bulgular UI'da gösterilir (kriter 4).
  const report = docker.getByTestId("compose-report");
  await expect(report).toBeVisible({ timeout: 15_000 });
  await expect(report.getByText(/Şema geçerli \(2 servis\)/)).toBeVisible();
  await expect(report.getByText(/privileged/)).toBeVisible();
  await expect(report.getByText(/public-port/)).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "04-compose-findings.png"), fullPage: true });

  // Deploy planı: aksiyonlar metin olarak listelenir; yürütülmez (ADR-0011).
  await docker.getByRole("button", { name: /Deploy planı/ }).click();
  await expect(report.getByText(/container oluştur: web/)).toBeVisible({ timeout: 15_000 });
  await expect(report.getByText(/hiçbir kaynak oluşturulmadı/)).toBeVisible();
  await page.screenshot({ path: path.join(shotDir, "05-compose-plan.png"), fullPage: true });
});
