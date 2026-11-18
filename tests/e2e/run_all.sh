#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"
export COREPACK_ENABLE_DOWNLOAD_PROMPT=0

PYTHON_BIN="${PYTHON:-python}"
if [[ -z "${VR_ROUTER_PORT:-}" || -z "${VR_MOCK_PORT:-}" || -z "${VR_UI_PORT:-}" ]]; then
  read -r AUTO_ROUTER_PORT AUTO_MOCK_PORT AUTO_UI_PORT <<< "$("$PYTHON_BIN" - <<'PY'
import socket
ports = []
for _ in range(3):
    sock = socket.socket()
    sock.bind(("127.0.0.1", 0))
    ports.append(sock.getsockname()[1])
    sock.close()
print(*ports)
PY
)"
  export VR_ROUTER_PORT="${VR_ROUTER_PORT:-$AUTO_ROUTER_PORT}"
  export VR_MOCK_PORT="${VR_MOCK_PORT:-$AUTO_MOCK_PORT}"
  export VR_UI_PORT="${VR_UI_PORT:-$AUTO_UI_PORT}"
fi

VENV_DIR="dist/e2e-venv"
if [[ ! -x "$VENV_DIR/Scripts/python.exe" && ! -x "$VENV_DIR/bin/python" ]]; then
  "$PYTHON_BIN" -m venv "$VENV_DIR"
fi
if [[ -x "$VENV_DIR/Scripts/python.exe" ]]; then
  PY="$VENV_DIR/Scripts/python.exe"
else
  PY="$VENV_DIR/bin/python"
fi
"$PY" -m pip install -r tests/e2e/requirements.txt

corepack pnpm --dir web install --frozen-lockfile
corepack pnpm --dir web exec playwright install chromium
corepack pnpm --dir web build

EXT=""
case "$(uname -s)" in
  MINGW*|MSYS*|CYGWIN*) EXT=".exe" ;;
esac
go build -o "dist/viberouter-e2e${EXT}" .
go build -o "dist/mockupstream-e2e${EXT}" ./cmd/mockupstream
"$PY" tests/e2e/run_tests.py "dist/viberouter-e2e${EXT}" "dist/mockupstream-e2e${EXT}"
