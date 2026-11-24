#!/bin/sh
set -eu
TASK_ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
TASK_BUNDLED_NODE='/Users/jim/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/bin'
if ! command -v node >/dev/null 2>&1 && [ -x "$TASK_BUNDLED_NODE/node" ]; then
  PATH="$TASK_BUNDLED_NODE:$PATH"
  export PATH
fi
cd "$TASK_ROOT/frontend"
if ! command -v node >/dev/null 2>&1; then
  echo '需要 Node.js 22.12+，然后执行 pnpm install。' >&2
  exit 1
fi
if [ ! -x node_modules/.bin/vue-tsc ]; then
  echo '请先在 frontend 目录执行 pnpm install。' >&2
  exit 1
fi
node_modules/.bin/vue-tsc --noEmit
node_modules/.bin/vite build "$@"
