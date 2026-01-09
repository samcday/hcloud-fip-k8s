#!/usr/bin/env bash
set -euxo pipefail

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
E2E_FIP_COUNT="${E2E_FIP_COUNT:-1}"
E2E_IMAGE_REPO="${E2E_IMAGE_REPO:-}"
E2E_IMAGE_TAG="${E2E_IMAGE_TAG:-}"
E2E_IMAGE_TAR="${E2E_IMAGE_TAR:-}"
E2E_SSH_USER="${E2E_SSH_USER:-root}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
E2E_SSH_DIR="${SCRIPT_DIR}/.ssh"
E2E_WORK_DIR="${SCRIPT_DIR}/.e2e"
E2E_CONFIG="${E2E_WORK_DIR}/hetzner-k3s.yaml"
E2E_KUBECONFIG="${E2E_WORK_DIR}/kubeconfig"

HCLOUD_TOKEN="${HCLOUD_TOKEN:-}"
if [[ -z "${HCLOUD_TOKEN}" && -f "${E2E_CONFIG}" ]]; then
  HCLOUD_TOKEN="$(awk '/^hetzner_token:/ {print $2; exit}' "${E2E_CONFIG}")"
fi
if [[ -z "${HCLOUD_TOKEN}" ]]; then
  log "HCLOUD_TOKEN is not set and no token was found in ${E2E_CONFIG}."
  exit 1
fi

mkdir -p "${E2E_SSH_DIR}" "${E2E_WORK_DIR}"
if [[ ! -s "${E2E_SSH_DIR}/id_ed25519" || ! -s "${E2E_SSH_DIR}/id_ed25519.pub" ]]; then
  rm -f "${E2E_SSH_DIR}/id_ed25519" "${E2E_SSH_DIR}/id_ed25519.pub"
  ssh-keygen -t ed25519 -N "" -f "${E2E_SSH_DIR}/id_ed25519" >/dev/null
fi

if [[ -z "${E2E_IMAGE_REPO}${E2E_IMAGE_TAG}${E2E_IMAGE_TAR}" ]]; then
  if command -v docker >/dev/null 2>&1; then
    E2E_IMAGE_REPO="hcloud-fip-k8s"
    E2E_IMAGE_TAG="e2e-$(date +%s)"
    E2E_IMAGE_TAR="${E2E_WORK_DIR}/hcloud-fip-k8s-${E2E_IMAGE_TAG}.tar"
    log "Building ${E2E_IMAGE_REPO}:${E2E_IMAGE_TAG} for e2e..."
    docker build -t "${E2E_IMAGE_REPO}:${E2E_IMAGE_TAG}" "${SCRIPT_DIR}/.." >&2
    docker save -o "${E2E_IMAGE_TAR}" "${E2E_IMAGE_REPO}:${E2E_IMAGE_TAG}" >&2
  else
    log "Docker not found; using chart default image."
  fi
fi

cat <<EOF
E2E_IMAGE_REPO=${E2E_IMAGE_REPO}
E2E_IMAGE_TAG=${E2E_IMAGE_TAG}
E2E_IMAGE_TAR=${E2E_IMAGE_TAR}
KUBECONFIG=${E2E_KUBECONFIG}
HCLOUD_TOKEN=${HCLOUD_TOKEN}
E2E_CLUSTER_NAME=${E2E_CLUSTER_NAME}
E2E_NAMESPACE=${E2E_NAMESPACE}
E2E_SELECTOR=${E2E_SELECTOR}
E2E_LABEL_KEY=${E2E_LABEL_KEY}
E2E_SETUP_ANNOTATION=${E2E_SETUP_ANNOTATION}
E2E_FIP_COUNT=${E2E_FIP_COUNT}
EOF

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

