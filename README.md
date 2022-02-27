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

TODO: helm install instructions

TODO: rendered manifest install instructions
