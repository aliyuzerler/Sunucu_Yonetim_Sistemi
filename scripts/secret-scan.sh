#!/usr/bin/env bash
# Basit, yüksek sinyalli secret taraması (CI adımı).
# Kapsamlı tarama (gitleaks/osv-scanner) bağımlılık politikasıyla sonraki
# fazlarda değerlendirilir; bu betik en kritik desenleri yakalar.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

PATTERNS=(
  'ghp_[A-Za-z0-9]{20,}'
  'github_pat_[A-Za-z0-9_]{20,}'
  'AKIA[0-9A-Z]{16}'
  'xox[baprs]-[A-Za-z0-9-]{10,}'
  '-----BEGIN (RSA |EC |OPENSSH |PGP |DSA )?PRIVATE KEY-----'
  'glpat-[A-Za-z0-9_-]{20,}'
  'sk-(ant-)?[A-Za-z0-9]{32,}'
)

# .env.example placeholder'ları ve bu betiğin kendisi hariç tutulur.
EXCLUDES=(
  ':(exclude).env.example'
  ':(exclude)scripts/secret-scan.sh'
  ':(exclude)deploy/.env.example'
)

FAILED=0
for pat in "${PATTERNS[@]}"; do
  if git grep -qE "$pat" -- . "${EXCLUDES[@]}" 2>/dev/null; then
    echo "SECRET BULUNDU (desen: $pat):"
    git grep -nE "$pat" -- . "${EXCLUDES[@]}" | head -5
    FAILED=1
  fi
done

if [ "$FAILED" -eq 0 ]; then
  echo "secret-scan: temiz ✓"
else
  echo "secret-scan: başarısız — yukarıdaki eşleşmeleri düzeltin." >&2
  exit 1
fi
