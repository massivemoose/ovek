#!/usr/bin/env bash

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ovek_bin_explicit=0
if [ -n "${OVEK_BIN:-}" ]; then
	ovek_bin_explicit=1
fi
ovek_bin="${OVEK_BIN:-${repo_root}/bin/ovek}"
brain_base_url="${OVEK_BASE_URL:-http://127.0.0.1}"
brain_host="${OVEK_BRAIN_HOST:-brain.localhost}"
brain_cli_url="${OVEK_BRAIN_URL:-http://${brain_host}}"
api_key="${OVEK_API_KEY:-dev-brain-key}"
project_name="${OVEK_PROJECT_NAME:-signup-demo}"
project_host="${OVEK_PROJECT_HOST:-${project_name}.localhost}"
capsule_image="${OVEK_CAPSULE_IMAGE:-ghcr.io/massivemoose/ovek-signup-example:latest}"
max_attempts="${OVEK_MAX_ATTEMPTS:-120}"
sleep_seconds="${OVEK_POLL_INTERVAL_SECONDS:-2}"
progress_interval="${OVEK_PROGRESS_INTERVAL_ATTEMPTS:-5}"

if [ -n "${OVEK_SMOKE_CONFIG_HOME:-}" ]; then
	config_root="${OVEK_SMOKE_CONFIG_HOME}"
	remove_config_root=0
else
	config_root="$(mktemp -d)"
	remove_config_root=1
fi

cleanup_runtime_on_exit=0

log() {
	printf '==> %s\n' "$*"
}

fail() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}

brain_api_url() {
	case "$1" in
		http://*|https://*)
			printf '%s\n' "$1"
			;;
		/*)
			printf '%s%s\n' "${brain_base_url%/}" "$1"
			;;
		*)
			printf '%s/%s\n' "${brain_base_url%/}" "$1"
			;;
	esac
}

brain_curl() {
	curl -fsS \
		-H "Host: ${brain_host}" \
		-H "X-API-Key: ${api_key}" \
		"$@"
}

http_code() {
	curl -sS -o /dev/null -w '%{http_code}' "$@" 2>/dev/null || printf '000'
}

run_ovek() {
	XDG_CONFIG_HOME="${config_root}" "${ovek_bin}" "$@"
}

require_contains() {
	haystack="$1"
	needle="$2"

	case "${haystack}" in
		*"${needle}"*)
			;;
		*)
			fail "Expected output to contain '${needle}'"
			;;
	esac
}

require_regex() {
	haystack="$1"
	pattern="$2"

	if ! printf '%s\n' "${haystack}" | grep -Eq "${pattern}"; then
		fail "Expected output to match '${pattern}'"
	fi
}

cleanup_project_runtime_quiet() {
	curl -sS -o /dev/null \
		-X DELETE \
		-H "Host: ${brain_host}" \
		-H "X-API-Key: ${api_key}" \
		"$(brain_api_url "/v1/projects/${project_name}/runtime")" >/dev/null 2>&1 || true
}

diagnostic_podman() {
	if command -v sudo >/dev/null 2>&1 && sudo -n podman ps >/dev/null 2>&1; then
		sudo -n podman "$@"
		return
	fi
	if command -v podman >/dev/null 2>&1 && podman ps >/dev/null 2>&1; then
		podman "$@"
		return
	fi

	return 127
}

print_capsule_failure_diagnostics() {
	job_id="${1:-}"
	app_container=""
	if [ -n "${job_id}" ]; then
		app_container="ovek-${project_name}-app-${job_id}"
	fi
	pb_container="ovek-${project_name}-pb"

	printf '%s\n' "--- capsule failure diagnostics ---" >&2

	if [ -n "${job_id}" ]; then
		printf '%s\n' "--- job logs (${job_id}) ---" >&2
		run_ovek logs --job "${job_id}" --no-follow >&2 || true
	fi

	printf '%s\n' "--- database status (${project_name}) ---" >&2
	run_ovek db status "${project_name}" >&2 || true

	printf '%s\n' "--- podman containers ---" >&2
	diagnostic_podman ps -a \
		--filter "name=ovek-${project_name}" \
		--format 'table {{.Names}}\t{{.Status}}\t{{.Image}}' >&2 || printf '%s\n' "podman diagnostics unavailable" >&2

	if [ -n "${app_container}" ]; then
		printf '%s\n' "--- app container state (${app_container}) ---" >&2
		diagnostic_podman inspect "${app_container}" \
			--format 'status={{.State.Status}} exit={{.State.ExitCode}} oom={{.State.OOMKilled}} error={{.State.Error}}' >&2 || true

		printf '%s\n' "--- app container env names (${app_container}) ---" >&2
		diagnostic_podman inspect "${app_container}" \
			--format '{{range .Config.Env}}{{println .}}{{end}}' 2>/dev/null | sed 's/=.*//' | sort >&2 || true

		printf '%s\n' "--- app container logs (${app_container}) ---" >&2
		diagnostic_podman logs "${app_container}" >&2 || true
	fi

	printf '%s\n' "--- PocketBase container state (${pb_container}) ---" >&2
	diagnostic_podman inspect "${pb_container}" \
		--format 'status={{.State.Status}} exit={{.State.ExitCode}} oom={{.State.OOMKilled}} error={{.State.Error}}' >&2 || true

	printf '%s\n' "--- PocketBase container logs (${pb_container}) ---" >&2
	diagnostic_podman logs "${pb_container}" >&2 || true

	printf '%s\n' "--- end capsule failure diagnostics ---" >&2
}

