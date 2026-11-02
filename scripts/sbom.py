#!/usr/bin/env python3
"""Faz 15 SBOM üreticisi (bağımlılık politikası madde 3 — ADR-0020 destek).

Go modülleri (backend/control-plane + agent go.mod/go.sum) ve npm kilit
dosyası (web/package-lock.json) taranarak CycloneDX 1.5 uyumlu bir JSON SBOM
üretir. Lisans bilgisi, depodaki kayıtlı kataloğa (docs/technology/
dependency-registry.md) ve npm paketleri için package-lock "license"
alanlarına dayanır; kayıtsızysa "NOASSERTION" yazılır (dürüst bilinmezlik).

Çıktı: stdout — `make sbom` ya da CI release-audit job'ı kullanır.
"""
import json
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def parse_go_mod(path: Path):
    """go.mod'dan module satırı + require bloklarını çek."""
    requires = []
    module_name = None
    in_require = False
    for raw in path.read_text().splitlines():
        line = raw.split("//")[0].strip()
        if line.startswith("module "):
            module_name = line.split()[1]
        if line.startswith("require ("):
            in_require = True
            continue
        if in_require and line.startswith(")"):
            in_require = False
            continue
        if in_require:
            parts = line.split()
            if len(parts) >= 2:
                requires.append((parts[0], parts[1]))
        elif line.startswith("require "):
            parts = line.split()[1:]
            if len(parts) >= 2:
                requires.append((parts[0], parts[1]))
    return module_name, requires


def go_components():
    comps = []
    go_mods = [
        REPO / "backend/control-plane/go.mod",
        REPO / "agent/go.mod",
        REPO / "gen/proto/go.mod",
    ]
    for gm in go_mods:
        if not gm.exists():
            continue
        module, requires = parse_go_mod(gm)
        comps.append({
            "type": "application",
            "name": module or gm.parent.name,
            "version": "unknown",
            "purl": f"pkg:golang/{module}" if module else None,
            "properties": [{"name": "server-control:path", "value": str(gm.parent.relative_to(REPO))}],
        })
        for name, ver in requires:
            # License: dependency-registry kayıtlı katalog (Go tarafı tek
            # harici bağımlılık ailesi: golang.org/x/* — BSD-3-Clause).
            license_id = "NOASSERTION"
            if name.startswith("golang.org/x/"):
                license_id = "BSD-3-Clause"
            comps.append({
                "type": "library",
                "name": name,
                "version": ver,
                "purl": f"pkg:golang/{name}@{ver}",
                "licenses": [{"license": {"id": license_id}}],
            })
    return comps


def npm_components():
    lock = REPO / "web/package-lock.json"
    if not lock.exists():
        return []
    data = json.loads(lock.read_text())
    comps = []
    for name, info in sorted((data.get("packages") or {}).items()):
        if not name:  # root
            continue
        version = info.get("version", "unknown")
        lic = info.get("license", "NOASSERTION")
        if isinstance(lic, list):
            lic = ",".join(str(x) for x in lic) or "NOASSERTION"
        comps.append({
            "type": "library",
            "name": name,
            "version": version,
            "purl": f"pkg:npm/{name}@{version}",
            "scope": "dev" if info.get("dev") else "runtime",
            "licenses": [{"license": {"id": lic if lic else "NOASSERTION"}}],
        })
    return comps


def main():
    components = go_components() + npm_components()
    bom = {
        "bomFormat": "CycloneDX",
        "specVersion": "1.5",
        "version": 1,
        "metadata": {
            "component": {
                "type": "application",
                "name": "server-control-zcode",
                "version": "0.16.0-rc1",
            },
            "properties": [
                {"name": "server-control:generator", "value": "scripts/sbom.py (stdlib only)"},
                {"name": "server-control:policy", "value": "docs/technology/dependency-policy.md"},
            ],
        },
        "components": components,
    }
    json.dump(bom, sys.stdout, indent=2)
    sys.stdout.write("\n")


if __name__ == "__main__":
    main()
