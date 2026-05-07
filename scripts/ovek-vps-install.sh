#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
install_dir="${OVEK_INSTALL_DIR:-/opt/ovek}"
config_dir="${OVEK_CONFIG_DIR:-/etc/ovek}"
data_dir="${OVEK_DATA_DIR:-/var/lib/ovek}"
env_file="${OVEK_ENV_FILE:-${config_dir}/ovek.env}"
service_file="${OVEK_SERVICE_FILE:-/etc/systemd/system/ovek.service}"

log() {
	printf '==> %s\n' "$*"
}

fail() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}

fail_with_hints() {
	message="$1"
	shift
	printf 'error: %s\n' "${message}" >&2
	for hint in "$@"; do
		printf 'hint: %s\n' "${hint}" >&2
	done
	exit 1
}

require_ubuntu() {
	if [ ! -r /etc/os-release ]; then
		fail "this installer expects Ubuntu and could not read /etc/os-release"
	fi

	. /etc/os-release
	if [ "${ID:-}" != "ubuntu" ]; then
		fail "this installer currently supports Ubuntu only; detected ${ID:-unknown}"
	fi
}

require_sudo() {
	if [ "$(id -u)" -eq 0 ]; then
		sudo_cmd=()
		return
	fi

	if ! command -v sudo >/dev/null 2>&1; then
		fail "sudo is required when not running as root"
	fi
	sudo_cmd=(sudo)
}

run_sudo() {
	"${sudo_cmd[@]}" "$@"
}

install_packages() {
	log "Updating Ubuntu package metadata"
	if ! run_sudo apt-get update; then
		fail_with_hints \
			"apt-get update failed" \
			"retry: sudo apt-get update" \
			"check network, DNS, and Ubuntu apt mirror availability"
	fi

	log "Installing Podman and compose dependencies"
	if ! run_sudo apt-get install -y ca-certificates curl git make podman podman-compose; then
		fail_with_hints \
			"package installation failed" \
			"retry: sudo apt-get install -y ca-certificates curl git make podman podman-compose" \
			"check the apt output above for the package or repository that failed"
	fi
}

ensure_podman() {
	log "Enabling podman.socket"
	if ! run_sudo systemctl enable --now podman.socket; then
		fail_with_hints \
			"could not enable and start podman.socket" \
			"inspect: sudo systemctl status podman.socket" \
			"logs: sudo journalctl -u podman.socket -n 100 --no-pager"
	fi
}

compose_command() {
	printf '==> Detecting Podman compose provider\n' >&2

	if command -v podman >/dev/null 2>&1; then
		if compose_output="$(podman compose version 2>&1)"; then
			printf '==> Using podman compose\n' >&2
			printf '%s\n' "$(command -v podman) compose"
			return
		fi
		printf 'note: podman compose is unavailable: %s\n' "${compose_output}" >&2
	fi

	if command -v podman-compose >/dev/null 2>&1; then
		if compose_output="$(podman-compose --version 2>&1)"; then
			printf '==> Using podman-compose\n' >&2
			printf '%s\n' "$(command -v podman-compose)"
			return
		fi
		printf 'note: podman-compose is unavailable: %s\n' "${compose_output}" >&2
	fi

	fail_with_hints \
		"no Podman compose provider found after package installation" \
		"inspect: podman compose version" \
		"inspect: podman-compose --version" \
		"retry install: sudo apt-get install -y podman-compose"
}

install_runtime_files() {
	log "Installing runtime stack into ${install_dir}"
	if run_sudo test -d "${data_dir}"; then
		log "Preserving existing data directory ${data_dir}"
	fi

	if ! run_sudo mkdir -p "${install_dir}" "${config_dir}" "${data_dir}/projects" "${data_dir}/traefik/dynamic" "${data_dir}/job-logs"; then
		fail_with_hints \
			"could not create runtime directories" \
			"inspect permissions for ${install_dir}, ${config_dir}, and ${data_dir}"
	fi
	if ! run_sudo chmod 0755 "${install_dir}"; then
		fail "could not set permissions on ${install_dir}"
	fi
	if ! run_sudo chmod 0755 "${config_dir}"; then
		fail "could not set permissions on ${config_dir}"
	fi
	if ! run_sudo chmod 0750 "${data_dir}"; then
		fail "could not set permissions on ${data_dir}"
	fi

	if ! tar -C "${repo_root}" -cf - Dockerfile.brain podman-compose.yml go.mod go.sum cmd/brain internal | run_sudo tar -C "${install_dir}" -xf -; then
		fail_with_hints \
			"could not copy runtime files into ${install_dir}" \
			"confirm the checkout contains Dockerfile.brain, podman-compose.yml, cmd/brain, and internal"
	fi

	if [ -L "${install_dir}/brain_data" ]; then
		log "Keeping existing ${install_dir}/brain_data symlink"
		return
	fi
	if [ -e "${install_dir}/brain_data" ]; then
		log "${install_dir}/brain_data already exists; leaving it in place"
		return
	fi
	if ! run_sudo ln -s "${data_dir}" "${install_dir}/brain_data"; then
		fail "could not link ${install_dir}/brain_data to ${data_dir}"
	fi
}

