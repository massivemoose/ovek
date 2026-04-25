#!/usr/bin/env bash

set -euo pipefail

brain_base_url="${OVEK_BASE_URL:-http://127.0.0.1}"
brain_host="${OVEK_BRAIN_HOST:-brain.localhost}"
api_key="${OVEK_API_KEY:-dev-brain-key}"
project_name="${OVEK_PROJECT_NAME:-demo-app}"
project_host="${OVEK_PROJECT_HOST:-${project_name}.localhost}"
repo_url="${OVEK_REPO_URL:-https://github.com/heroku/nodejs-getting-started.git}"
compose_cmd="${COMPOSE_CMD:-sudo podman compose -f podman-compose.yml}"
max_attempts="${OVEK_MAX_ATTEMPTS:-120}"
sleep_seconds="${OVEK_POLL_INTERVAL_SECONDS:-2}"
progress_interval="${OVEK_PROGRESS_INTERVAL_ATTEMPTS:-5}"

log() {
	printf '==> %s\n' "$*"
}

fail() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}

brain_curl() {
	curl -fsS \
		-H "Host: ${brain_host}" \
		-H "X-API-Key: ${api_key}" \
		"$@"
}

json_string_field() {
	printf '%s' "$1" | tr -d '\n' | sed -n 's/.*"'"$2"'":"\([^"]*\)".*/\1/p'
}

print_job_failure_context() {
	job_id="$1"
	job_body="$2"

	job_error_message="$(json_string_field "${job_body}" errorMessage)"
	job_logs_url="${brain_base_url}/v1/jobs/${job_id}/logs"

	if [ -n "${job_error_message}" ]; then
		printf 'job_error: %s\n' "${job_error_message}" >&2
	fi

	printf 'job_logs_url: %s\n' "${job_logs_url}" >&2
	printf '%s\n' "--- job logs (${job_id}) ---" >&2
	brain_curl "${job_logs_url}" >&2 || true
	printf '%s\n' "--- end job logs (${job_id}) ---" >&2
}

wait_for_ping() {
	attempt=1
	while [ "${attempt}" -le "${max_attempts}" ]; do
		if brain_curl "${brain_base_url}/v1/ping" >/dev/null 2>&1; then
			return 0
		fi

		if [ "${attempt}" -eq 1 ] || [ $((attempt % progress_interval)) -eq 0 ]; then
			log "Still waiting for Brain (${attempt}/${max_attempts})"
		fi

		sleep "${sleep_seconds}"
		attempt=$((attempt + 1))
	done

	fail "Brain did not respond at ${brain_base_url}/v1/ping"
}

deploy_project() {
	headers_file="$(mktemp)"
	body_file="$(mktemp)"

	status_code="$(curl -sS \
		-w '%{http_code}' \
		-D "${headers_file}" \
		-o "${body_file}" \
		-H "Host: ${brain_host}" \
		-H "X-API-Key: ${api_key}" \
		-H "Content-Type: application/json" \
		-d "{\"repoUrl\":\"${repo_url}\"}" \
		"${brain_base_url}/v1/projects/${project_name}/deployments")"

	if [ "${status_code}" -lt 200 ] || [ "${status_code}" -ge 300 ]; then
		printf '%s\n' "--- deployment create response ---" >&2
		cat "${body_file}" >&2 || true
		printf '\n%s\n' "--- end deployment create response ---" >&2
		rm -f "${headers_file}" "${body_file}"
		fail "Deployment request returned HTTP ${status_code}"
	fi

	location_header="$(awk '/^Location:/ {print $2}' "${headers_file}" | tr -d '\r' | tail -n 1)"

	rm -f "${headers_file}" "${body_file}"

	if [ -z "${location_header}" ]; then
		fail "Deployment response did not include a Location header"
	fi

	printf '%s\n' "${location_header##*/}"
}

