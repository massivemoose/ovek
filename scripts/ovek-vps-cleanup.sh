#!/usr/bin/env bash

set -euo pipefail

install_dir="${OVEK_INSTALL_DIR:-/opt/ovek}"
config_dir="${OVEK_CONFIG_DIR:-/etc/ovek}"
data_dir="${OVEK_DATA_DIR:-/var/lib/ovek}"
env_file="${OVEK_ENV_FILE:-${config_dir}/ovek.env}"
service_file="${OVEK_SERVICE_FILE:-/etc/systemd/system/ovek.service}"
vps_compose_file="podman-compose.vps.yml"
assume_yes=0
dry_run=0
sudo_cmd=()
containers=()
networks=()

log() {
	printf '==> %s\n' "$*"
}

warn() {
	printf 'warn: %s\n' "$*" >&2
}

fail() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}

usage() {
	cat <<EOF
Usage:
  ./scripts/ovek-vps-cleanup.sh [--yes] [--dry-run]

Removes the Ovek runtime installed by scripts/ovek-vps-install.sh from an
Ubuntu VPS. The script prints the removal plan first and requires confirmation
unless --yes is provided.
EOF
}

parse_args() {
	for arg in "$@"; do
		case "${arg}" in
		--yes|-y)
			assume_yes=1
			;;
		--dry-run)
			dry_run=1
			;;
		--help|-h)
			usage
			exit 0
			;;
		*)
			fail "unknown argument ${arg}"
			;;
		esac
	done
}

require_ubuntu() {
	if [ ! -r /etc/os-release ]; then
		fail "this cleanup script expects Ubuntu and could not read /etc/os-release"
	fi

	# shellcheck disable=SC1091
	. /etc/os-release
	if [ "${ID:-}" != "ubuntu" ]; then
		fail "this cleanup script currently supports Ubuntu only; detected ${ID:-unknown}"
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

run_cleanup() {
	if [ "${dry_run}" -eq 1 ]; then
		printf 'dry-run:'
		printf ' %q' "${sudo_cmd[@]}" "$@"
		printf '\n'
		return 0
	fi

	"${sudo_cmd[@]}" "$@"
}

run_sudo_best_effort() {
	if ! run_cleanup "$@"; then
		warn "command failed: $*"
	fi
}

append_unique() {
	value="$1"
	shift
	for existing in "$@"; do
		if [ "${existing}" = "${value}" ]; then
			return
		fi
	done
	printf '%s\n' "${value}"
}

collect_containers() {
	if ! command -v podman >/dev/null 2>&1; then
		return
	fi

	for name in brain traefik; do
		if run_sudo podman container exists "${name}" >/dev/null 2>&1; then
			containers+=("${name}")
		fi
	done

	while IFS= read -r name; do
		if [ -z "${name}" ]; then
			continue
		fi
		if [ "${#containers[@]}" -eq 0 ]; then
			containers+=("${name}")
		elif unique="$(append_unique "${name}" "${containers[@]}")"; then
			if [ -n "${unique}" ]; then
				containers+=("${unique}")
			fi
		fi
	done < <(run_sudo podman ps -a --filter label=ovek.managed=true --format '{{.Names}}' 2>/dev/null || true)
}

collect_networks() {
	if ! command -v podman >/dev/null 2>&1; then
		return
	fi

	if run_sudo podman network exists ovek-net >/dev/null 2>&1; then
		networks+=("ovek-net")
	fi

	while IFS= read -r name; do
		if [ -z "${name}" ]; then
			continue
		fi
		if [ "${#networks[@]}" -eq 0 ]; then
			networks+=("${name}")
		elif unique="$(append_unique "${name}" "${networks[@]}")"; then
			if [ -n "${unique}" ]; then
				networks+=("${unique}")
			fi
		fi
	done < <(run_sudo podman network ls --filter label=ovek.managed=true --format '{{.Name}}' 2>/dev/null || true)
}

print_list() {
	if [ "$#" -eq 0 ]; then
		printf '  none detected\n'
		return
	fi
	for item in "$@"; do
		printf '  %s\n' "${item}"
	done
}

print_plan() {
	log "Ovek VPS cleanup plan"
	printf 'Systemd:\n'
	printf '  stop/disable/remove ovek.service if present\n'
	printf '  %s\n' "${service_file}"
	printf 'Runtime files:\n'
	printf '  %s\n' "${install_dir}"
	printf '  %s\n' "${config_dir}"
	printf '  %s\n' "${data_dir}"
	printf '  %s\n' "${env_file}"
	printf 'Podman containers:\n'
	print_list "${containers[@]}"
	printf 'Podman networks:\n'
	print_list "${networks[@]}"
	printf '\n'
	printf 'This script does not remove unrelated Podman images, volumes, or containers.\n'
}

confirm_cleanup() {
	if [ "${assume_yes}" -eq 1 ]; then
		return
	fi

	printf 'Type "remove ovek" to remove the runtime state listed above: '
	read -r confirmation
	if [ "${confirmation}" != "remove ovek" ]; then
		fail "cleanup not confirmed"
	fi
}

compose_command() {
	if command -v podman >/dev/null 2>&1 && podman compose version >/dev/null 2>&1; then
		printf '%s compose\n' "$(command -v podman)"
		return
	fi
	if command -v podman-compose >/dev/null 2>&1 && podman-compose --version >/dev/null 2>&1; then
		printf '%s\n' "$(command -v podman-compose)"
		return
	fi
	return 1
}

stop_systemd_service() {
	if ! command -v systemctl >/dev/null 2>&1; then
		warn "systemctl is not available; skipping service removal"
		return
	fi

	if run_sudo test -f "${service_file}"; then
		log "Stopping and disabling ovek.service"
		run_sudo_best_effort systemctl disable --now ovek.service
		log "Removing ${service_file}"
		run_cleanup rm -f "${service_file}"
		run_sudo_best_effort systemctl daemon-reload
		run_sudo_best_effort systemctl reset-failed ovek.service
	else
		log "No ovek.service unit found"
	fi
}

compose_down() {
	if ! run_sudo test -f "${install_dir}/${vps_compose_file}"; then
		return
	fi
	if ! compose_cmd="$(compose_command)"; then
		warn "no Podman compose provider found; removing detected containers and networks directly"
		return
	fi

	log "Stopping VPS runtime stack with compose"
	run_sudo_best_effort /bin/sh -lc "cd '${install_dir}' && ${compose_cmd} -f '${vps_compose_file}' down"
}

remove_containers() {
	if [ "${#containers[@]}" -eq 0 ]; then
		return
	fi
	log "Removing Ovek Podman containers"
	run_sudo_best_effort podman rm -f "${containers[@]}"
}

remove_networks() {
	if [ "${#networks[@]}" -eq 0 ]; then
		return
	fi
	log "Removing Ovek Podman networks"
	for network in "${networks[@]}"; do
		run_sudo_best_effort podman network rm "${network}"
	done
}

remove_files() {
	log "Removing Ovek runtime files"
	run_cleanup rm -rf "${install_dir}" "${config_dir}" "${data_dir}"
}

main() {
	parse_args "$@"
	require_ubuntu
	require_sudo
	collect_containers
	collect_networks
	print_plan
	confirm_cleanup
	stop_systemd_service
	compose_down
	remove_containers
	remove_networks
	remove_files
	log "Ovek VPS runtime state removed"
}

main "$@"
