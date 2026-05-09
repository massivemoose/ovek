#!/usr/bin/env bash

set -euo pipefail

machine="${PODMAN_MACHINE:-podman-machine-default}"
vm_dir="${OVEK_VM_DIR:-/var/home/core/ovek}"
brain_base_url="${OVEK_BASE_URL:-http://127.0.0.1}"
brain_host="${OVEK_BRAIN_HOST:-brain.localhost}"
api_key="${OVEK_API_KEY:-dev-brain-key}"

usage() {
	cat <<'EOF'
Usage:
  ./pm <command> [args...]

Stack lifecycle:
  ./pm init                    Create the Podman machine if needed
  ./pm rootful                 Ensure the Podman machine is rootful
  ./pm sync                    Sync this repo into the Podman machine
  ./pm bootstrap-compose       Install a compose provider in the VM
  ./pm up                      Build/recreate the Podman VM stack
  ./pm smoke                   Run the Podman VM smoke suite
  ./pm capsule-smoke           Run the Podman VM capsule smoke suite
  ./pm builder-up              Build/recreate the legacy builder stack
  ./pm builder-smoke           Run the legacy source-build smoke suite
  ./pm builder-down            Stop/remove the legacy builder stack
  ./pm down                    Stop/remove the Podman VM stack

VM/container inspection:
  ./pm compose <args...>        Run compose inside the VM repo
  ./pm podman <args...>         Run rootful podman inside the VM
  ./pm ps [args...]             Shortcut for: podman ps
  ./pm logs <container> [args]  Shortcut for: podman logs
  ./pm shell                   Open a shell in the VM repo
  ./pm ssh [command...]         SSH into the Podman machine

HTTP helpers:
  ./pm api [METHOD] <path> [curl args...]
                               Curl Brain through Traefik with auth headers
                               Default METHOD is GET
  ./pm app <project> [path] [curl args...]
                               Curl a routed app through Traefik
  ./pm curl <host> <path> [curl args...]
                               Curl an arbitrary routed host

Examples:
  ./pm compose logs brain
  ./pm podman ps -a
  ./pm api GET /v1/projects/demo-app/runtime
  ./pm api DELETE /v1/projects/demo-app/runtime
  ./pm app demo-app /
  ./pm curl brain.localhost /v1/ping

Environment:
  PODMAN_MACHINE       default: podman-machine-default
  OVEK_VM_DIR         default: /var/home/core/ovek
  OVEK_BASE_URL       default: http://127.0.0.1
  OVEK_BRAIN_HOST     default: brain.localhost
  OVEK_API_KEY        default: dev-brain-key
  PM_QUIET=1           do not print the underlying command
EOF
}

fail() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}

quote_arg() {
	printf '%q' "$1"
}

join_quoted_args() {
	local first=1
	local arg

	for arg in "$@"; do
		if [ "${first}" -eq 0 ]; then
			printf ' '
		fi
		quote_arg "${arg}"
		first=0
	done
}

print_command() {
	if [ "${PM_QUIET:-0}" = "1" ]; then
		return
	fi

	printf '=>'
	local arg
	for arg in "$@"; do
		printf ' %q' "${arg}"
	done
	printf '\n'
}

run_host() {
	print_command "$@"
	"$@"
}

run_vm() {
	local command="$1"

	run_host podman machine ssh "${machine}" "${command}"
}

run_vm_repo() {
	local command="$1"

	run_vm "cd $(quote_arg "${vm_dir}") && ${command}"
}

require_args() {
	local command="$1"
	local count="$2"

	if [ "${count}" -eq 0 ]; then
		fail "${command} requires arguments"
	fi
}

curl_brain() {
	local method="$1"
	local path="$2"
	shift 2

	case "${path}" in
		http://*|https://*)
			url="${path}"
			;;
		/*)
			url="${brain_base_url}${path}"
			;;
		*)
			url="${brain_base_url}/${path}"
			;;
	esac

	run_host curl -sS -H "Host: ${brain_host}" -H "X-API-Key: ${api_key}" -X "${method}" "$@" "${url}"
}

command="${1:-help}"
if [ "$#" -gt 0 ]; then
	shift
fi

case "${command}" in
	help|-h|--help)
		usage
		;;
	init)
		run_host make podman-machine-init
		;;
	rootful)
		run_host make podman-machine-rootful
		;;
	sync)
		run_host make podman-machine-sync
		;;
	bootstrap-compose)
		run_host make podman-vm-bootstrap-compose
		;;
	up)
		run_host make podman-vm-up
		;;
	smoke)
		run_host make podman-vm-smoke
		;;
	capsule-smoke)
		run_host make podman-vm-capsule-smoke
		;;
	builder-up)
		run_host make podman-vm-builder-up
		;;
	builder-smoke)
		run_host make podman-vm-builder-smoke
		;;
	builder-down)
		run_host make podman-vm-builder-down
		;;
	down)
		run_host make podman-vm-down
		;;
	compose)
		require_args "compose" "$#"
		run_vm_repo "./scripts/podman-machine-compose.sh $(join_quoted_args "$@")"
		;;
	podman)
		require_args "podman" "$#"
		run_vm "sudo podman $(join_quoted_args "$@")"
		;;
	ps)
		run_vm "sudo podman ps $(join_quoted_args "$@")"
		;;
	logs)
		require_args "logs" "$#"
		run_vm "sudo podman logs $(join_quoted_args "$@")"
		;;
	shell)
		run_vm_repo "bash -l"
		;;
	ssh)
		if [ "$#" -eq 0 ]; then
			run_host podman machine ssh "${machine}"
		else
			run_vm "$(join_quoted_args "$@")"
		fi
		;;
	api)
		if [ "$#" -eq 0 ]; then
			fail "api requires a path or METHOD path"
		fi

		method="GET"
		case "${1}" in
			GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)
				method="$1"
				shift
				;;
		esac
		if [ "$#" -eq 0 ]; then
			fail "api requires a path"
		fi
		path="$1"
		shift
		curl_brain "${method}" "${path}" "$@"
		;;
	app)
		if [ "$#" -eq 0 ]; then
			fail "app requires a project name"
		fi
		project="$1"
		shift
		path="${1:-/}"
		if [ "$#" -gt 0 ]; then
			shift
		fi
		case "${path}" in
			/*) ;;
			*) path="/${path}" ;;
		esac
		run_host curl -sS -H "Host: ${project}.localhost" "$@" "${brain_base_url}${path}"
		;;
	curl)
		if [ "$#" -lt 2 ]; then
			fail "curl requires a host and path"
		fi
		host="$1"
		path="$2"
		shift 2
		case "${path}" in
			http://*|https://*)
				url="${path}"
				;;
			/*)
				url="${brain_base_url}${path}"
				;;
			*)
				url="${brain_base_url}/${path}"
				;;
		esac
		run_host curl -sS -H "Host: ${host}" "$@" "${url}"
		;;
	*)
		fail "unknown command ${command}. Run './pm help'."
		;;
esac
