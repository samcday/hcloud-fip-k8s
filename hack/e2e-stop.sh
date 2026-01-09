#!/usr/bin/env bash
set -euo pipefail

HETZNER_K3S_BIN="${HETZNER_K3S_BIN:-hetzner-k3s}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
E2E_WORK_DIR="${SCRIPT_DIR}/.e2e"
E2E_CONFIG="${E2E_WORK_DIR}/hetzner-k3s.yaml"
E2E_SELECTOR="${E2E_SELECTOR:-}"

if [[ ! -f "${E2E_CONFIG}" ]]; then
  echo "No config found at ${E2E_CONFIG}; nothing to delete." >&2
  exit 1
fi

cluster_name="$(grep -m 1 "^cluster_name:" "${E2E_CONFIG}" | awk '{print $2}')"

if [[ -z "${cluster_name}" ]]; then
  echo "cluster_name missing from ${E2E_CONFIG}." >&2
  exit 1
fi

if [[ -z "${E2E_SELECTOR}" ]]; then
  E2E_SELECTOR="e2e-run=${cluster_name}"
fi

if command -v hcloud >/dev/null 2>&1; then
  fip_json="$(hcloud floating-ip list -l "${E2E_SELECTOR}" -o json 2>/dev/null || true)"
  if [[ -z "${fip_json}" ]]; then
    fip_json="[]"
  fi
  ids="$(printf '%s' "${fip_json}" | python - <<'PY'
import json
import sys

raw = sys.stdin.read().strip()
if not raw:
    print("")
    sys.exit(0)
data = json.loads(raw)
ids = [str(item.get("id")) for item in data if item.get("id") is not None]
print("\n".join(ids))
PY
)"
  if [[ -n "${ids}" ]]; then
    while IFS= read -r id; do
      if [[ -n "${id}" ]]; then
        hcloud floating-ip delete "${id}" >/dev/null
      fi
    done <<<"${ids}"
  fi
fi

printf '%s\n' "${cluster_name}" | "${HETZNER_K3S_BIN}" delete --config "${E2E_CONFIG}"