wait_for_job() {
	job_id="$1"
	attempt=1

	while [ "${attempt}" -le "${max_attempts}" ]; do
		job_body="$(brain_curl "${brain_base_url}/v1/jobs/${job_id}")"
		job_status="$(json_string_field "${job_body}" status)"

		case "${job_status}" in
			succeeded)
				return 0
				;;
			failed)
				print_job_failure_context "${job_id}" "${job_body}"
				fail "Job ${job_id} failed"
				;;
		esac

		if [ "${attempt}" -eq 1 ] || [ $((attempt % progress_interval)) -eq 0 ]; then
			log "Job ${job_id} still ${job_status:-pending} (${attempt}/${max_attempts})"
		fi

		sleep "${sleep_seconds}"
		attempt=$((attempt + 1))
	done

	job_body="$(brain_curl "${brain_base_url}/v1/jobs/${job_id}")"
	print_job_failure_context "${job_id}" "${job_body}"
	fail "Job ${job_id} did not reach a terminal state"
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

http_code() {
	curl -sS -o /dev/null -w '%{http_code}' "$@"
}

wait_for_app_http_200() {
	attempt=1
	last_status_code=""

	while [ "${attempt}" -le "${max_attempts}" ]; do
		last_status_code="$(http_code -H "Host: ${project_host}" "${brain_base_url}/")"
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
			"${brain_base_url}/v1/projects/${project_name}/runtime")"

		if [ "${status_code}" = "204" ]; then
			rm -f "${body_file}"
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

log "Waiting for Brain"
wait_for_ping

log "Creating first deployment"
first_job_id="$(deploy_project)"
log "First deployment job: ${first_job_id}"
wait_for_job "${first_job_id}"
first_job_logs="$(brain_curl "${brain_base_url}/v1/jobs/${first_job_id}/logs")"
require_contains "${first_job_logs}" "railpack prepare"
require_contains "${first_job_logs}" '$ buildctl '
require_contains "${first_job_logs}" "--output type=image"

first_runtime="$(brain_curl "${brain_base_url}/v1/projects/${project_name}/runtime")"
first_image_ref="$(json_string_field "${first_runtime}" imageRef)"
[ -n "${first_image_ref}" ] || fail "First runtime did not report an imageRef"

log "Checking runtime logs and routed app after first deploy"
runtime_logs="$(brain_curl "${brain_base_url}/v1/projects/${project_name}/runtime/logs")"
[ -n "${runtime_logs}" ] || fail "Runtime logs endpoint returned no content"

wait_for_app_http_200

log "Creating second deployment"
second_job_id="$(deploy_project)"
log "Second deployment job: ${second_job_id}"
wait_for_job "${second_job_id}"
second_runtime="$(brain_curl "${brain_base_url}/v1/projects/${project_name}/runtime")"
second_image_ref="$(json_string_field "${second_runtime}" imageRef)"
[ -n "${second_image_ref}" ] || fail "Second runtime did not report an imageRef"
[ "${second_image_ref}" != "${first_image_ref}" ] || fail "Second deployment did not replace the current runtime image"

log "Restarting Brain to validate reconciliation"
bash -lc "${compose_cmd} restart brain"
wait_for_ping
post_restart_runtime="$(brain_curl "${brain_base_url}/v1/projects/${project_name}/runtime")"
post_restart_image_ref="$(json_string_field "${post_restart_runtime}" imageRef)"
[ "${post_restart_image_ref}" = "${second_image_ref}" ] || fail "Runtime image changed after Brain restart"

log "Cleaning up project runtime"
cleanup_project_runtime

runtime_body="$(brain_curl "${brain_base_url}/v1/projects/${project_name}/runtime")"
runtime_current_deployment_id="$(json_string_field "${runtime_body}" currentDeploymentId)"
[ -z "${runtime_current_deployment_id}" ] || fail "Expected runtime currentDeploymentId to be empty after cleanup, got ${runtime_current_deployment_id}"
require_contains "${runtime_body}" '"app":null'
require_contains "${runtime_body}" '"pocketBase":null'
require_contains "${runtime_body}" '"network":null'

runtime_logs_status_code="$(http_code -H "Host: ${brain_host}" -H "X-API-Key: ${api_key}" "${brain_base_url}/v1/projects/${project_name}/runtime/logs")"
[ "${runtime_logs_status_code}" = "404" ] || fail "Expected runtime logs endpoint to return HTTP 404 after cleanup, got ${runtime_logs_status_code}"

project_body="$(brain_curl "${brain_base_url}/v1/projects/${project_name}")"
project_status="$(json_string_field "${project_body}" status)"
[ "${project_status}" = "idle" ] || fail "Expected project status to be idle after cleanup, got ${project_status}"

log "Smoke suite passed"
