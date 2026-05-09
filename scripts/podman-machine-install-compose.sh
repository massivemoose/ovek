#!/usr/bin/env bash

set -euo pipefail

if ./scripts/podman-machine-compose-check.sh >/dev/null 2>&1; then
	echo "A compose provider is already available inside the Podman machine."
	exit 0
fi

sudo python3 -m ensurepip --upgrade
sudo python3 -m pip install --upgrade --user podman-compose

if ! ./scripts/podman-machine-compose-check.sh >/dev/null 2>&1; then
	echo "Failed to install a compose provider inside the Podman machine." >&2
	exit 1
fi

echo "Installed podman-compose inside the Podman machine."