write_env_file() {
	if [ -f "${env_file}" ]; then
		log "Preserving existing ${env_file}; secrets and runtime data are unchanged"
		return
	fi

	log "Writing ${env_file}"
	secrets_key="$(head -c 32 /dev/urandom | base64)"
	tmp_file="$(mktemp)"
	cat >"${tmp_file}" <<EOF
OVEK_AUTH_MODE=prod
OVEK_SECRETS_KEY=${secrets_key}
RUNTIME_ENGINE=podman
RUNTIME_HOST=unix:///run/podman/podman.sock
POCKETBASE_IMAGE=docker.io/elestio/pocketbase:latest
EOF
	if ! run_sudo install -m 0600 -o root -g root "${tmp_file}" "${env_file}"; then
		rm -f "${tmp_file}"
		fail_with_hints \
			"could not write ${env_file}" \
			"inspect: sudo ls -ld ${config_dir}" \
			"retry after fixing permissions"
	fi
	rm -f "${tmp_file}"
}

write_systemd_unit() {
	log "Writing ${service_file}"
	compose_cmd="$(compose_command)"
	tmp_file="$(mktemp)"
	cat >"${tmp_file}" <<EOF
[Unit]
Description=Ovek runtime
Wants=network-online.target podman.socket
After=network-online.target podman.socket

[Service]
Type=oneshot
RemainAfterExit=yes
WorkingDirectory=${install_dir}
EnvironmentFile=${env_file}
ExecStartPre=/usr/bin/mkdir -p ${data_dir}/projects ${data_dir}/traefik/dynamic ${data_dir}/job-logs
ExecStart=/bin/sh -lc '${compose_cmd} -f podman-compose.yml up -d --build --force-recreate'
ExecStop=/bin/sh -lc '${compose_cmd} -f podman-compose.yml down'
TimeoutStartSec=600

[Install]
WantedBy=multi-user.target
EOF
	if ! run_sudo install -m 0644 -o root -g root "${tmp_file}" "${service_file}"; then
		rm -f "${tmp_file}"
		fail_with_hints \
			"could not write ${service_file}" \
			"inspect: sudo ls -ld $(dirname "${service_file}")"
	fi
	rm -f "${tmp_file}"
}

start_service() {
	log "Reloading systemd"
	if ! run_sudo systemctl daemon-reload; then
		fail_with_hints \
			"systemd daemon-reload failed" \
			"inspect: sudo systemctl status ovek.service"
	fi

	log "Starting ovek.service"
	if ! run_sudo systemctl enable --now ovek.service; then
		fail_with_hints \
			"ovek.service failed to start" \
			"inspect: sudo systemctl status ovek.service" \
			"logs: sudo journalctl -u ovek.service -n 100 --no-pager" \
			"containers: sudo podman ps -a"
	fi

	if ! run_sudo systemctl is-active --quiet ovek.service; then
		fail_with_hints \
			"ovek.service is not active after startup" \
			"inspect: sudo systemctl status ovek.service" \
			"logs: sudo journalctl -u ovek.service -n 100 --no-pager"
	fi
}

main() {
	require_ubuntu
	require_sudo
	install_packages
	ensure_podman
	install_runtime_files
	write_env_file
	write_systemd_unit
	start_service

	log "Ovek runtime is installed"
	printf 'Next commands:\n'
	printf './scripts/ovek-vps-check.sh\n'
	printf 'ssh -N -L 8088:127.0.0.1:80 <user>@<vps-host>\n'
	printf './bin/ovek auth bootstrap --profile vps-trial --host http://brain.localhost:8088\n'
}

main "$@"
