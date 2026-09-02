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
if [[ ! "$VERSION" =~ ^v?[0-9]+\.[0-9]+\.[0-9]+(\.[0-9]+)?([-+][0-9A-Za-z][0-9A-Za-z.+-]*)?$ ]]; then
  echo "!! FATAL: invalid version '$VERSION' (expected v1.2.3 or v1.2.3-rc1)" >&2
  exit 2
fi
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
OUT="$ROOT/dist/windows"
STAGE="$OUT/VaryaOne"

# Inno's script reader treats a continuation line whose first token is `[` as
# a new section header even inside [Code]. Catch that cheaply before the SPA and
# Go builds spend minutes producing artifacts that ISCC will later discard.
if [ "${VARYAONE_REQUIRE_INSTALLER:-0}" = "1" ]; then
  awk '
    /^\[Code\]$/ { in_code=1; next }
    in_code && /^\[[A-Za-z]/ { in_code=0 }
    in_code && /^[[:space:]]*\[/ {
      printf "!! FATAL: installer.iss:%d looks like an invalid section tag: %s\n", NR, $0 > "/dev/stderr"
      bad=1
    }
    END { exit bad }
  ' "$ROOT/deploy/windows/installer.iss"
fi

rm -rf "$OUT"
mkdir -p "$STAGE"

# Numeric x.x.x.x for VERSIONINFO resources (strip leading v and any -suffix).
_NUM="${VERSION#v}"; _NUM="${_NUM%%[-+]*}"
IFS=. read -r _a _b _c _d <<EOF
$_NUM
EOF
NUMERIC="${_a:-0}.${_b:-0}.${_c:-0}.${_d:-0}"

echo ">> building SPA bundle"
( cd "$ROOT/web" && VARYAONE_ADAPTER=static npm ci --prefer-offline --no-audit && VARYAONE_ADAPTER=static npm run build )
rm -rf "$ROOT/internal/platform/spa/dist"
cp -r "$ROOT/web/build" "$ROOT/internal/platform/spa/dist"

# Refresh embedded manifest/version (+icon for the client) resources when
# go-winres is available; committed rsrc_windows_amd64.syso files are the fallback.
if command -v go-winres >/dev/null 2>&1; then
  for d in varyaone varyaone-client; do
    ( cd "$ROOT/cmd/$d/winres" && go-winres make --in winres.json --arch amd64 \
        --file-version "$NUMERIC" --product-version "$NUMERIC" \
        --out "$ROOT/cmd/$d/rsrc" )
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

  # Integrity gate — a broken bundle must never leave CI.
  pgbin="$STAGE/pgsql/bin"
  filesize() { stat -c%s "$1" 2>/dev/null || stat -f%z "$1" 2>/dev/null || echo 0; }
  for t in initdb.exe postgres.exe pg_ctl.exe psql.exe; do
    sz=$(filesize "$pgbin/$t")
    [ "$sz" -ge 20000 ] || { echo "!! FATAL: pgsql/bin/$t missing or truncated ($sz bytes)"; exit 1; }
  done
  dlls=$(find "$pgbin" -maxdepth 1 -iname '*.dll' | wc -l)
  [ "$dlls" -ge 20 ] || { echo "!! FATAL: only $dlls DLLs in pgsql/bin"; exit 1; }
  for pat in 'libpq*' 'libcrypto*' 'libssl*'; do
    ls "$pgbin"/${pat}.dll >/dev/null 2>&1 || { echo "!! FATAL: no ${pat}.dll in pgsql/bin"; exit 1; }
  done
  for f in vcruntime140.dll vcruntime140_1.dll msvcp140.dll; do
    [ -f "$pgbin/$f" ] || { echo "!! FATAL: $f not in pgsql/bin — app-local MSVC runtime missing (run fetch-tools.ps1)"; exit 1; }
  done
  echo "   pgsql/bin OK: $dlls DLLs"
else
  echo "!! FATAL: deploy/windows/pgsql missing — run fetch-tools.ps1 first" >&2
  exit 1
fi

echo ">> zipping updater artifact"
# The Windows updater (internal/platform/desktop/updater.go) extracts this zip
# straight into the install dir, so its entries must sit at the archive root
# (varyaone.exe, RELEASE, pgsql/...) — not under a VaryaOne/ prefix.
ZIP="$OUT/varyaone-$VERSION-windows-amd64.zip"
if command -v zip >/dev/null 2>&1; then
  ( cd "$STAGE" && zip -qr "../$(basename "$ZIP")" . )
elif command -v 7z >/dev/null 2>&1; then
  ( cd "$STAGE" && 7z a -bd -tzip "$ZIP" ./* >/dev/null )
elif command -v 7z.exe >/dev/null 2>&1; then
  ( cd "$STAGE" && 7z.exe a -bd -tzip "$ZIP" ./* >/dev/null )
else
  echo "!! FATAL: zip or 7z is required to build the updater artifact" >&2
  exit 1
fi
sha256sum "$ZIP" | awk '{print $1}' > "$ZIP.sha256"

echo ">> building install wizard"
missing_prereq=0
for prereq in vc_redist.x64.exe MicrosoftEdgeWebview2Setup.exe; do
  if [ ! -f "$ROOT/deploy/windows/$prereq" ]; then
    echo "!! deploy/windows/$prereq missing — run fetch-tools.ps1 (installer prerequisite)" >&2
    missing_prereq=1
  fi
done
if command -v iscc >/dev/null 2>&1; then
  [ "$missing_prereq" -eq 0 ] || exit 1
  iscc "//DMyAppVersion=${VERSION#v}" "//DMyNumericVersion=${NUMERIC}" "$ROOT/deploy/windows/installer.iss"
  echo "   $OUT/VaryaOne-Setup-${VERSION#v}.exe"
elif [ "${VARYAONE_REQUIRE_INSTALLER:-0}" = "1" ]; then
  echo "!! FATAL: iscc (Inno Setup) not found but VARYAONE_REQUIRE_INSTALLER=1" >&2
  exit 1
else
  echo "!! iscc (Inno Setup) not found — skipped the setup .exe (zip still produced)"
fi

echo ">> done"
echo "   $ZIP"
echo "   sha256: $(cat "$ZIP.sha256")"
