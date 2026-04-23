#!/usr/bin/env bash

set -euo pipefail

repo_dir="$(pwd)"

if sudo --preserve-env=PWD env PWD="${repo_dir}" podman compose version >/dev/null 2>&1; then
	exec sudo --preserve-env=PWD env PWD="${repo_dir}" podman compose -f podman-compose.yml "$@"
fi

if sudo --preserve-env=PWD env PATH="/root/.local/bin:${PATH}" PWD="${repo_dir}" podman-compose --version >/dev/null 2>&1; then
	exec sudo --preserve-env=PWD env PATH="/root/.local/bin:${PATH}" PWD="${repo_dir}" podman-compose -f podman-compose.yml "$@"
fi

if sudo --preserve-env=PWD env PWD="${repo_dir}" /root/.local/bin/podman-compose --version >/dev/null 2>&1; then
	exec sudo --preserve-env=PWD env PWD="${repo_dir}" /root/.local/bin/podman-compose -f podman-compose.yml "$@"
fi

if sudo --preserve-env=PWD env PWD="${repo_dir}" podman-compose --version >/dev/null 2>&1; then
	exec sudo --preserve-env=PWD env PWD="${repo_dir}" podman-compose -f podman-compose.yml "$@"
fi

echo "No compose provider is available inside the Podman machine." >&2
echo "Run 'make podman-vm-bootstrap-compose' and retry." >&2
exit 1
