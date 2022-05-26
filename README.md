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
