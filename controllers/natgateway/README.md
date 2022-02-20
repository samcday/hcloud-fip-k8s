## natgateway

This controller ensures that the Nodes of a cluster are configured to use a NAT gateway, or act as one. It does this by ensuring that a single *primary* eligible NAT gateway Node is selected, and that all other Nodes send their default traffic through it.

The Nodes to be configured can be limited with a label selector. The primary NAT gateway Node will be indicated with a configurable [node role label](https://stackoverflow.com/a/48856238).

## Eligible NAT gateways

A Node is considered an *eligible* NAT gateway if:

 * It has a label indicating assignment of a public floating IP (likely from the hcloudfip controller), and
 * it also has an annotation indicating it has been set up to act as a NAT gateway, matching the IP in the label.

The annotation is added after a "setup" Job has been successfully created and run. This Job is expected to configure the Node to act as a NAT gateway for the floating IP in the annotation/label.

The annotation is removed if it no longer matches the label, and a "teardown" Job has been successfully created and run. This Job is expected to clean up the changes made by the setup Job.

The Job images is configurable, defaulting to BusyBox. The setup/teardown scripts are configurable, defaulting to ones appropriate for Ubuntu 20.04. The scripts are provided a `FLOATING_IP` environment variable, and are run in a privileged (root) chroot in the `/` of the Node. So, like, be careful what you run in there, yeah? 

When creating setup and teardown jobs for an IP, if there exists any Jobs of the corresponding type, but for a different IP, they are deleted, regardless of their running state (pending, running, completed). The new setup/teardown Jobs are not created until the deletion has fully taken place. The created Jobs are given a configurable [TTL](https://kubernetes.io/docs/concepts/workloads/controllers/ttlafterfinished/), defaulting to 1 hour.

## Primary NAT gateway

Of the eligible NAT gateway Nodes described above, one is chosen to be the *primary*. It can only be the primary if it is eligible, **and** it is schedulable (i.e, Ready and not cordoned).

A default (`0.0.0.0/0`) route is added to the configured Hetzner Cloud private network, pointing to the primary Node.

If a default route already exists, it is changed if it's not pointing to a currently eligible NAT gateway.
