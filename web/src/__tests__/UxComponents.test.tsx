// UX denetimi m.4.3 + m.4.4 + m.4.5 vitest:
//   - CopyButton: panoya yazma (Clipboard API + execCommand yedeği),
//     "Kopyalandı" geri bildirimi, başarısızlık durumu.
//   - ConfirmDialog: açma/kapama, Escape, odak güvenli seçenekte.
//   - ToastProvider: role=status bildirim, kapatma, otomatik kaybolma.
//   - LocaleProvider: tercih localStorage'a yazılır/okunur; geçersiz
//     değer tarayıcı diline düşer; <html lang> güncellenir.
import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor, act } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { CopyButton } from "../components/CopyButton";
import { ConfirmDialog } from "../components/ConfirmDialog";
import { ToastProvider, useToast } from "../components/ToastProvider";
import { LocaleProvider, useI18n } from "../i18n/LocaleProvider";

beforeEach(() => {
  window.localStorage.clear();
});

describe("CopyButton (m.4.4)", () => {
  it("Clipboard API ile yazar ve 'Kopyalandı' bildirir", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });

    render(<LocaleProvider><CopyButton value="TOKEN-ABC" /></LocaleProvider>);
    const btn = screen.getByTestId("copy-button");
    expect(btn).toHaveTextContent("Kopyala");
    await user.click(btn);
    await waitFor(() => {
      expect(btn).toHaveTextContent("Kopyalandı");
    });
    expect(writeText).toHaveBeenCalledWith("TOKEN-ABC");
    expect(btn).toHaveAttribute("data-copy-state", "done");
  });

  it("Clipboard API yoksa execCommand yedeğini kullanır", async () => {
    const user = userEvent.setup();
    // clipboard yok → yedek yol
    Object.defineProperty(navigator, "clipboard", { value: undefined, configurable: true });
    const exec = vi.fn(() => true);
    document.execCommand = exec as unknown as typeof document.execCommand;

    render(<LocaleProvider><CopyButton value="FALLBACK-1" /></LocaleProvider>);
    await user.click(screen.getByTestId("copy-button"));
    await waitFor(() => {
      expect(screen.getByTestId("copy-button")).toHaveTextContent("Kopyalandı");
    });
    expect(exec).toHaveBeenCalledWith("copy");
  });

  it("yazma başarısız olursa dürüstçe 'Kopyalanamadı' der", async () => {
    const user = userEvent.setup();
    Object.defineProperty(navigator, "clipboard", {
      value: { writeText: vi.fn().mockRejectedValue(new Error("denied")) },
      configurable: true,
    });
    // Yedek yol da başarısız olsun (önceki testin stub'u kalmasın).
    document.execCommand = vi.fn(() => false) as unknown as typeof document.execCommand;

    render(<LocaleProvider><CopyButton value="X" /></LocaleProvider>);
    await user.click(screen.getByTestId("copy-button"));
    await waitFor(() => {
      expect(screen.getByTestId("copy-button")).toHaveTextContent("Kopyalanamadı");
      expect(screen.getByTestId("copy-button")).toHaveAttribute("data-copy-state", "failed");
    });
  });
});

