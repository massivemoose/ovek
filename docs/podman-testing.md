# Podman Testing

Ovek now keeps two explicit validation lanes for Podman:

- Mac-hosted Linux sandboxing through `podman machine`
- real Linux acceptance on Ubuntu CI using the same `podman-compose.yml`

The Linux-first `podman-compose.yml` remains the canonical Podman topology. The Mac workflow runs that exact stack inside the machine instead of trying to make the compose file understand macOS path and socket differences.

See [registry-model.md](registry-model.md) for the registry trust model and the reason the Podman topology uses separate BuildKit push, runtime pull, and Brain registry API hosts.

## Mac Linux-Sandbox Workflow

Prerequisites:

- Podman Desktop or Podman CLI is installed and on your `PATH`
- a Podman machine exists locally
- the machine can run in `rootful` mode

Recommended command flow:

1. `make podman-machine-init`
2. `make podman-machine-rootful`
3. `make podman-vm-bootstrap-compose`
4. `make podman-vm-up`
5. `make podman-vm-smoke`
6. `make podman-vm-down`

What these targets do:

- `podman-machine-rootful` ensures the machine is in `rootful=true` mode before starting it.
- `podman-machine-sync` copies the repo into the VM with explicit excludes from `.podman-machine-syncignore`.
- `podman-vm-bootstrap-compose` installs `podman-compose` inside the Podman machine if no compose provider is present yet.
- `podman-vm-up` runs the canonical `podman-compose.yml` inside the VM.
- `podman-vm-up` rebuilds and force-recreates the Podman stack so code changes in Brain actually land in the running control-plane container between iterations.
- `podman-vm-smoke` runs the shared Linux Podman smoke suite inside the VM.
- The VM startup helper also precreates `brain_data/projects`, `brain_data/traefik/dynamic`, and `brain_data/job-logs` so Traefik's file provider and Brain's local state paths exist before containers start.

The VM copy is disposable test state. It is intentionally not treated as a bidirectional workspace.
The helper sync preserves `brain_data` across resyncs so repeated `make podman-vm-up` and `make podman-vm-smoke` runs do not delete live bind-mount sources out from under running containers.

### Manual VM Wrapper

Use `./pm` for Docker-like manual inspection against the Podman machine lane. It prints the underlying command before running it so the VM indirection stays visible.

Common commands:

1. `./pm up`
2. `./pm smoke`
3. `./pm compose ps`
4. `./pm compose logs brain`
5. `./pm podman ps -a`
6. `./pm logs <CONTAINER_NAME>`
7. `./pm api GET /v1/ping`
8. `./pm api GET /v1/projects/demo-app/runtime`
9. `./pm app demo-app /`
10. `./pm down`

The HTTP helpers curl through the forwarded Traefik port on the Mac host and inject the required `Host:` headers. `./pm api` also injects the local dev API key.

### Sync Excludes

The VM sync path excludes:

- `.git`
- `brain_data`
- `pb_data`
- common cache/build directories such as `node_modules`, `.cache`, `dist`, and `coverage`

If additional generated directories appear later, add them to `.podman-machine-syncignore` so the sync stays fast.

### Hostname Ergonomics

The required smoke path is `curl`-first and uses explicit `Host:` headers, so browser DNS is not part of the baseline requirement.

If you want browser testing on macOS, optional `/etc/hosts` entries like the following are enough for one Brain route and one demo project route:

1. `echo '127.0.0.1 brain.localhost demo-app.localhost' | sudo tee -a /etc/hosts`

This is convenience only. The smoke harness does not depend on it.

### Compose Provider Note

The helper targets expect `sudo podman compose` to work inside the Podman machine.

If `make podman-vm-check` fails because no compose provider is installed in the VM, run `make podman-vm-bootstrap-compose` and retry. Keep that install scoped to the VM; do not change the canonical compose file to work around the missing provider.

## Real Linux Workflow

Use the same compose file and the same smoke runner on a Linux host:

1. `sudo systemctl start podman.socket`
2. `make podman-linux-up`
3. `make podman-linux-smoke`
4. `make podman-linux-down`

This is the same path used by CI and is the baseline proof that the Linux Podman contract still works.

## What The Smoke Suite Verifies

- Brain starts and answers at `brain.localhost`
- first deploy succeeds
- build logs include `railpack prepare` and `buildctl build`
- current runtime is readable
- runtime logs are readable
- routed app responds through Traefik
- second deploy replaces the first runtime image
- Brain restart preserves the promoted runtime
- cleanup removes current runtime state and returns the project to `idle`

Current limits:

- the automated smoke suite checks PocketBase provisioning and reconciliation indirectly through deploy/runtime success, but it does not yet assert application-level data persistence through PocketBase writes
- SELinux labeling and hardened-host bind-mount checks still need a true Linux follow-up outside the macOS VM lane

Current Podman baseline note:

- `podman-compose.yml` uses Podman SELinux relabeling on host bind mounts.
- Shared paths such as the Traefik dynamic config and project data use `:z` so multiple containers can read and write the same content.
- The Brain container disables SELinux separation with `security_opt: ["label=disable"]` when mounting `/run/podman/podman.sock`. Podman documents this as the safer approach for system paths under `/run`, rather than relabeling them.
- The Podman dev scaffold uses a split registry host model:
  - `BUILD_REGISTRY_PUBLISH_HOST=registry:5000` for in-network BuildKit pushes
  - `RUNTIME_REGISTRY_HOST=localhost:5001` for host-side Podman runtime pulls
  - `REGISTRY_API_BASE_URL=http://registry:5000` for Brain-to-registry cleanup calls
