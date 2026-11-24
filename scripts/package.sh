#!/bin/sh
# Build a clean server bundle from an allowlist; never copy local credentials/data.
set -eu
INSTALL_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
INSTALL_ARCH=${1:-amd64}
case "$INSTALL_ARCH" in amd64|arm64) ;; *) echo '用法：sh scripts/package.sh [amd64|arm64]' >&2; exit 1 ;; esac
INSTALL_GO="$INSTALL_ROOT/runtime/tools/go/bin/go"
if command -v go >/dev/null 2>&1; then INSTALL_GO=$(command -v go); fi
[ -x "$INSTALL_GO" ] || { echo '构建需要 Go 1.26+' >&2; exit 1; }
sh "$INSTALL_ROOT/scripts/build.sh"
INSTALL_STAGE=$(mktemp -d "${TMPDIR:-/tmp}/starindex-package.XXXXXXXX")
# This path is created by mktemp solely for packaging, never a project/user root.
trap 'rm -r -- "$INSTALL_STAGE"' EXIT HUP INT TERM
mkdir -p "$INSTALL_STAGE/starindex/public" "$INSTALL_STAGE/starindex/database" "$INSTALL_STAGE/starindex/runtime" "$INSTALL_ROOT/dist"
cd "$INSTALL_ROOT/server"
CGO_ENABLED=0 GOOS=linux GOARCH="$INSTALL_ARCH" "$INSTALL_GO" build -trimpath -o "$INSTALL_STAGE/starindex/starindex" ./cmd/starindex
cp -R "$INSTALL_ROOT/public/build" "$INSTALL_ROOT/public/icons" "$INSTALL_STAGE/starindex/public/"
cp "$INSTALL_ROOT/public/favicon.svg" "$INSTALL_STAGE/starindex/public/"
cp -R "$INSTALL_ROOT/database/migrations" "$INSTALL_STAGE/starindex/database/"
cp -R "$INSTALL_ROOT/deploy" "$INSTALL_STAGE/starindex/"
cp "$INSTALL_ROOT/docs/installation.md" "$INSTALL_STAGE/starindex/INSTALL.md"
cp "$INSTALL_ROOT/LICENSE.txt" "$INSTALL_STAGE/starindex/"
COPYFILE_DISABLE=1 tar -czf "$INSTALL_ROOT/dist/starindex-linux-$INSTALL_ARCH.tar.gz" -C "$INSTALL_STAGE" starindex
echo "部署包：$INSTALL_ROOT/dist/starindex-linux-$INSTALL_ARCH.tar.gz"
echo '部署包不含本地配置、密钥、登录信息或业务数据。'
