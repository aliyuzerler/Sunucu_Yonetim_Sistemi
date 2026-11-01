import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { App } from "../App";
import { LocaleProvider } from "../i18n/LocaleProvider";

function renderApp() {
  return render(
    <LocaleProvider>
      <App />
    </LocaleProvider>,
  );
}

describe("App kabuğu", () => {
  it("gezinme gruplarını ve öğelerini Türkçe render eder", () => {
    renderApp();
    expect(screen.getByRole("navigation", { name: "Ana gezinme" })).toBeInTheDocument();
    for (const group of ["Genel Bakış", "Altyapı", "Hosting", "Operasyonlar", "Ayarlar"]) {
      expect(screen.getAllByText(group).length).toBeGreaterThan(0);
    }
    expect(screen.getByRole("button", { name: /Sunucular/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Görevler ve Hareketler/ })).toBeInTheDocument();
  });

  it("dil değiştirildiğinde gezinme İngilizceye döner", async () => {
    const user = userEvent.setup();
    renderApp();
    await user.click(screen.getByRole("button", { name: "EN" }));
    expect(screen.getAllByText("Infrastructure").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Operations").length).toBeGreaterThan(0);
  });

  it("genel bakışta dürüst boş durum gösterir (sahte metrik yok)", () => {
    renderApp();
    expect(screen.getByText("Henüz kayıtlı sunucu yok")).toBeInTheDocument();
    expect(screen.getByText("İzleme modülü etkin değil")).toBeInTheDocument();
  });

  it("henüz uygulanmamış modül faz bilgisiyle açıklama gösterir", async () => {
    const user = userEvent.setup();
    renderApp();
    await user.click(screen.getByRole("button", { name: /Docker/ }));
    expect(screen.getByText("Bu modül henüz uygulanmadı")).toBeInTheDocument();
    // Ana içerikteki açıklama hem modülü hem fazı dürüstçe belirtir.
    expect(
      screen.getByText(/docker modülü yol haritasında Faz 7 planında/),
    ).toBeInTheDocument();
  });
});
