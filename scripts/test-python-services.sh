#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VENV="${PYTHON_TEST_VENV:-${ROOT}/.tmp_python_test_venv}"
PYTHON_BIN="${PYTHON_BIN:-python3}"

if [[ ! -x "${VENV}/bin/python" ]]; then
  "${PYTHON_BIN}" -m venv "${VENV}"
fi

"${VENV}/bin/python" -m pip install --upgrade pip
"${VENV}/bin/python" -m pip install \
  "protobuf>=6.31.1" \
  grpcio grpcio-tools \
  "requests[socks]" curl-cffi PySocks \
  "psycopg[binary]" \
  faker maxminddb

run_unittest() {
  local service="$1"
  shift || true
  echo "[python-test] ${service}"
  (cd "${ROOT}/${service}" && "${VENV}/bin/python" -m unittest discover -v "$@")
}

"${VENV}/bin/python" -m grpc_tools.protoc \
  -I "${ROOT}/proto" \
  --python_out="${ROOT}/checkphone-tgbot" \
  --grpc_python_out="${ROOT}/checkphone-tgbot" \
  "${ROOT}/proto/email.proto" \
  "${ROOT}/proto/gopay_app.proto" \
  "${ROOT}"/proto/orchestrator*.proto

run_unittest browser-reg
run_unittest checkphone-tgbot
run_unittest gopay-app
run_unittest gopay-payment/gopay-flow
run_unittest herosms-sms-service
