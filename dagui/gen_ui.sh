#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

UI_DIR="$SCRIPT_DIR/ui"
if [[ ! -d "$UI_DIR" ]]; then
  echo "error: $UI_DIR not found"; exit 1
fi

pushd "$UI_DIR" >/dev/null
if [[ -f package-lock.json ]]; then npm ci; else npm install; fi
npm run build
popd >/dev/null

# nothing to copy; go:embed reads from ./ui/dist
echo "UI built at $UI_DIR/dist"