cleanup() {
	if [ "${cleanup_runtime_on_exit}" = "1" ]; then
		cleanup_project_runtime_quiet
	fi
	if [ "${remove_config_root}" = "1" ]; then
		rm -rf "${config_root}"
	fi
}
trap cleanup EXIT

build_ovek() {
	if [ "${ovek_bin_explicit}" = "1" ]; then
		if [ ! -x "${ovek_bin}" ]; then
			fail "OVEK_BIN=${ovek_bin} is not executable"
		fi
		log "Using Ovek CLI from OVEK_BIN=${ovek_bin}"
	else
		log "Building Ovek CLI at ${ovek_bin}"
		mkdir -p "$(dirname "${ovek_bin}")"
		(cd "${repo_root}" && go build -o "${ovek_bin}" ./cmd/ovek)
	fi

	db_help="$("${ovek_bin}" help db 2>/dev/null || true)"
	if ! grep -Eq 'ovek db (init|status|tunnel)' <<<"${db_help}"; then
		fail "Ovek CLI at ${ovek_bin} does not support 'ovek db'; rebuild it or unset OVEK_BIN"
	fi
}

wait_for_ping() {
	attempt=1
	while [ "${attempt}" -le "${max_attempts}" ]; do
		if brain_curl "$(brain_api_url /v1/ping)" >/dev/null 2>&1; then
			return 0
		fi

		if [ "${attempt}" -eq 1 ] || [ $((attempt % progress_interval)) -eq 0 ]; then
			log "Still waiting for Brain (${attempt}/${max_attempts})"
		fi

		sleep "${sleep_seconds}"
		attempt=$((attempt + 1))
	done

	fail "Brain did not respond at $(brain_api_url /v1/ping)"
}

ensure_pocketbase_app_secrets() {
	status_output=""
	if status_output="$(run_ovek db status "${project_name}" 2>&1)"; then
		if printf '%s\n' "${status_output}" | grep -Eq '^Initialized[[:space:]]+yes' &&
			printf '%s\n' "${status_output}" | grep -Eq '^App Secrets[[:space:]]+yes'; then
			log "Database app secrets already configured"
			return
		fi

		printf '%s\n' "${status_output}" >&2
		fail "Database exists for ${project_name}, but app secrets are not configured"
	fi

	log "Initializing database app secrets"
	cleanup_runtime_on_exit=1
	if ! init_output="$(run_ovek db init "${project_name}" --app-secrets 2>&1)"; then
		printf '%s\n' "${init_output}" >&2
		fail "Database initialization failed"
	fi
	require_contains "${init_output}" "Database initialized."
	require_regex "${init_output}" '^App Secrets[[:space:]]+yes'
}

wait_for_app_http_200() {
	attempt=1
	last_status_code=""

	while [ "${attempt}" -le "${max_attempts}" ]; do
		last_status_code="$(http_code -H "Host: ${project_host}" "${brain_base_url%/}/")"
		if [ "${last_status_code}" = "200" ]; then
			return 0
		fi

		if [ "${attempt}" -eq 1 ] || [ $((attempt % progress_interval)) -eq 0 ]; then
			log "Routed app returned HTTP ${last_status_code}; waiting (${attempt}/${max_attempts})"
		fi

		sleep "${sleep_seconds}"
		attempt=$((attempt + 1))
	done

	fail "Expected routed app to return HTTP 200, got ${last_status_code}"
}

