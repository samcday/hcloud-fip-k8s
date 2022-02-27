#!/bin/bash
set -uexo pipefail
controller-gen object paths="./..."
controller-gen rbac:roleName='"{{ include \"hcloud-fip-k8s.fullname\" . }}"' paths=./... output:dir=charts/hcloud-fip-k8s/templates
