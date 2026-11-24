#!/bin/sh
set -eu
# A local toolchain; no global PATH or operating system package changes.
ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
TOOLS="$ROOT/runtime/tools"
mkdir -p "$TOOLS"
ARCHIVE="$TOOLS/go1.27.4.darwin-arm64.tar.gz"
if [ "$(uname -s)/$(uname -m)" != "Darwin/arm64" ]; then
  echo 'Install Go 1.26+ from go.dev for your platform.' >&2
  exit 1
fi
curl -fL --retry 3 https://go.dev/dl/go1.27.4.darwin-arm64.tar.gz -o "$ARCHIVE"
EXPECTED=91a44a2b8a716600ad94a4f143e156116620db8828086a6e3602291be9bd0bb4
ACTUAL="$(shasum -a 256 "$ARCHIVE" | cut -d ' ' -f 1)"
[ "$EXPECTED" = "$ACTUAL" ] || { echo 'Go archive checksum mismatch' >&2; exit 1; }
tar -xzf "$ARCHIVE" -C "$TOOLS"
"$TOOLS/go/bin/go" version