cleanup_project_runtime() {
	max_cleanup_attempts="${OVEK_CLEANUP_MAX_ATTEMPTS:-5}"
	cleanup_retry_seconds="${OVEK_CLEANUP_RETRY_SECONDS:-2}"
	attempt=1

	while [ "${attempt}" -le "${max_cleanup_attempts}" ]; do
		body_file="$(mktemp)"
		status_code="$(curl -sS \
			-o "${body_file}" \
			-w '%{http_code}' \
			-X DELETE \
			-H "Host: ${brain_host}" \
			-H "X-API-Key: ${api_key}" \
			"$(brain_api_url "/v1/projects/${project_name}/runtime")")"

		if [ "${status_code}" = "204" ]; then
			rm -f "${body_file}"
			cleanup_runtime_on_exit=0
			return 0
		fi

		if [ "${status_code}" -ge 500 ] && [ "${attempt}" -lt "${max_cleanup_attempts}" ]; then
			log "Cleanup returned HTTP ${status_code}; retrying (${attempt}/${max_cleanup_attempts})"
			rm -f "${body_file}"
			sleep "${cleanup_retry_seconds}"
			attempt=$((attempt + 1))
			continue
		fi

		printf '%s\n' "--- cleanup response ---" >&2
		cat "${body_file}" >&2 || true
		printf '\n%s\n' "--- end cleanup response ---" >&2
		rm -f "${body_file}"
		fail "Expected cleanup to return HTTP 204, got ${status_code}"
	done

	fail "Cleanup did not succeed after ${max_cleanup_attempts} attempts"
}

build_ovek

log "Waiting for Brain"
wait_for_ping

log "Authenticating isolated smoke profile"
mkdir -p "${config_root}"
run_ovek auth login --profile capsule-smoke --host "${brain_cli_url%/}" --api-key "${api_key}" >/dev/null

ensure_pocketbase_app_secrets

log "Running capsule ${capsule_image}"
run_output_file="$(mktemp)"
if ! run_ovek run "${project_name}" "${capsule_image}" >"${run_output_file}" 2>&1; then
	cat "${run_output_file}" >&2 || true
	failed_job_id="$(awk '$1 == "Job" {print $2; exit}' "${run_output_file}")"
	print_capsule_failure_diagnostics "${failed_job_id}"
	rm -f "${run_output_file}"
	fail "Capsule run failed"
fi
run_output="$(cat "${run_output_file}")"
job_id="$(awk '$1 == "Job" {print $2; exit}' "${run_output_file}")"
rm -f "${run_output_file}"

[ -n "${job_id}" ] || fail "Could not extract job ID from ovek run output"
cleanup_runtime_on_exit=1
require_contains "${run_output}" "Status    succeeded"
require_contains "${run_output}" "${capsule_image}"

log "Validating project status"
status_output="$(run_ovek status "${project_name}")"
require_regex "${status_output}" '^Status[[:space:]]+running'
require_contains "${status_output}" "${capsule_image}"

log "Validating image-run job logs"
job_logs="$(run_ovek logs --job "${job_id}" --no-follow)"
require_contains "${job_logs}" "lifecycle: using prebuilt image ${capsule_image}"
require_contains "${job_logs}" "lifecycle: image ready"
require_contains "${job_logs}" "lifecycle: provisioning PocketBase"
require_contains "${job_logs}" "lifecycle: starting app container"
require_contains "${job_logs}" "lifecycle: waiting for app readiness"
require_contains "${job_logs}" "lifecycle: deployment promoted"

log "Validating runtime logs"
runtime_logs="$(run_ovek logs "${project_name}" --no-follow)"
[ -n "${runtime_logs}" ] || fail "Runtime logs endpoint returned no content"

log "Validating routed app"
wait_for_app_http_200

log "Validating database sidecar"
db_status="$(run_ovek db status "${project_name}")"
require_regex "${db_status}" '^Running[[:space:]]+yes'
require_regex "${db_status}" '^Initialized[[:space:]]+yes'
require_regex "${db_status}" '^App Secrets[[:space:]]+yes'

log "Cleaning up project runtime"
cleanup_project_runtime

runtime_body="$(brain_curl "$(brain_api_url "/v1/projects/${project_name}/runtime")")"
require_contains "${runtime_body}" '"app":null'
require_contains "${runtime_body}" '"pocketBase":null'
require_contains "${runtime_body}" '"network":null'

runtime_logs_status_code="$(http_code -H "Host: ${brain_host}" -H "X-API-Key: ${api_key}" "$(brain_api_url "/v1/projects/${project_name}/runtime/logs")")"
[ "${runtime_logs_status_code}" = "404" ] || fail "Expected runtime logs endpoint to return HTTP 404 after cleanup, got ${runtime_logs_status_code}"

post_cleanup_status="$(run_ovek status "${project_name}")"
require_regex "${post_cleanup_status}" '^Status[[:space:]]+idle'

log "Capsule smoke suite passed"
