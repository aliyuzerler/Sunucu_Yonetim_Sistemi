#!/bin/bash
# Faz 15: Linux (deb + rpm) ve Windows (zip) paketleme hattı (ADR-0020).
# Kullanım: package-linux.sh <sürüm> <dist-dir> <arch=dflt amd64>
#
# Çıktılar (dist altında):
#   dist/server-control-agent_<v>_amd64.deb        — server-agent + privd + systemd unit'leri
#   dist/server-control-agent_<v>_amd64.rpm        — aynı içerik rpm
#   dist/server-control-agent_<v>_windows_amd64.zip— server-agent.exe + broker.exe + README
# Sürüm parametresi git tag'inden (v0.16.0-rc1 → 0.16.0-rc1) ya da açık verilir.
set -euo pipefail

VERSION="${1:?sürüm gerekli (örn. 0.16.0-rc1)}"
DIST="${2:?dist dizini gerekli}"
ARCH="${3:-amd64}"
REPO="$(cd "$(dirname "$0")/.." && pwd)"
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT

echo "== Faz 15 paketleme: sürüm=$VERSION dist=$DIST arch=$ARCH"

# ---- Derleme (yalnız go, -buildvcs=false — CI-05 dersi) ----
GO="$(command -v go)"
echo "-- agent derleniyor (linux/$ARCH)"
cd "$REPO/agent"
CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" "$GO" build -trimpath -buildvcs=false \
  -ldflags "-s -w -X main.version=$VERSION" \
  -o "$STAGE/server-agent" ./cmd/server-agent
CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" "$GO" build -trimpath -buildvcs=false \
  -ldflags "-s -w -X main.version=$VERSION" \
  -o "$STAGE/server-privd" ./cmd/server-privd
echo "-- agent derleniyor (windows/$ARCH)"
CGO_ENABLED=0 GOOS=windows GOARCH="$ARCH" "$GO" build -trimpath -buildvcs=false \
  -ldflags "-s -w -X main.version=$VERSION" \
  -o "$STAGE/server-agent.exe" ./cmd/server-agent
CGO_ENABLED=0 GOOS=windows GOARCH="$ARCH" "$GO" build -trimpath -buildvcs=false \
  -ldflags "-s -w -X main.version=$VERSION" \
  -o "$STAGE/server-privd.exe" ./cmd/server-privd

mkdir -p "$DIST"

# ---- .deb (dpkg-deb) ----
DEBROOT="$STAGE/deb/server-control-agent_${VERSION}_${ARCH}"
mkdir -p "$DEBROOT/DEBIAN" \
  "$DEBROOT/usr/local/bin" \
  "$DEBROOT/lib/systemd/system" \
  "$DEBROOT/etc/server-control" \
  "$DEBROOT/var/lib/server-control/updates/incoming" \
  "$DEBROOT/var/lib/server-control/updates/staged" \
  "$DEBROOT/var/lib/server-control/updates/rollback"
install -m 0755 "$STAGE/server-agent" "$DEBROOT/usr/local/bin/server-agent"
install -m 0755 "$STAGE/server-privd" "$DEBROOT/usr/local/bin/server-privd"
install -m 0644 "$REPO/packaging/agent/systemd/server-control-agent.service" "$DEBROOT/lib/systemd/system/"
install -m 0644 "$REPO/packaging/agent/systemd/server-control-broker.service" "$DEBROOT/lib/systemd/system/"
cat > "$DEBROOT/DEBIAN/control" <<EOF
Package: server-control-agent
Version: $VERSION
Section: admin
Priority: optional
Architecture: $ARCH
Depends: libc6
Maintainer: Server Control Operators <ops@server-control.invalid>
Description: Server Control agent (server-agent + privileged broker)
 Typed, allowlisted node agent with an outbound-only mTLS connection
 and a highly constrained privileged broker. Update family is opt-in
 (--update-enable --update-trust-key); unsigned updates are impossible.
EOF
cat > "$DEBROOT/DEBIAN/postinst" <<'EOF'
#!/bin/sh
# Upgrade-safe: enable the unprivileged agent only; the broker is opt-in.
set -e
getent group server-control >/dev/null 2>&1 || groupadd --system server-control
getent passwd server-control >/dev/null 2>&1 || useradd --system --gid server-control \
  --home-dir /var/lib/server-control --shell /usr/sbin/nologin server-control
install -d -o server-control -g server-control -m 0750 /var/lib/server-control
install -d -m 0750 /var/lib/server-control/updates/incoming \
  /var/lib/server-control/updates/staged /var/lib/server-control/updates/rollback