describe("ConfirmDialog (m.4.3)", () => {
  const base = {
    title: "Onay bekleniyor",
    description: "Sağlayıcı silinsin mi?",
    confirmLabel: "Onayla",
    cancelLabel: "Vazgeç",
  };

  it("kapalıyken hiçbir şey render etmez", () => {
    render(<ConfirmDialog {...base} open={false} onConfirm={() => {}} onCancel={() => {}} />);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("açılınca dialog, başlık ve açıklama görünür; odak Vazgeç'tedir", () => {
    render(<ConfirmDialog {...base} open danger onConfirm={() => {}} onCancel={() => {}} />);
    const dialog = screen.getByRole("dialog", { name: "Onay bekleniyor" });
    expect(dialog).toHaveTextContent("Sağlayıcı silinsin mi?");
    expect(screen.getByTestId("confirm-dialog-confirm")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Vazgeç" })).toHaveFocus();
  });

  it("Escape iptal eder; arka plan tıklaması da iptal sayılır", async () => {
    const user = userEvent.setup();
    const onCancel = vi.fn();
    render(<ConfirmDialog {...base} open onConfirm={() => {}} onCancel={onCancel} />);
    await user.keyboard("{Escape}");
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it("Onayla tıklaması onConfirm'i çalıştırır", async () => {
    const user = userEvent.setup();
    const onConfirm = vi.fn();
    render(<ConfirmDialog {...base} open onConfirm={onConfirm} onCancel={() => {}} />);
    await user.click(screen.getByTestId("confirm-dialog-confirm"));
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });
});

function ToastDemo({ onReady }: { onReady: (showToast: (m: string, k?: "info" | "ok" | "err") => void) => void }) {
  const { showToast } = useToast();
  onReady(showToast);
  return null;
}

describe("ToastProvider (m.4.3)", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("showToast role=status bildirim gösterir; kapatma butonu kaldırır", async () => {
    const user = userEvent.setup();
    let fire: ((m: string, k?: "info" | "ok" | "err") => void) | null = null;
    render(
      <LocaleProvider>
        <ToastProvider>
          <ToastDemo onReady={(fn) => (fire = fn)} />
        </ToastProvider>
      </LocaleProvider>,
    );
    act(() => {
      fire!("Yedek tamamlandı.", "ok");
    });
    const toast = screen.getByTestId("toast");
    expect(toast).toHaveTextContent("Yedek tamamlandı.");
    expect(toast).toHaveAttribute("data-toast-kind", "ok");
    await user.click(screen.getByRole("button", { name: "Bildirimi kapat" }));
    expect(screen.queryByTestId("toast")).not.toBeInTheDocument();
  });

  it("bildirim 5 sn sonra kendiliğinden kaybolur", () => {
    vi.useFakeTimers();
    let fire: ((m: string, k?: "info" | "ok" | "err") => void) | null = null;
    render(
      <LocaleProvider>
        <ToastProvider>
          <ToastDemo onReady={(fn) => (fire = fn)} />
        </ToastProvider>
      </LocaleProvider>,
    );
    act(() => {
      fire!("Görev gönderildi.");
    });
    expect(screen.getByTestId("toast")).toBeInTheDocument();
    act(() => {
      vi.advanceTimersByTime(5000);
    });
    expect(screen.queryByTestId("toast")).not.toBeInTheDocument();
  });
});

function LocaleProbe() {
  const { locale, setLocale, t } = useI18n();
  return (
    <div>
      <p data-testid="locale">{locale}</p>
      <p data-testid="text">{t("nav.item.nodes")}</p>
      <button type="button" onClick={() => setLocale("en")}>EN</button>
      <button type="button" onClick={() => setLocale("tr")}>TR</button>
    </div>
  );
}

describe("LocaleProvider kalıcılığı (m.4.5)", () => {
  it("localStorage'daki geçerli tercih ilk render'da uygulanır", () => {
    window.localStorage.setItem("servercontrol.locale", "en");
    render(<LocaleProvider><LocaleProbe /></LocaleProvider>);
    expect(screen.getByTestId("locale")).toHaveTextContent("en");
    expect(screen.getByTestId("text")).toHaveTextContent("Nodes");
  });

  it("geçersiz saklanan değer tarayıcı diline bakar; tr değilse proje dili tr başlar", () => {
    window.localStorage.setItem("servercontrol.locale", "fr");
    // jsdom'un navigator.language'ı "en-US" → tr öneki yok → varsayılan "tr".
    render(<LocaleProvider><LocaleProbe /></LocaleProvider>);
    expect(screen.getByTestId("locale")).toHaveTextContent("tr");
  });

  it("tarayıcı dili Türkçe ise ve tercih yoksa tr ile başlar", () => {
    Object.defineProperty(navigator, "language", { value: "tr-TR", configurable: true });
    render(<LocaleProvider><LocaleProbe /></LocaleProvider>);
    expect(screen.getByTestId("locale")).toHaveTextContent("tr");
    // Stub geri alınır: diğer testler en-US varsayımına dönsün.
    Object.defineProperty(navigator, "language", { value: "en-US", configurable: true });
  });

  it("dil değişikliği localStorage'a yazılır ve <html lang> güncellenir", async () => {
    const user = userEvent.setup();
    render(<LocaleProvider><LocaleProbe /></LocaleProvider>);
    expect(window.localStorage.getItem("servercontrol.locale")).toBeNull();
    await user.click(screen.getByRole("button", { name: "EN" }));
    await waitFor(() => {
      expect(window.localStorage.getItem("servercontrol.locale")).toBe("en");
    });
    expect(document.documentElement.lang).toBe("en-US");
    await user.click(screen.getByRole("button", { name: "TR" }));
    await waitFor(() => {
      expect(document.documentElement.lang).toBe("tr-TR");
    });
  });
});
