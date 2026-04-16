# alces

Alces is a local-first control plane for building and running app projects through Brain.

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

Brain creates tenant app containers and per-project PocketBase sidecars dynamically through the Docker API. Those app containers are not part of the static Compose file.

Current project runtime reads now include:

- `GET /v1/projects/{projectName}/runtime`
- `GET /v1/projects/{projectName}/runtime/logs`

## Registry And Builder Config

Brain currently supports these registry- and builder-related settings:

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

### Why Local Dev Uses Two Registry Hosts

Local Docker Desktop development currently uses an intentional split:

- builder publish host: `host.docker.internal:5001`
- runtime pull host: `localhost:5001`

This hides a Docker Desktop reachability mismatch behind config:

- the builder path runs inside the Brain container and needs a host name that resolves back to the host-published registry port
- the runtime image ref stored by Brain should stay usable from the local Docker runtime, where `localhost:5001` is the right pull address

If you run Alces in a different environment, update `BUILD_REGISTRY_PUBLISH_HOST`, `RUNTIME_REGISTRY_HOST`, and `REGISTRY_API_BASE_URL` together so:

1. BuildKit can push successfully
2. the runtime engine can pull successfully
3. Brain can reach the registry API for managed cleanup

## Local Compose Defaults

The local `docker-compose.yml` keeps the dev registry behavior explicit:

- `BUILD_REGISTRY_PUBLISH_HOST=host.docker.internal:5001`
- `RUNTIME_REGISTRY_HOST=localhost:5001`
- `REGISTRY_API_BASE_URL=http://registry:5000`
- `REGISTRY_INSECURE=true`

The registry exposes host port `5001`, while the Brain container reaches its API over the internal Compose service name `registry:5000`.

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
