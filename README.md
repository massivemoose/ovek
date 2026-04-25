# ovek

Ovek is a local-first control plane for building and running app projects through Brain.

## Brain Deploy Flow

The current Brain deployment path is:

1. `git clone`
2. `railpack prepare`
3. `buildctl build`
4. push the built image to the local registry
5. pull that registry-backed image into the runtime
6. start the managed app and PocketBase containers

The older `railpack build --output` plus `docker import` path is no longer the active build flow.

## Local Dev Topology

The local Compose stack includes:

- `brain`: the Brain API and deploy worker
- `buildkitd`: the standalone BuildKit daemon Brain talks to through `buildctl`
- `registry`: the local registry used as the build/runtime handoff
- `traefik`: the edge router for `brain.localhost` and deployed apps at `<project>.localhost`

Brain creates tenant app containers and per-project PocketBase sidecars dynamically through the selected runtime engine. Those app containers are not part of the static Compose file.

Traefik no longer watches container labels directly. Brain now owns the routing source of truth by writing dynamic file-provider config under `TRAEFIK_DYNAMIC_CONFIG_DIR`.

Current project runtime reads now include:

- `GET /v1/projects/{projectName}/runtime`
- `GET /v1/projects/{projectName}/runtime/logs`

## Registry And Builder Config

Brain currently supports these registry- and builder-related settings:

- `RUNTIME_ENGINE`
  - runtime engine Brain uses for managed app and PocketBase lifecycle
  - supported values: `docker`, `podman`
  - local default: `docker`
- `RUNTIME_HOST`
  - optional runtime API socket/host override
  - Podman default when `RUNTIME_ENGINE=podman`: `unix:///run/podman/podman.sock`
- `BUILDKIT_HOST`
  - BuildKit endpoint used by `railpack prepare` and `buildctl`
  - local default: `docker-container://buildkit`
- `BUILD_REGISTRY_PUBLISH_HOST`
  - registry host written into the builder-side pushed image ref
  - local default: `host.docker.internal:5001`
- `RUNTIME_REGISTRY_HOST`
  - registry host stored in Brain state and used for runtime pulls
  - local default: `localhost:5001`
- `REGISTRY_API_BASE_URL`
  - Brain-internal registry API endpoint used for managed artifact cleanup
  - local default: `http://registry:5000`
- `RAILPACK_FRONTEND_IMAGE`
  - Railpack frontend image passed to `buildctl`
  - local default: `ghcr.io/railwayapp/railpack-frontend`
- `REGISTRY_INSECURE`
  - enables insecure-registry push behavior for the local registry path
  - local default: `true`
- `TRAEFIK_DYNAMIC_CONFIG_DIR`
  - directory where Brain writes Traefik file-provider config
  - local default: `/var/lib/ovek/traefik/dynamic`
- `TRAEFIK_BRAIN_SERVICE_URL`
  - internal Brain URL Traefik should route `brain.localhost` to
  - local default: `http://brain:8081`

### Why Local Dev Uses Two Registry Hosts

Local Docker Desktop development currently uses an intentional split:

- builder publish host: `host.docker.internal:5001`
- runtime pull host: `localhost:5001`

This hides a Docker Desktop reachability mismatch behind config:

- the builder path runs inside the Brain container and needs a host name that resolves back to the host-published registry port
- the runtime image ref stored by Brain should stay usable from the local Docker runtime, where `localhost:5001` is the right pull address

If you run Ovek in a different environment, update `BUILD_REGISTRY_PUBLISH_HOST`, `RUNTIME_REGISTRY_HOST`, and `REGISTRY_API_BASE_URL` together so:

1. BuildKit can push successfully
2. the runtime engine can pull successfully
3. Brain can reach the registry API for managed cleanup

See [docs/registry-model.md](docs/registry-model.md) for the full registry trust model, Docker/Podman host split, and local insecure-registry assumptions.

## Local Compose Defaults

The local `docker-compose.yml` keeps the dev registry behavior explicit:

- `RUNTIME_ENGINE=docker`
- `BUILD_REGISTRY_PUBLISH_HOST=host.docker.internal:5001`
- `RUNTIME_REGISTRY_HOST=localhost:5001`
- `REGISTRY_API_BASE_URL=http://registry:5000`
- `REGISTRY_INSECURE=true`
- `TRAEFIK_DYNAMIC_CONFIG_DIR=/var/lib/ovek/traefik/dynamic`
- `TRAEFIK_BRAIN_SERVICE_URL=http://brain:8081`

The registry exposes host port `5001`, while the Brain container reaches its API over the internal Compose service name `registry:5000`.
Traefik reads dynamic config from the shared `brain_data/traefik` volume instead of watching the Docker provider.

## Podman Dev Scaffold

An initial Linux-first Podman stack lives in `podman-compose.yml`.

- It keeps the current BuildKit plus registry build flow.
- It switches Brain to `RUNTIME_ENGINE=podman`.
- It expects a rootful Podman service socket by default at `unix:///run/podman/podman.sock`.
- The default Podman scaffold uses a split registry path:
  - `BUILD_REGISTRY_PUBLISH_HOST=registry:5000`
  - `RUNTIME_REGISTRY_HOST=localhost:5001`
  - `REGISTRY_API_BASE_URL=http://registry:5000`
- These values stay configurable, but the split reflects the current topology where BuildKit and Brain live inside the compose network while the Podman runtime engine pulls from the VM host side.

## Linux-Faithful Podman Testing

Podman validation now uses two explicit lanes:

- a local macOS lane that runs the canonical Linux Podman stack inside `podman machine`
- a real Linux acceptance lane in CI using the same `podman-compose.yml`

The compose topology stays Linux-first. macOS uses helper tooling around `podman machine` instead of a second compose file.

Developer entrypoints:

- `make podman-machine-init`
- `make podman-machine-rootful`
- `make podman-vm-up`
- `make podman-vm-smoke`
- `make podman-vm-down`
- `make podman-linux-up`
- `make podman-linux-smoke`
- `make podman-linux-down`

The shared automated smoke path is `scripts/podman-smoke.sh`. It validates first deploy, redeploy, routed app reachability, runtime logs, Brain restart reconciliation, and cleanup.

See [docs/podman-testing.md](docs/podman-testing.md) for the full Mac VM workflow, optional hostname setup, and the real-Linux acceptance path.
See [docs/registry-model.md](docs/registry-model.md) for the registry host split that makes the Podman topology work.

## Managed Registry Artifact Cleanup

Brain now treats registry-backed deployment images as managed artifacts.

- When a deployment successfully supersedes an older deployment, Brain attempts to delete the superseded image manifest from the local managed registry.
- When `DELETE /v1/projects/{projectName}/runtime` succeeds, Brain also attempts to delete the project's stored deployment image manifests and managed build log files.
- Artifact cleanup is best-effort. Deploy success and runtime cleanup success do not get downgraded just because a manifest delete was skipped or failed.

Current scope:

- cleanup only targets refs that match `RUNTIME_REGISTRY_HOST`
- cleanup removes manifest reachability through the registry API
- cleanup only removes job log files that live under Brain's managed `job-logs` directory
- offline blob garbage collection is still out of scope

## Local Validation

Use the command-by-command flow in `.llms/test-plan.md` for the preferred local manual validation path.
