#!/usr/bin/env bash

set -euo pipefail

machine_name="${PODMAN_MACHINE_NAME:-podman-machine-default}"
vm_dir="${ALCES_VM_DIR:-/var/home/core/alces}"
repo_root="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
exclude_file="${repo_root}/.podman-machine-syncignore"

if [ ! -f "${exclude_file}" ]; then
	echo "Missing sync exclude file: ${exclude_file}" >&2
	exit 1
fi

if ! podman machine inspect "${machine_name}" >/dev/null 2>&1; then
	echo "Podman machine '${machine_name}' does not exist. Run 'make podman-machine-init' first." >&2
	exit 1
fi

tar_flags=()
if [ "$(uname -s)" = "Darwin" ]; then
	tar_flags+=(--no-mac-metadata)
fi

COPYFILE_DISABLE=1 COPY_EXTENDED_ATTRIBUTES_DISABLE=1 tar "${tar_flags[@]}" -C "${repo_root}" -cf - --exclude-from="${exclude_file}" . | \
	podman machine ssh "${machine_name}" "mkdir -p '${vm_dir}' && find '${vm_dir}' -mindepth 1 -maxdepth 1 ! -name brain_data -exec rm -rf {} + && tar --warning=no-unknown-keyword -xf - -C '${vm_dir}'"
