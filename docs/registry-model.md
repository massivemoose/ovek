# Registry Trust And Host Model

Ovek uses a local registry as the handoff point between build and runtime.
Brain does not currently import build output directly into the runtime engine.
Instead, each deployment moves through three registry interactions:

1. BuildKit pushes the built image.
2. The runtime engine pulls the promoted image.
3. Brain calls the registry HTTP API for best-effort managed artifact cleanup.

Those three interactions may need different hostnames because they run from
different network namespaces.

## The Three Registry Names

`BUILD_REGISTRY_PUBLISH_HOST` is the registry host written into the BuildKit
output ref. It must be reachable from the builder side of the stack.

`RUNTIME_REGISTRY_HOST` is the registry host stored in Brain state and used by
the runtime engine when pulling app images. It must be reachable from the
runtime engine's point of view.

`REGISTRY_API_BASE_URL` is the HTTP endpoint Brain uses for registry API calls,
such as deleting manifests for superseded or cleaned-up deployments. It must be
reachable from the Brain container.

These values should be changed together. A deployment can only work end to end
when:

1. BuildKit can push to `BUILD_REGISTRY_PUBLISH_HOST`.
2. the selected runtime engine can pull from `RUNTIME_REGISTRY_HOST`.
3. Brain can reach `REGISTRY_API_BASE_URL`.

## Local Docker Defaults

The Docker Compose development stack uses:

| Setting | Value | Consumer |
| --- | --- | --- |
| `BUILD_REGISTRY_PUBLISH_HOST` | `host.docker.internal:5001` | BuildKit push path |
| `RUNTIME_REGISTRY_HOST` | `localhost:5001` | Docker runtime pull path |
| `REGISTRY_API_BASE_URL` | `http://registry:5000` | Brain registry API path |

This split exists because the builder runs from inside the Brain/BuildKit
container context, while the Docker runtime pull is executed by the host Docker
daemon. The registry service is also available to Brain over the Compose
network as `registry:5000`.

## Local Podman Defaults

The Linux-first Podman stack uses:

| Setting | Value | Consumer |
| --- | --- | --- |
| `BUILD_REGISTRY_PUBLISH_HOST` | `registry:5000` | BuildKit push path |
| `RUNTIME_REGISTRY_HOST` | `localhost:5001` | rootful Podman runtime pull path |
| `REGISTRY_API_BASE_URL` | `http://registry:5000` | Brain registry API path |

In the Podman topology, Brain and BuildKit live inside the compose network, so
BuildKit can push to the registry service name directly. The runtime pull is
performed by the rootful Podman service on the Linux host or Podman VM, where
the registry is reachable through the host-published `localhost:5001` port.

The macOS `podman machine` lane intentionally runs this same Linux-first
topology inside the VM. The split should be understood from the VM's point of
view, not from macOS directly:

- BuildKit pushes to `registry:5000` inside the VM compose network.
- Brain reaches the registry API at `http://registry:5000` inside the VM compose network.
- rootful Podman pulls runtime images from `localhost:5001` on the VM host side.

## Trust Model

The current local registry path is intentionally development-scoped.

- The registry is plain HTTP and is configured as insecure with `REGISTRY_INSECURE=true`.
- BuildKit receives `registry.insecure=true` for local pushes.
- The Podman runtime pull path uses the native Libpod image pull API with `tlsVerify=false` when `REGISTRY_INSECURE=true`.
- Docker runtime pulls rely on the local Docker daemon accepting the configured local registry.
- Brain only treats image refs matching `RUNTIME_REGISTRY_HOST` as managed cleanup targets.

Do not use these local defaults as production security guidance. A hardened
install should either use TLS for the registry or explicitly provision the
runtime and builder trust configuration for the intended private registry.

## Operational Rules

- Keep Docker as the default runtime path unless `RUNTIME_ENGINE=podman` is explicitly set.
- Keep the local registry host split explicit in compose files instead of hiding it in code.
- Store runtime image refs using `RUNTIME_REGISTRY_HOST`, because that is the name future runtime operations need.
- Use `REGISTRY_API_BASE_URL` only for Brain-to-registry API calls; do not store it in deployment image refs.
- Treat registry manifest deletion as best effort. Deleting manifest reachability is not the same as immediate blob garbage collection.
