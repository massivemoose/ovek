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
	log "Installing Podman and compose dependencies"
	run_sudo apt-get update
	run_sudo apt-get install -y ca-certificates curl git make podman podman-compose
}

ensure_podman() {
	log "Enabling podman.socket"
	run_sudo systemctl enable --now podman.socket
}

compose_command() {
	if command -v podman >/dev/null 2>&1 && podman compose version >/dev/null 2>&1; then
		printf '%s\n' "$(command -v podman) compose"
		return
	fi

	if command -v podman-compose >/dev/null 2>&1; then
		printf '%s\n' "$(command -v podman-compose)"
		return
	fi

	fail "no Podman compose provider found after package installation"
}

install_runtime_files() {
	log "Installing runtime stack into ${install_dir}"
	run_sudo mkdir -p "${install_dir}" "${config_dir}" "${data_dir}/projects" "${data_dir}/traefik/dynamic" "${data_dir}/job-logs"
	run_sudo chmod 0755 "${install_dir}"
	run_sudo chmod 0755 "${config_dir}"
	run_sudo chmod 0750 "${data_dir}"

	tar -C "${repo_root}" -cf - Dockerfile.brain podman-compose.yml go.mod go.sum cmd/brain internal | run_sudo tar -C "${install_dir}" -xf -

	if [ -L "${install_dir}/brain_data" ]; then
		return
	fi
	if [ -e "${install_dir}/brain_data" ]; then
		log "${install_dir}/brain_data already exists; leaving it in place"
		return
	fi
	run_sudo ln -s "${data_dir}" "${install_dir}/brain_data"
}

write_env_file() {
	if [ -f "${env_file}" ]; then
		log "Keeping existing ${env_file}"
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
EOF
	run_sudo install -m 0600 -o root -g root "${tmp_file}" "${env_file}"
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
	run_sudo install -m 0644 -o root -g root "${tmp_file}" "${service_file}"
	rm -f "${tmp_file}"
}

start_service() {
	log "Starting ovek.service"
	run_sudo systemctl daemon-reload
	run_sudo systemctl enable --now ovek.service
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
	printf 'Next: ssh -N -L 8088:127.0.0.1:80 <user>@<vps-host>\n'
	printf 'Then: ovek auth bootstrap --profile vps-trial --host http://brain.localhost:8088\n'
}

main "$@"
