#!/bin/sh
set -eu
umask 077

PROJECT_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
RUNTIME_DIR="$PROJECT_ROOT/runtime"
RUN_USER=${1:-idcsobot}
LISTEN_ADDRESS=${2:-127.0.0.1:8088}

# aaPanel assigns ownership while starting Go projects. Restore the writable
# state directory immediately before replacing the launcher with the Go process.
RUN_GROUP=$(id -gn "$RUN_USER")
install -d -m 0750 -o "$RUN_USER" -g "$RUN_GROUP" "$RUNTIME_DIR"
touch "$RUNTIME_DIR/bot.lock"
chown "$RUN_USER:$RUN_GROUP" "$RUNTIME_DIR/bot.lock"
chmod 0600 "$RUNTIME_DIR/bot.lock"
exec setpriv --reuid="$RUN_USER" --regid="$RUN_GROUP" --init-groups --no-new-privs -- \
  "$RUNTIME_DIR/bin/starindex" all --root "$PROJECT_ROOT" --addr "$LISTEN_ADDRESS"
