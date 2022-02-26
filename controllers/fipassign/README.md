# fipassign

This controller ensures that:

* All matching floating IPs are assigned - either to a matching Node, or some other hcloud server.
* A maximum of one Node has the label indicating assignment of a floating IP.
* Whenever possible, floating IPs are assigned to schedulable Nodes.

It does this by regularly requesting the current status of floating IPs from the hcloud API and reconciling that with the matching Nodes in the cluster.

Each matching floating IP is assigned to a random Node if it is either:

1. unassigned
2. assigned to a matching *unschedulable* Node
3. assigned to a matching Node which has a differing label

The chosen random Node must not already have a label, and must be *schedulable*. The chosen node will be labelled immediately after the floating IP is assigned.

If the floating IP cannot be assigned to a suitable Node, no assignment takes place.

The label is removed from a particular Node if ALL of the following conditions are met:

* The floating IP has a currently valid assignment, i.e assigned to some matching Node, which is labelled.
* The valid assignment is not for the Node.
