## hcloudfip

This controller assigns floating IPs to Nodes that request them.

A floating IP is requested by annotating the Node. Once the assignment has been confirmed by Hetzner Cloud API, the IP is added to a label on the Node. This label can be used for things like pod affinity, DaemonSets, Cilium host policies, and so on.

If the annotation is removed from a Node, the floating IP is unassigned and the label is removed.

The floating IPs available for assignment can be limited with a [hcloud API label selector](https://docs.hetzner.cloud/#label-selector). The Nodes that can assign IPs can also be limited with a Kubernetes [label selector](https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#label-selectors).

This controller can assign only one floating IP per Node. [hcloud-cloud-controller-manager](https://github.com/hetznercloud/hcloud-cloud-controller-manager) must be running in the cluster.