systemctl daemon-reload >/dev/null 2>&1 || true
echo "server-control-agent kuruldu. Kurulum adımları: deploy/INSTALL-UPDATE.md"
EOF
chmod 0755 "$DEBROOT/DEBIAN/postinst"
cat > "$DEBROOT/DEBIAN/postrm" <<'EOF'
#!/bin/sh
# Uninstall removes binaries and units; DATA (inventory cache, update
# staging) is PRESERVED by design — documented downgrade/rollback limit.
set -e
systemctl daemon-reload >/dev/null 2>&1 || true
EOF
chmod 0755 "$DEBROOT/DEBIAN/postrm"
dpkg-deb --build --root-owner-group "$DEBROOT" "$DIST/server-control-agent_${VERSION}_${ARCH}.deb" >/dev/null
echo "deb OK: $DIST/server-control-agent_${VERSION}_${ARCH}.deb"

# ---- .rpm (rpmbuild; yoksa dürüst NOT BUILT) ----
if command -v rpmbuild >/dev/null 2>&1; then
  RPMTOP="$STAGE/rpmbuild"
  mkdir -p "$RPMTOP/BUILD" "$RPMTOP/RPMS" "$RPMTOP/SOURCES" "$RPMTOP/SPECS" "$RPMTOP/SRPMS"
  install -m 0755 "$STAGE/server-agent" "$RPMTOP/BUILD/server-agent"
  install -m 0755 "$STAGE/server-privd" "$RPMTOP/BUILD/server-privd"
  # NOT: sürümdeki '-' rpm'de '~' olur; tilde-expansion'a karşı sed ile
  # ön-hesaplanır (CI-51 dersi: heredoc içi ${var/-/~} ~'yu $HOME'a açar;
  # CI-53 dersi: 'local' yalnız fonksiyon içindedir).
  RPM_VERSION="$(printf '%s' "$VERSION" | sed 's/-/~/')"
  # rpm mimari adlandırması: amd64 → x86_64 (CI-54 dersi: BuildArch amd64
  # 'No compatible architectures found' verir).
  RPM_ARCH="x86_64"
  [[ "$ARCH" == "arm64" ]] && RPM_ARCH="aarch64"
  cat > "$RPMTOP/SPECS/server-control-agent.spec" <<EOF
Name:           server-control-agent
Version:        ${RPM_VERSION}
Release:        1%{?dist}
Summary:        Server Control agent (server-agent + privileged broker)
License:        Proprietary
BuildArch:      ${RPM_ARCH}
%description
Typed, allowlisted node agent with an outbound-only mTLS connection and a
highly constrained privileged broker (ADR-0002/0003/0020).
%install
mkdir -p %{buildroot}/usr/local/bin %{buildroot}/lib/systemd/system
install -m 0755 %{_builddir}/*server-agent %{buildroot}/usr/local/bin/server-agent
install -m 0755 %{_builddir}/*server-privd %{buildroot}/usr/local/bin/server-privd
install -m 0644 ${REPO}/packaging/agent/systemd/server-control-agent.service %{buildroot}/lib/systemd/system/
install -m 0644 ${REPO}/packaging/agent/systemd/server-control-broker.service %{buildroot}/lib/systemd/system/
%files
/usr/local/bin/server-agent
/usr/local/bin/server-privd
/lib/systemd/system/server-control-agent.service
/lib/systemd/system/server-control-broker.service
%post
getent group server-control >/dev/null 2>&1 || groupadd --system server-control
getent passwd server-control >/dev/null 2>&1 || useradd --system --gid server-control --home-dir /var/lib/server-control --shell /sbin/nologin server-control || true
install -d -o server-control -g server-control -m 0750 /var/lib/server-control 2>/dev/null || true
systemctl daemon-reload >/dev/null 2>&1 || true
%postun
systemctl daemon-reload >/dev/null 2>&1 || true
EOF
  rpmbuild --define "_topdir $RPMTOP" -bb "$RPMTOP/SPECS/server-control-agent.spec" >/dev/null
  find "$RPMTOP/RPMS" -name '*.rpm' -exec cp {} "$DIST/" \;
  echo "rpm OK"
else
  echo "rpm NOT BUILT: rpmbuild yok (CI'da apt install rpm ile kurulur)"
fi

# ---- Windows zip ----
WINZIP="$DIST/server-control-agent_${VERSION}_windows_${ARCH}.zip"
cd "$STAGE"
rm -f "$WINZIP"
zip -q "$WINZIP" server-agent.exe server-privd.exe
# README'yi zip'e ekle (aynı kökte)
"$GO" >/dev/null 2>&1 || true
cd "$REPO"
zip -q -j "$WINZIP" deploy/agent-windows/README.md
echo "zip OK: $WINZIP"

echo "== paketleme tamam"
