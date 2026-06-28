#!/usr/bin/env bash
set -euo pipefail

output_file="${1:-deploy/compose/.nexus-admin-password}"
mkdir -p "$(dirname "${output_file}")"

docker compose -f deploy/compose/compose.yaml exec -T nexus cat /nexus-data/admin.password > "${output_file}"
chmod 600 "${output_file}"
echo "wrote Nexus admin password to ${output_file}"

