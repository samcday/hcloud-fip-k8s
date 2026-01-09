#!/usr/bin/env bash
set -euo pipefail

log() {
  echo "$@" >&2
}

if [[ "${E2E_RUN:-}" != "1" ]]; then
  log "E2E_RUN=1 is required to run this harness."
  exit 1
fi

HETZNER_K3S_BIN="${HETZNER_K3S_BIN:-hetzner-k3s}"
E2E_CLUSTER_NAME="${E2E_CLUSTER_NAME:-hcloud-fip-k8s-e2e}"
E2E_NAMESPACE="${E2E_NAMESPACE:-hcloud-fip-k8s-e2e}"
E2E_SELECTOR="${E2E_SELECTOR:-e2e-run=${E2E_CLUSTER_NAME}}"
E2E_LABEL_KEY="${E2E_LABEL_KEY:-e2e.hcloud-fip-k8s.samcday.com/fip}"
E2E_SETUP_ANNOTATION="${E2E_SETUP_ANNOTATION:-e2e.hcloud-fip-k8s.samcday.com/fip}"
E2E_FIP_COUNT="${E2E_FIP_COUNT:-2}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
E2E_SSH_DIR="${SCRIPT_DIR}/.ssh"
E2E_WORK_DIR="${SCRIPT_DIR}/.e2e"
E2E_CONFIG="${E2E_WORK_DIR}/hetzner-k3s.yaml"
E2E_KUBECONFIG="${E2E_WORK_DIR}/kubeconfig"

if [[ -z "${HCLOUD_TOKEN}" ]]; then
  log "HCLOUD_TOKEN is not set and no active hcloud context token was found."
  exit 1
fi

mkdir -p "${E2E_SSH_DIR}" "${E2E_WORK_DIR}"
if [[ ! -s "${E2E_SSH_DIR}/id_ed25519" || ! -s "${E2E_SSH_DIR}/id_ed25519.pub" ]]; then
  rm -f "${E2E_SSH_DIR}/id_ed25519" "${E2E_SSH_DIR}/id_ed25519.pub"
  ssh-keygen -t ed25519 -N "" -f "${E2E_SSH_DIR}/id_ed25519" >/dev/null
fi

cat >"${E2E_CONFIG}" <<EOF
---
hetzner_token: ${HCLOUD_TOKEN}
cluster_name: ${E2E_CLUSTER_NAME}
k3s_version: v1.35.0+k3s1
kubeconfig_path: "${E2E_KUBECONFIG}"

networking:
  ssh:
    port: 22
    use_agent: false
    public_key_path: "${E2E_SSH_DIR}/id_ed25519.pub"
    private_key_path: "${E2E_SSH_DIR}/id_ed25519"
  allowed_networks:
    ssh:
      - 0.0.0.0/0
    api:
      - 0.0.0.0/0
  public_network:
    ipv4: true
    ipv6: true
  private_network:
    enabled: true
    subnet: 10.0.0.0/16
  cni:
    enabled: true
    encryption: false
    mode: flannel

datastore:
  mode: etcd

schedule_workloads_on_masters: false

masters_pool:
  instance_type: cx23
  instance_count: 1
  locations:
    - nbg1

worker_node_pools:
- name: e2e-workers
  instance_type: cx23
  instance_count: 1
  location: nbg1

protect_against_deletion: false
create_load_balancer_for_the_kubernetes_api: false
EOF

if [[ ! -s "${E2E_KUBECONFIG}" ]]; then
  log "Creating cluster..."
  "${HETZNER_K3S_BIN}" create --config "${E2E_CONFIG}" >&2
fi

export KUBECONFIG="${E2E_KUBECONFIG}"
kubectl wait --for=condition=Ready nodes --all --timeout=10m >&2
while IFS= read -r node; do
  if [[ -n "${node}" ]]; then
    kubectl uncordon "${node}" >&2
  fi
done < <(kubectl get nodes --no-headers | awk '$2 ~ /SchedulingDisabled/ {print $1}')

kubectl create namespace "${E2E_NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
kubectl -n "${E2E_NAMESPACE}" create secret generic hcloud \
  --from-literal=token="${HCLOUD_TOKEN}" \
  --dry-run=client -o yaml | kubectl apply -f - >/dev/null

helm upgrade --install hcloud-fip-k8s ./chart \
  --namespace "${E2E_NAMESPACE}" \
  --set floatingIP.selector="${E2E_SELECTOR}" \
  --set floatingIP.label="${E2E_LABEL_KEY}" \
  --set floatingIP.setupAnnotation="${E2E_SETUP_ANNOTATION}" >/dev/null

kubectl -n "${E2E_NAMESPACE}" rollout status deployment/hcloud-fip-k8s --timeout=5m >&2

cat <<EOF
E2E_RUN=1
KUBECONFIG=${E2E_KUBECONFIG}
HCLOUD_TOKEN=${HCLOUD_TOKEN}
E2E_NAMESPACE=${E2E_NAMESPACE}
E2E_SELECTOR=${E2E_SELECTOR}
E2E_LABEL_KEY=${E2E_LABEL_KEY}
E2E_SETUP_ANNOTATION=${E2E_SETUP_ANNOTATION}
E2E_FIP_COUNT=${E2E_FIP_COUNT}
EOF
