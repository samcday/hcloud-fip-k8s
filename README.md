# hcloud-fip-k8s

A controller to manage Hetzner Cloud floating IPs natively from your Kubernetes cluster.

This controller ensures that:

 * All matching floating IPs are assigned to a Node, and that Node has a label indicating the assignment.
 * Only one Node has the label indicating assignment for a particular floating IP.
 * Whenever possible, floating IPs are assigned to schedulable Nodes.
 * The floating IP is configured on the Node it is assigned to.

## Demo

TODO

## Installation

### Helm

This is the recommended way to install hcloud-fip-k8s.

```sh
helm repo add hcloud-fip-k8s https://samcday.github.io/hcloud-fip-k8s/
helm repo update hcloud-fip-k8s
helm install hcloud-fip-k8s hcloud-fip-k8s/hcloud-fip-k8s \
 --set floatingIP.selector=role=egress \
 --set floatingIP.label=node-role.kubernetes.io/egress \
 --set floatingIP.setupAnnotation=hcloud-fip-k8s.samcday.com/egress
```

### Static manifests

TODO

## E2E tests

E2E tests are Go tests in `e2e/` run against a real Hetzner Cloud cluster.
Use `hack/e2e-start.sh` to create a 2-node k3s cluster (via `vitobotta/hetzner-k3s`)
and install the controller; use `hack/e2e-stop.sh` to delete it.

```sh
export E2E_RUN=1
set -a
source <(./hack/e2e-start.sh)
set +a
E2E_FIP_COUNT=1 go test ./e2e -v
./hack/e2e-stop.sh
```

Requires `hetzner-k3s`, `kubectl`, `helm`, and a configured `hcloud` context (or set `HCLOUD_TOKEN`).
