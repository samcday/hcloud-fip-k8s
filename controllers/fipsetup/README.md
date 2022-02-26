# fipsetup

This controller ensures that whenever a Node receives a floating IP assignment label, a setup Job is run. Similarly,
whenever the Node loses or changes its label, a tear down Job for the previous value is run. This teardown Job must
complete before any subsequent setup Jobs can begin, and vice versa.

The Jobs that are run target the Node with a fixed nodeSelector, and thus will schedule successfully on cordoned Nodes.
