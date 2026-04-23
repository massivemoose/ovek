PODMAN_MACHINE ?= podman-machine-default
ALCES_VM_DIR ?= /var/home/core/alces
PODMAN_VM_REPO ?= $(ALCES_VM_DIR)
PODMAN_VM_COMPOSE := cd '$(PODMAN_VM_REPO)' && mkdir -p brain_data/projects brain_data/traefik/dynamic brain_data/job-logs

.PHONY: podman-machine-init
podman-machine-init:
	@if podman machine inspect '$(PODMAN_MACHINE)' >/dev/null 2>&1; then \
		echo 'Podman machine $(PODMAN_MACHINE) already exists.'; \
	else \
		podman machine init '$(PODMAN_MACHINE)'; \
	fi

.PHONY: podman-machine-start
podman-machine-start: podman-machine-init
	@if [ "$$(podman machine inspect --format '{{.State}}' '$(PODMAN_MACHINE)')" = "running" ]; then \
		echo 'Podman machine $(PODMAN_MACHINE) already running.'; \
	else \
		podman machine start '$(PODMAN_MACHINE)'; \
	fi

.PHONY: podman-machine-rootful
podman-machine-rootful: podman-machine-init
	PODMAN_MACHINE_NAME='$(PODMAN_MACHINE)' ./scripts/podman-machine-ensure-rootful.sh

.PHONY: podman-machine-sync
podman-machine-sync: podman-machine-rootful
	PODMAN_MACHINE_NAME='$(PODMAN_MACHINE)' ALCES_VM_DIR='$(ALCES_VM_DIR)' ./scripts/podman-machine-sync.sh

.PHONY: podman-vm-bootstrap-compose
podman-vm-bootstrap-compose: podman-machine-sync
	podman machine ssh '$(PODMAN_MACHINE)' "cd '$(PODMAN_VM_REPO)' && ./scripts/podman-machine-install-compose.sh"

.PHONY: podman-vm-check
podman-vm-check: podman-machine-sync
	podman machine ssh '$(PODMAN_MACHINE)' "cd '$(PODMAN_VM_REPO)' && if ! ./scripts/podman-machine-compose-check.sh >/dev/null 2>&1; then echo 'No compose provider is available inside the Podman machine. Run make podman-vm-bootstrap-compose and retry.' >&2; exit 1; fi"

.PHONY: podman-vm-up
podman-vm-up: podman-vm-check
	podman machine ssh '$(PODMAN_MACHINE)' "$(PODMAN_VM_COMPOSE) && ./scripts/podman-machine-compose.sh up -d --build --force-recreate"

.PHONY: podman-vm-smoke
podman-vm-smoke: podman-vm-up
	podman machine ssh '$(PODMAN_MACHINE)' "cd '$(PODMAN_VM_REPO)' && COMPOSE_CMD='./scripts/podman-machine-compose.sh' ./scripts/podman-smoke.sh"

.PHONY: podman-vm-down
podman-vm-down: podman-machine-rootful
	podman machine ssh '$(PODMAN_MACHINE)' "cd '$(PODMAN_VM_REPO)' && ./scripts/podman-machine-compose.sh down -v --remove-orphans"

.PHONY: podman-vm-shell
podman-vm-shell: podman-machine-sync
	podman machine ssh '$(PODMAN_MACHINE)' "cd '$(PODMAN_VM_REPO)' && bash -l"

.PHONY: podman-linux-up
podman-linux-up:
	mkdir -p brain_data/projects brain_data/traefik/dynamic brain_data/job-logs
	sudo podman compose -f podman-compose.yml up -d --build --force-recreate

.PHONY: podman-linux-smoke
podman-linux-smoke:
	COMPOSE_CMD='sudo podman compose -f podman-compose.yml' ./scripts/podman-smoke.sh

.PHONY: podman-linux-down
podman-linux-down:
	sudo podman compose -f podman-compose.yml down -v --remove-orphans
