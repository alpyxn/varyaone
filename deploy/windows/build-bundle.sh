#!/usr/bin/env bash
# Assemble the Windows desktop bundle: one Go binary + embedded SPA + bundled
# PostgreSQL. Produces both the updater artifact (zip) and, when Inno Setup's
# `iscc` is on PATH, the double-click install wizard (setup .exe).
#
#   deploy/windows/build-bundle.sh <version>
#
# Output:
#   dist/windows/VaryaOne/                                staged install tree
#   dist/windows/varyaone-<version>-windows-amd64.zip     updater artifact (files at zip root)
#   dist/windows/varyaone-<version>-windows-amd64.zip.sha256
#   dist/windows/VaryaOne-Setup-<version>.exe             install wizard (needs iscc)
#
# PostgreSQL binaries are expected under deploy/windows/pgsql/ (see fetch-tools.ps1).
set -euo pipefail

VERSION="${1:?usage: build-bundle.sh <version>}"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
OUT="$ROOT/dist/windows"
STAGE="$OUT/VaryaOne"

rm -rf "$OUT"
mkdir -p "$STAGE"

# Numeric x.x.x.x for VERSIONINFO resources (strip leading v and any -suffix).
_NUM="${VERSION#v}"; _NUM="${_NUM%%-*}"
IFS=. read -r _a _b _c _d <<EOF
$_NUM
EOF
NUMERIC="${_a:-0}.${_b:-0}.${_c:-0}.${_d:-0}"

echo ">> building SPA bundle"
( cd "$ROOT/web" && VARYAONE_ADAPTER=static npm ci && VARYAONE_ADAPTER=static npm run build )
rm -rf "$ROOT/internal/platform/spa/dist"
cp -r "$ROOT/web/build" "$ROOT/internal/platform/spa/dist"

# Refresh embedded manifest/version (+icon for the client) resources when
# go-winres is available; committed rsrc_windows_amd64.syso files are the fallback.
if command -v go-winres >/dev/null 2>&1; then
  for d in varyaone varyaone-client; do
    ( cd "$ROOT/cmd/$d/winres" && go-winres make --in winres.json --arch amd64 \
        --file-version "$NUMERIC" --product-version "$NUMERIC" \
        --out "$ROOT/cmd/$d/rsrc" ) || true
  done
fi

echo ">> building varyaone.exe (windows/amd64)"
( cd "$ROOT" && GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
    go build -buildvcs=false -ldflags "-s -w -X main.version=$VERSION" \
    -o "$STAGE/varyaone.exe" ./cmd/varyaone )

echo "$VERSION" > "$STAGE/RELEASE"

echo ">> building varyaone-client.exe (windows/amd64)"
( cd "$ROOT" && GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
    go build -buildvcs=false -ldflags "-s -w -H windowsgui -X main.version=$VERSION" \
    -o "$STAGE/varyaone-client.exe" ./cmd/varyaone-client )

if [ -d "$ROOT/deploy/windows/pgsql" ]; then
  echo ">> bundling PostgreSQL"
  cp -r "$ROOT/deploy/windows/pgsql" "$STAGE/pgsql"
else
  echo "!! deploy/windows/pgsql missing — run fetch-tools.ps1 first (installer will be incomplete)"
fi

echo ">> zipping updater artifact"
# The Windows updater (internal/platform/desktop/updater.go) extracts this zip
# straight into the install dir, so its entries must sit at the archive root
# (varyaone.exe, RELEASE, pgsql/...) — not under a VaryaOne/ prefix.
ZIP="$OUT/varyaone-$VERSION-windows-amd64.zip"
( cd "$STAGE" && zip -qr "../$(basename "$ZIP")" . )
sha256sum "$ZIP" | awk '{print $1}' > "$ZIP.sha256"

echo ">> building install wizard"
for prereq in vc_redist.x64.exe MicrosoftEdgeWebview2Setup.exe; do
  [ -f "$ROOT/deploy/windows/$prereq" ] || \
    echo "!! deploy/windows/$prereq missing — run fetch-tools.ps1 (installer prerequisite)"
done
if command -v iscc >/dev/null 2>&1; then
  iscc "//DMyAppVersion=${VERSION#v}" "//DMyNumericVersion=${NUMERIC}" "$ROOT/deploy/windows/installer.iss"
  echo "   $OUT/VaryaOne-Setup-${VERSION#v}.exe"
else
  echo "!! iscc (Inno Setup) not found — skipped the setup .exe (zip still produced)"
fi

echo ">> done"
echo "   $ZIP"
echo "   sha256: $(cat "$ZIP.sha256")"