if [[ -n "${E2E_IMAGE_TAR}" ]]; then
  if [[ ! -f "${E2E_IMAGE_TAR}" ]]; then
    log "E2E_IMAGE_TAR ${E2E_IMAGE_TAR} does not exist."
    exit 1
  fi
  if [[ -z "${E2E_IMAGE_REPO}" || -z "${E2E_IMAGE_TAG}" ]]; then
    log "E2E_IMAGE_REPO and E2E_IMAGE_TAG must be set when E2E_IMAGE_TAR is provided."
    exit 1
  fi
  image_name="${E2E_IMAGE_REPO}:${E2E_IMAGE_TAG}"
  image_tar_name="$(basename "${E2E_IMAGE_TAR}")"
  ssh_opts=(-i "${E2E_SSH_DIR}/id_ed25519" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null)
  while IFS=$'\t' read -r name external internal; do
    ip="${external:-${internal}}"
    if [[ -z "${ip}" ]]; then
      log "No IP found for node ${name}."
      exit 1
    fi
    if ssh "${ssh_opts[@]}" "${E2E_SSH_USER}@${ip}" "k3s ctr images list | grep -Fq '${image_name}'" </dev/null; then
      log "Image ${image_name} already present on ${name}, skipping import."
      continue
    fi
    log "Importing ${image_name} into node ${name} (${ip})..."
    ssh "${ssh_opts[@]}" "${E2E_SSH_USER}@${ip}" "mkdir -p /var/lib/rancher/k3s/agent/images" </dev/null >/dev/null
    if command -v rsync >/dev/null 2>&1 && ssh "${ssh_opts[@]}" "${E2E_SSH_USER}@${ip}" "command -v rsync" </dev/null >/dev/null 2>&1; then
      rsync -az --checksum -e "ssh ${ssh_opts[*]}" "${E2E_IMAGE_TAR}" "${E2E_SSH_USER}@${ip}:/var/lib/rancher/k3s/agent/images/${image_tar_name}" >/dev/null
    else
      scp "${ssh_opts[@]}" "${E2E_IMAGE_TAR}" "${E2E_SSH_USER}@${ip}:/var/lib/rancher/k3s/agent/images/${image_tar_name}" </dev/null >/dev/null
    fi
    imported=false
    for _ in $(seq 1 30); do
      if ssh "${ssh_opts[@]}" "${E2E_SSH_USER}@${ip}" "k3s ctr images list | grep -Fq '${image_name}'" </dev/null; then
        imported=true
        break
      fi
      sleep 2
    done
    if [[ "${imported}" != "true" ]]; then
      ssh "${ssh_opts[@]}" "${E2E_SSH_USER}@${ip}" "k3s ctr images import /var/lib/rancher/k3s/agent/images/${image_tar_name}" </dev/null >/dev/null
      if ssh "${ssh_opts[@]}" "${E2E_SSH_USER}@${ip}" "k3s ctr images list | grep -Fq '${image_name}'" </dev/null; then
        imported=true
      fi
    fi
    if [[ "${imported}" != "true" ]]; then
      log "Failed to import ${image_name} into node ${name}."
      exit 1
    fi
  done < <(kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.addresses[?(@.type=="ExternalIP")].address}{"\t"}{.status.addresses[?(@.type=="InternalIP")].address}{"\n"}{end}')
fi

kubectl create namespace "${E2E_NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
if ! kubectl -n "${E2E_NAMESPACE}" get secret hcloud >/dev/null 2>&1; then
  kubectl -n "${E2E_NAMESPACE}" create secret generic hcloud \
    --from-literal=token="${HCLOUD_TOKEN}" \
    --dry-run=client -o yaml | kubectl apply -f - >/dev/null
else
  log "Secret ${E2E_NAMESPACE}/hcloud already exists; leaving token unchanged."
fi

helm_args=()
if [[ -n "${E2E_IMAGE_REPO}" ]]; then
  helm_args+=(--set "image.repository=${E2E_IMAGE_REPO}")
fi
if [[ -n "${E2E_IMAGE_TAG}" ]]; then
  helm_args+=(--set "image.tag=${E2E_IMAGE_TAG}")
fi
if [[ -n "${E2E_IMAGE_TAR}" ]]; then
  helm_args+=(--set "image.pullPolicy=IfNotPresent")
fi

helm upgrade --install hcloud-fip-k8s ./chart \
  --namespace "${E2E_NAMESPACE}" \
  --set floatingIP.selector="${E2E_SELECTOR}" \
  --set floatingIP.label="${E2E_LABEL_KEY}" \
  --set floatingIP.setupAnnotation="${E2E_SETUP_ANNOTATION}" \
  "${helm_args[@]}" >/dev/null

kubectl -n "${E2E_NAMESPACE}" rollout status deployment/hcloud-fip-k8s --timeout=5m >&2

echo E2E_RUN=1
