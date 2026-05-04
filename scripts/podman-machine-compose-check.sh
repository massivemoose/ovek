#!/usr/bin/env bash

set -euo pipefail

compose_file="${PODMAN_COMPOSE_FILE:-podman-compose.yml}"

if sudo podman compose version >/dev/null 2>&1; then
	printf 'sudo podman compose -f %q\n' "${compose_file}"
	exit 0
fi

if sudo env PATH="/root/.local/bin:${PATH}" podman-compose --version >/dev/null 2>&1; then
	printf 'sudo env PATH=/root/.local/bin:$PATH podman-compose -f %q\n' "${compose_file}"
	exit 0
fi

if sudo /root/.local/bin/podman-compose --version >/dev/null 2>&1; then
	printf 'sudo /root/.local/bin/podman-compose -f %q\n' "${compose_file}"
	exit 0
fi

if sudo podman-compose --version >/dev/null 2>&1; then
	printf 'sudo podman-compose -f %q\n' "${compose_file}"
	exit 0
fi

exit 1
