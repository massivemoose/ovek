#!/usr/bin/env bash

set -euo pipefail

if sudo podman compose version >/dev/null 2>&1; then
	printf '%s\n' "sudo podman compose -f podman-compose.yml"
	exit 0
fi

if sudo env PATH="/root/.local/bin:${PATH}" podman-compose --version >/dev/null 2>&1; then
	printf '%s\n' "sudo env PATH=/root/.local/bin:\$PATH podman-compose -f podman-compose.yml"
	exit 0
fi

if sudo /root/.local/bin/podman-compose --version >/dev/null 2>&1; then
	printf '%s\n' "sudo /root/.local/bin/podman-compose -f podman-compose.yml"
	exit 0
fi

if sudo podman-compose --version >/dev/null 2>&1; then
	printf '%s\n' "sudo podman-compose -f podman-compose.yml"
	exit 0
fi

exit 1
