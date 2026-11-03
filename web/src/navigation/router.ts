// UX denetimi m.4.1: URL tabanlı hash yönlendirme.
//
// Biçim: "#/<sayfa>" ve "#/nodes/<node-id>" (m.4.2 derin bağlantı).
// Boş/bilinmeyen hash → overview. react-router yerine kasıtlı olarak
// bağımsız küçük modül: yeni harici bağımlılık YOK (proje ilkesi) ve
// ayrıştırma saf fonksiyon olduğundan birim test edilebilir.
//
// Tarayıcı geçmişi (geri/ileri), yer imi ve sayfa yenileme artık
// görünümle senkrondur; state yalnız hash'ten türetilir.

export interface Route {
  page: string;
  /** Yalnız nodes sayfasında anlamlı: #/nodes/<node-id>. */
  nodeId: string | null;
}

export function parseHash(hash: string): Route {
  const raw = hash.replace(/^#\/?/, "").trim();
  if (raw === "") return { page: "overview", nodeId: null };
  const segments = raw
    .split("/")
    .filter((s) => s.length > 0)
    .map(decodeURIComponent);
  const page = segments[0];
  if (!page) return { page: "overview", nodeId: null };
  // m.4.2: ikinci segment yalnız nodes sayfasında node kimliğidir.
  const nodeId =
    page === "nodes" && segments.length >= 2 && segments[1].length > 0
      ? segments[1]
      : null;
  return { page, nodeId };
}

export function buildHash(page: string, sub?: string | null): string {
  if (sub) return `#/${page}/${encodeURIComponent(sub)}`;
  return `#/${page}`;
}
