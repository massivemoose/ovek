#!/usr/bin/env bash

set -euo pipefail

env_file="${OVEK_ENV_FILE:-/etc/ovek/ovek.env}"
brain_url="${OVEK_CHECK_BRAIN_URL:-http://127.0.0.1/v1/ping}"
brain_host="${OVEK_CHECK_BRAIN_HOST:-brain.localhost}"
api_key="${OVEK_CHECK_API_KEY:-dev-brain-key}"

failures=0
warnings=0
sudo_ready=0
sudo_cmd=()

ok() {
	printf 'ok: %s\n' "$*"
}

warn() {
	warnings=$((warnings + 1))
	printf 'warn: %s\n' "$*" >&2
}

fail() {
	failures=$((failures + 1))
	printf 'fail: %s\n' "$*" >&2
}

run_priv() {
	"${sudo_cmd[@]}" "$@"
}

check_ubuntu() {
	if [ ! -r /etc/os-release ]; then
		fail "/etc/os-release is not readable"
		return
	fi

	# shellcheck disable=SC1091
	. /etc/os-release
	if [ "${ID:-}" != "ubuntu" ]; then
		fail "expected Ubuntu, detected ${ID:-unknown}"
		return
	fi

	ok "Ubuntu host detected${VERSION_ID:+ (${VERSION_ID})}"
}

check_sudo() {
	if [ "$(id -u)" -eq 0 ]; then
		sudo_ready=1
		sudo_cmd=()
		ok "running as root"
		return
	fi

	if ! command -v sudo >/dev/null 2>&1; then
		fail "sudo is required when not running as root"
		return
	fi

	sudo_cmd=(sudo -n)
	if ! sudo -n true >/dev/null 2>&1; then
		fail "sudo is installed but cannot run non-interactively; run 'sudo -v' and retry"
		return
	fi

	sudo_ready=1
	ok "sudo is available without an interactive prompt"
}

check_podman() {
	if ! command -v podman >/dev/null 2>&1; then
		fail "podman is not installed or not on PATH"
		return
	fi

	podman_version="$(podman --version 2>/dev/null || true)"
	if [ -z "${podman_version}" ]; then
		fail "podman is installed but did not report a version"
		return
	fi

	ok "${podman_version}"
}

check_compose() {
	if command -v podman >/dev/null 2>&1 && podman compose version >/dev/null 2>&1; then
		ok "podman compose provider is available"
		return
	fi

	if command -v podman-compose >/dev/null 2>&1 && podman-compose --version >/dev/null 2>&1; then
		ok "podman-compose provider is available"
		return
	fi

	fail "no Podman compose provider found"
}

check_service() {
	service_name="$1"

	if ! command -v systemctl >/dev/null 2>&1; then
		fail "systemctl is not available; cannot inspect ${service_name}"
		return
	fi

	if systemctl is-active --quiet "${service_name}"; then
		ok "${service_name} is active"
		return
	fi

	fail "${service_name} is not active"
}

check_env_file() {
	if [ "${sudo_ready}" != "1" ]; then
		fail "cannot inspect ${env_file} without non-interactive sudo"
		return
	fi

	if ! env_content="$(run_priv cat "${env_file}" 2>/dev/null)"; then
		fail "${env_file} is missing or not readable"
		return
	fi

	if printf '%s\n' "${env_content}" | grep -Eq '^RUNTIME_ENGINE=podman$'; then
		ok "${env_file} sets RUNTIME_ENGINE=podman"
	else
		fail "${env_file} does not set RUNTIME_ENGINE=podman"
	fi

	if printf '%s\n' "${env_content}" | grep -Eq '^RUNTIME_HOST=unix:///run/podman/podman.sock$'; then
		ok "${env_file} points Brain at the Podman socket"
	else
		fail "${env_file} does not set RUNTIME_HOST=unix:///run/podman/podman.sock"
	fi
}

check_containers() {
	if [ "${sudo_ready}" != "1" ]; then
		fail "cannot inspect Podman containers without non-interactive sudo"
		return
	fi

	if ! container_names="$(run_priv podman ps --format '{{.Names}}' 2>/dev/null)"; then
		fail "sudo podman ps failed"
		return
	fi

	if printf '%s\n' "${container_names}" | grep -Fxq brain; then
		ok "brain container is running"
	else
		fail "brain container is not running"
	fi

	if printf '%s\n' "${container_names}" | grep -Fxq traefik; then
		ok "traefik container is running"
	else
		fail "traefik container is not running"
	fi
}

check_brain() {
	if ! command -v curl >/dev/null 2>&1; then
		fail "curl is not installed or not on PATH"
		return
	fi

	if curl -fsS -H "Host: ${brain_host}" -H "X-API-Key: ${api_key}" "${brain_url}" >/dev/null 2>&1; then
		ok "Brain is reachable at ${brain_url} with Host: ${brain_host}"
		return
	fi

	fail "Brain did not respond at ${brain_url} with Host: ${brain_host}"
}

main() {
	check_ubuntu
	check_sudo
	check_podman
	check_compose
	check_service podman.socket
	check_service ovek.service
	check_env_file
	check_containers
	check_brain

	if [ "${warnings}" -gt 0 ]; then
		printf 'warnings: %s\n' "${warnings}" >&2
	fi
	if [ "${failures}" -gt 0 ]; then
		printf 'failures: %s\n' "${failures}" >&2
		exit 1
	fi

	printf 'Ovek VPS preflight passed.\n'
}

main "$@"
