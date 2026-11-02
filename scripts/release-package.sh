#!/bin/bash
# Faz 15: sürüm paketleme + checksum + imza hattı (ADR-0020, SCP-SIG-V1).
# Kullanım: release-package.sh <sürüm> <dist>
# Gerekli: RELEASE_PRIV_B64 (base64 ed25519 özel anahtar) — imza için ZORUNLU.
#   Anahtar yoksa: paketler + SHA256SUMS üretilir, imza adımı dürüstçe
#   "NOT SIGNED" olur (CI test koşuları kendi geçici anahtarını üretir).
set -euo pipefail

VERSION="${1:?sürüm gerekli}"
DIST="${2:?dist gerekli}"
REPO="$(cd "$(dirname "$0")/.." && pwd)"

echo "== release-package: $VERSION"
bash "$REPO/scripts/package-linux.sh" "$VERSION" "$DIST"
cd "$DIST"

# Checksums (sha256sum)
sha256sum server-control-agent_* > SHA256SUMS
echo "-- SHA256SUMS:"
cat SHA256SUMS

# İmzalar (SCP-SIG-V1; sigtool gerekir)
if [[ -n "${RELEASE_PRIV_B64:-}" ]] && command -v sigtool >/dev/null 2>&1; then
  : > SHA256SUMS.sig
  while IFS= read -r f; do
    [[ -f "$f" ]] || continue
    sigtool sign --priv "$RELEASE_PRIV_B64" --file "$f" >> SHA256SUMS.sig
  done < <(ls server-control-agent_*)
  echo "-- imzalar: SHA256SUMS.sig ($(wc -l < SHA256SUMS.sig) satır)"
else
  echo "NOT SIGNED: RELEASE_PRIV_B64/sigtool yok — yalnız checksum üretildi"
fi
echo "== release-package tamam"
