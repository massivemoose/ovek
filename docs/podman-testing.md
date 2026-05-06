# Podman Testing

Ovek's MVP runtime path is Podman-first. The default `podman-compose.yml` stack is runtime-only: Brain plus Traefik. It is used for local macOS testing through `podman machine`, Linux acceptance, and the Ubuntu VPS trial.

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
5. `make podman-vm-capsule-smoke`
6. `make podman-vm-down`

What these targets do:

- `podman-machine-rootful` ensures the machine is in `rootful=true` mode before starting it.
- `podman-machine-sync` copies the repo into the VM with explicit excludes from `.podman-machine-syncignore`.
- `podman-vm-bootstrap-compose` installs `podman-compose` inside the Podman machine if no compose provider is present yet.
- `podman-vm-up` runs the runtime-only `podman-compose.yml` inside the VM.
- `podman-vm-capsule-smoke` runs the image-first capsule smoke suite from the host against the VM-exposed Traefik port.
- The VM startup helper precreates `brain_data/projects`, `brain_data/traefik/dynamic`, and `brain_data/job-logs` so Brain and Traefik have stable bind-mount sources.

The VM copy is disposable test state. It is intentionally not treated as a bidirectional workspace.

## Manual VM Wrapper

Use `./pm` for manual inspection against the Podman machine lane. It prints the underlying command before running it so the VM indirection stays visible.

Common commands:

1. `./pm up`
2. `./pm capsule-smoke`
3. `./pm compose ps`
4. `./pm compose logs brain`
5. `./pm podman ps -a`
6. `./pm logs <CONTAINER_NAME>`
7. `./pm api GET /v1/ping`
8. `./pm api GET /v1/projects/signup-demo/runtime`
9. `./pm app signup-demo /`
10. `./pm down`

The HTTP helpers curl through the forwarded Traefik port on the Mac host and inject the required `Host:` headers. `./pm api` also injects the local dev API key.

## Hostname Ergonomics

The capsule smoke path uses the real `ovek` CLI, so `brain.localhost` must resolve on the host running the script. If your resolver does not already map `.localhost` names to loopback, add entries like the following:

1. `echo '127.0.0.1 brain.localhost signup-demo.localhost' | sudo tee -a /etc/hosts`

The capsule smoke still checks routed app reachability with an explicit `Host:` header; the hostname requirement is for CLI access to Brain.

## Real Linux Workflow

Use the runtime-only compose file and capsule smoke runner on a Linux host:

1. `sudo systemctl start podman.socket`
2. `make podman-linux-up`
3. `make podman-linux-capsule-smoke`
4. `make podman-linux-down`

This is the same path used by CI and is the baseline proof that the Linux Podman contract works.

For a real Ubuntu VPS trial over an SSH tunnel, use [vps-trial.md](vps-trial.md).

## Legacy Source-Build Stack

The old server-side source-build flow is still available for internal regression, but it is not the MVP acceptance path. It starts BuildKit and the local registry through `podman-compose.builder.yml`.

On macOS with Podman Machine:

1. `make podman-vm-builder-up`
2. `make podman-vm-builder-smoke`
3. `make podman-vm-builder-down`

On Linux:

1. `make podman-linux-builder-up`
2. `make podman-linux-builder-smoke`
3. `make podman-linux-builder-down`

## What Capsule Smoke Verifies

`scripts/podman-capsule-smoke.sh` covers the image-first user flow:

- builds or reuses `./bin/ovek`
- logs into Brain with an isolated temporary CLI config
- ensures PocketBase app secrets exist for the signup example
- runs `ghcr.io/massivemoose/ovek-signup-example:latest` with `ovek run`
- verifies image-run job logs, runtime logs, routed app reachability, database status, and cleanup

Current limits:

- capsule smoke checks PocketBase provisioning and status, but it does not yet assert application-level data persistence through PocketBase record writes
- SELinux labeling and hardened-host bind-mount checks still need a true Linux follow-up outside the macOS VM lane
