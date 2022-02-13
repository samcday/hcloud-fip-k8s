## hcloudfip

This controller manages a pool of Hetzner Cloud floating IPs and ensures they are:

* assigned to Nodes that request them with a label.
* unassigned from Nodes that do not request them with a label.
* (optionally) randomly distributed to Nodes if not yet assigned or requested.

The Floating IPs that this controller manages can be limited with a [hcloud API label selector](https://docs.hetzner.cloud/#label-selector). Similarly, the Nodes that this controller will consider for assignment can be limited with a Kubernetes [label selector](https://kubernetes.io/docs/concepts/overview/working-with-objects/labels).

Each Node can have a maximum of one Floating IP assigned per controller instance. Multiple instances of the assignment controller can be run. Each must have a different requesting label and set of Floating IPs. Each controller can have the same or overlapping node selection.

The controller will never remove the label requesting a floating IP.

This controller requires that [hcloud-cloud-controller-manager](https://github.com/hetznercloud/hcloud-cloud-controller-manager) is running in the same cluster.
