#!/bin/bash
set -uexo pipefail
controller-gen object paths="./..."
controller-gen rbac:roleName='"{{ include \"the-nat-controller.fullname\" . }}"' paths=./... output:dir=charts/the-nat-controller/templates
