#!/usr/bin/env bash

set -euo pipefail

machine_name="${PODMAN_MACHINE_NAME:-podman-machine-default}"

if ! podman machine inspect "${machine_name}" >/dev/null 2>&1; then
	echo "Podman machine '${machine_name}' does not exist. Run 'make podman-machine-init' first." >&2
	exit 1
fi

rootful_mode="$(podman machine inspect --format '{{.Rootful}}' "${machine_name}")"
machine_state="$(podman machine inspect --format '{{.State}}' "${machine_name}")"

if [ "${rootful_mode}" != "true" ]; then
	podman machine stop "${machine_name}" >/dev/null 2>&1 || true
	podman machine set --rootful=true "${machine_name}"
	machine_state="stopped"
fi

if [ "${machine_state}" != "running" ]; then
	podman machine start "${machine_name}"
fi

if [ "$(podman machine inspect --format '{{.Rootful}}' "${machine_name}")" != "true" ]; then
	echo "Podman machine '${machine_name}' is not running in rootful mode." >&2
	exit 1
fi
