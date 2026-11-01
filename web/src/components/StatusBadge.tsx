export type BadgeVariant = "ok" | "warn" | "error" | "info" | "neutral";

const variantStyles: Record<BadgeVariant, string> = {
  ok: "bg-ok-soft text-ok border-ok/30",
  warn: "bg-warn-soft text-warn border-warn/30",
  error: "bg-err-soft text-err border-err/30",
  info: "bg-info-soft text-info border-info/30",
  neutral: "bg-neutral-soft text-ink-soft border-border",
};

// Status is never communicated by color alone: each badge pairs an icon with
// text and an aria-label (design-system.md rule).
const variantIcon: Record<BadgeVariant, string> = {
  ok: "✓",
  warn: "!",
  error: "✕",
  info: "i",
  neutral: "•",
};

const variantLabel: Record<BadgeVariant, string> = {
  ok: "başarılı",
  warn: "uyarı",
  error: "hata",
  info: "bilgi",
  neutral: "nötr",
};

export function StatusBadge({
  variant,
  children,
}: {
  variant: BadgeVariant;
  children: string;
}) {
  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-full border px-2.5 py-0.5 text-xs font-medium ${variantStyles[variant]}`}
    >
      <span
        aria-hidden="true"
        className="inline-flex h-4 w-4 items-center justify-center rounded-full bg-current/15 text-[10px] font-bold"
      >
        {variantIcon[variant]}
      </span>
      <span aria-label={`durum: ${variantLabel[variant]}`}>{children}</span>
    </span>
  );
}
