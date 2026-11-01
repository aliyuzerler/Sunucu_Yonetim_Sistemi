export function EmptyState({
  title,
  description,
}: {
  title: string;
  description: string;
}) {
  return (
    <div className="flex flex-col items-center justify-center gap-1.5 px-4 py-8 text-center">
      <p className="text-sm font-medium text-ink">{title}</p>
      <p className="max-w-xs text-xs leading-relaxed text-ink-soft">{description}</p>
    </div>
  );
}
