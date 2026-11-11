#!/usr/bin/env bash
# Builds the Windows installer and the Mac helper binaries.
# Needs: Go 1.24+, makensis (NSIS 3), curl, python3 (with pip), xz/tar.
# Usage: installer/build.sh [VERSION]   -> dist/
set -euo pipefail

VERSION="${1:-2.0.6}"
PG_VERSION="17.11.0"
PG_JAR_SHA256="98040fae18dd9633ff95932125b0cecf0a45a1a9312e216e2a33ad03a31d4251"
VC_VERSION="14.51.36231"
VC_WHL_SHA256="89f70d1a37bb64164cdb09d02dd82d6b1400e1310be2995b4ed5a9f23efe2222"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WORK="$ROOT/installer/.work"
STAGE="$WORK/stage"
DIST="$ROOT/dist"
rm -rf "$STAGE" && mkdir -p "$WORK/dl" "$STAGE" "$DIST"

check() { echo "$2  $1" | sha256sum -c --quiet - || { echo "checksum mismatch: $1" >&2; exit 1; }; }

# 1. PostgreSQL for Windows (zonky embedded-postgres binaries, from Maven Central).
JAR="$WORK/dl/pg-$PG_VERSION.jar"
if [ ! -f "$JAR" ]; then
  curl -fsSL -o "$JAR" "https://repo1.maven.org/maven2/io/zonky/test/postgres/embedded-postgres-binaries-windows-amd64/$PG_VERSION/embedded-postgres-binaries-windows-amd64-$PG_VERSION.jar"
fi
check "$JAR" "$PG_JAR_SHA256"
rm -rf "$WORK/pg" && mkdir -p "$WORK/pg/x"
python3 -I -c 'import zipfile,sys; zipfile.ZipFile(sys.argv[1]).extract("postgres-windows-x86_64.txz", sys.argv[2])' "$JAR" "$WORK/pg"
tar -xJf "$WORK/pg/postgres-windows-x86_64.txz" -C "$WORK/pg/x"
mkdir -p "$STAGE/pgsql"
cp -r "$WORK/pg/x/bin" "$WORK/pg/x/lib" "$WORK/pg/x/share" "$STAGE/pgsql/"
# GUI and test leftovers that the server never loads.
rm -f "$STAGE"/pgsql/bin/wx*.dll "$STAGE/pgsql/bin/testplug.dll"

# 2. Microsoft Visual C++ runtime DLLs, app-local next to postgres.exe.
WHL="$WORK/dl/msvc_runtime-$VC_VERSION-cp312-cp312-win_amd64.whl"
if [ ! -f "$WHL" ]; then
  python3 -m pip download --quiet --no-deps --only-binary=:all: --platform win_amd64 \
    --python-version 312 -d "$WORK/dl" "msvc-runtime==$VC_VERSION"
fi
check "$WHL" "$VC_WHL_SHA256"
python3 -I -c '
import zipfile, sys, os
z = zipfile.ZipFile(sys.argv[1])
for n in ("vcruntime140.dll", "vcruntime140_1.dll", "msvcp140.dll"):
    open(os.path.join(sys.argv[2], n), "wb").write(z.read("msvc_runtime-'"$VC_VERSION"'.data/data/Scripts/" + n))
' "$WHL" "$STAGE/pgsql/bin"

# 3. The connector itself.
LD="-s -w -X github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/version.Version=$VERSION"
cd "$ROOT/whatsapp-bridge"
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$LD" -o "$STAGE/whatsapp-mcp.exe" ./cmd/whatsapp-mcp
CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -trimpath -ldflags "$LD" -o "$DIST/whatsapp-mcp-mac-arm64" ./cmd/whatsapp-mcp
CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build -trimpath -ldflags "$LD" -o "$DIST/whatsapp-mcp-mac-intel" ./cmd/whatsapp-mcp

# 4. Texts shown in the install folder (Windows line endings).
crlf() { sed 's/$/\r/' "$1" > "$2"; }
crlf "$ROOT/docs/GUIA.md" "$STAGE/GUIA.txt"
crlf "$ROOT/LICENSE" "$STAGE/LICENSE.txt"
crlf "$ROOT/installer/THIRD-PARTY.txt" "$STAGE/THIRD-PARTY.txt"
crlf "$ROOT/installer/stop-services.ps1" "$STAGE/stop-services.ps1"

# 5. Installer.
makensis -V2 -DVERSION="$VERSION" -DSTAGE="$STAGE" -DOUTFILE="$DIST/WhatsAppMCP-Setup-$VERSION.exe" "$ROOT/installer/whatsapp-mcp.nsi"
cd "$DIST" && sha256sum WhatsAppMCP-Setup-$VERSION.exe whatsapp-mcp-mac-arm64 whatsapp-mcp-mac-intel > SHA256SUMS.txt
cat SHA256SUMS.txt
