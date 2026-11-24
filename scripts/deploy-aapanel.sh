#!/bin/sh
set -eu
umask 022

PROJECT_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
RUNTIME_DIR="$PROJECT_ROOT/runtime"
PUBLIC_BUILD="$PROJECT_ROOT/public/build"
GO_BIN=${STARINDEX_GO:-go}

for tool in "$GO_BIN" node pnpm flock; do
  command -v "$tool" >/dev/null 2>&1 || {
    printf '缺少构建工具：%s\n' "$tool" >&2
    exit 1
  }
done

mkdir -p "$RUNTIME_DIR" "$PUBLIC_BUILD"
exec 9>"$RUNTIME_DIR/.aapanel-release.lock"
flock -n 9 || { echo '另一个部署构建正在运行。' >&2; exit 1; }

BINARY_STAGE=$(mktemp -d "$RUNTIME_DIR/.aapanel-release.XXXXXXXX")
FRONTEND_STAGE=
cleanup() {
  rm -rf -- "$BINARY_STAGE"
  if [ -n "$FRONTEND_STAGE" ]; then
    rm -rf -- "$FRONTEND_STAGE"
  fi
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
FRONTEND_STAGE=$(mktemp -d "$PUBLIC_BUILD/.aapanel-release.XXXXXXXX")

(cd "$PROJECT_ROOT/frontend" && pnpm install --frozen-lockfile --prod=false)
sh "$PROJECT_ROOT/scripts/build.sh" --outDir "$FRONTEND_STAGE"
GO_TARGET_ARCH=$("$GO_BIN" env GOHOSTARCH)
(cd "$PROJECT_ROOT/server" && CGO_ENABLED=0 GOOS=linux GOARCH="$GO_TARGET_ARCH" \
  "$GO_BIN" build -trimpath -o "$BINARY_STAGE/starindex" ./cmd/starindex)

test -s "$FRONTEND_STAGE/index.html"
test -x "$BINARY_STAGE/starindex"
mkdir -p "$PUBLIC_BUILD/assets"
# Publish hashed bundles before switching the HTML entry. Open pages can still load older bundles.
mv -f "$FRONTEND_STAGE"/assets/* "$PUBLIC_BUILD/assets/"
mv -f "$BINARY_STAGE/starindex" "$RUNTIME_DIR/starindex"
mv -f "$FRONTEND_STAGE/index.html" "$PUBLIC_BUILD/index.html"

printf '发布完成：%s\n' "$RUNTIME_DIR/starindex"
echo '请在宝塔 Go 项目中启动或重启服务。'
