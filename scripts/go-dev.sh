#!/bin/sh
set -eu
ROOT="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
GO="$ROOT/runtime/tools/go/bin/go"
if command -v go >/dev/null 2>&1; then GO="$(command -v go)"; fi
[ -x "$GO" ] || { echo '请先安装 Go 1.26+，本机可运行 sh scripts/install-go.sh' >&2; exit 1; }
mkdir -p "$ROOT/runtime"
cd "$ROOT/server"
"$GO" build -trimpath -o "$ROOT/runtime/starindex" ./cmd/starindex
cd "$ROOT"
exec "$ROOT/runtime/starindex" "${1:-all}" --root "$ROOT"
