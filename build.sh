#!/bin/bash
set -uexo pipefail
go install sigs.k8s.io/controller-tools/cmd/controller-gen@latest
controller-gen object paths="./..."
controller-gen rbac:roleName='"{{ include \"hcloud-fip-k8s.fullname\" . }}"' paths=./... output:dir=charts/hcloud-fip-k8s/templates
